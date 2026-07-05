// Package api exposes the management REST API and the live-traffic SSE endpoint.
package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/spec"
	"github.com/wcrum/gofi/tapstream"
)

// Handler wires the REST routes and the SSE stream endpoint.
type Handler struct {
	ctrl   *controller.Controller
	static fs.FS // embedded SPA; may be nil
	mode   deploymode.Mode
}

// New builds the chi router. Pass a non-nil staticFS to serve the SPA at "/".
func New(ctrl *controller.Controller, staticFS fs.FS, mode deploymode.Mode) http.Handler {
	h := &Handler{ctrl: ctrl, static: staticFS, mode: mode}
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// REST API
	r.Route("/api", func(r chi.Router) {
		r.Post("/listeners", h.createListener)
		r.Get("/listeners", h.listListeners)
		r.Get("/listeners/{name}", h.getListener)
		r.Delete("/listeners/{name}", h.deleteListener)

		// SSE: live traffic tap for one listener
		r.Get("/listeners/{name}/stream", h.streamListener)

		// Runtime status: which container/Pod (if any) is backing a listener.
		r.Get("/listeners/{name}/status", h.listenerStatus)

		r.Post("/simulate", h.simulate)

		r.Get("/mode", h.getMode)
	})

	// SPA — serve index.html for all non-API routes
	if staticFS != nil {
		fileServer := http.FileServer(http.FS(staticFS))
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			fileServer.ServeHTTP(w, r)
		})
	}

	return r
}

// --- Listener CRUD ---

func (h *Handler) createListener(w http.ResponseWriter, r *http.Request) {
	var def spec.ListenerDefinition
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if def.Name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}
	if def.Source.Port == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("source.port is required"))
		return
	}
	if _, err := filter.NewChain(def.Stages); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.ctrl.Add(def); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	slog.Info("listener created via API", "name", def.Name, "port", def.Source.Port)
	writeJSON(w, http.StatusCreated, def)
}

func (h *Handler) listListeners(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.ctrl.List())
}

func (h *Handler) getListener(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	def, ok := h.ctrl.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("listener %q not found", name))
		return
	}
	writeJSON(w, http.StatusOK, def)
}

func (h *Handler) deleteListener(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if _, ok := h.ctrl.Get(name); !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("listener %q not found", name))
		return
	}
	h.ctrl.Remove(name)
	slog.Info("listener deleted via API", "name", name)
	w.WriteHeader(http.StatusNoContent)
}

// --- Mock-mode simulation ---
//
// Pure dry-run: runs the request body's ListenerDefinition + raw bytes through
// pipeline.Simulate. Deliberately independent of the controller — the listener
// need not exist or be registered, so the editor can simulate an unsaved draft.
// Zero side effects: never dispatches to a real sink, never touches h.ctrl.

type simulateRequest struct {
	Listener spec.ListenerDefinition `json:"listener"`
	Raw      string                  `json:"raw"`
}

func (h *Handler) simulate(w http.ResponseWriter, r *http.Request) {
	var req simulateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := pipeline.Simulate(req.Listener, []byte(req.Raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// --- Deployment info ---

func (h *Handler) getMode(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"mode":        string(h.mode),
		"processRole": "controller",
		"driverKind":  h.ctrl.DriverKind(),
	})
}

// --- SSE live traffic stream ---
//
// The tap for a given listener may live in this process (InProcess driver)
// or in a sibling container/Pod (Docker/Kubernetes drivers). driver.TapTarget
// tells us which: Local is served directly via tapstream.Serve, Addr is
// reverse-proxied to the remote listener's own tap relay (see taprelay).

func (h *Handler) streamListener(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	target, ok, err := h.ctrl.Tap(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("listener %q not found or not running", name))
		return
	}

	if target.Local != nil {
		_ = tapstream.Serve(r.Context(), w, target.Local)
		return
	}

	if target.Addr == "" {
		writeError(w, http.StatusNotImplemented, fmt.Errorf("live view is not available for listener %q in this deployment mode yet", name))
		return
	}

	h.proxyTapStream(w, r, target.Addr)
}

// proxyTapStream reverse-proxies an SSE request to a remote listener's tap
// relay server (see taprelay package), rewriting the path to /stream.
func (h *Handler) proxyTapStream(w http.ResponseWriter, r *http.Request, addr string) {
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: addr})
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.URL.Path = "/stream"
		req.Host = addr
	}
	proxy.ServeHTTP(w, r)
}

// --- Runtime status ---

func (h *Handler) listenerStatus(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	info, ok, err := h.ctrl.RuntimeStatus(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("listener %q not found", name))
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
