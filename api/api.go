// Package api exposes the management REST API and the live-traffic SSE endpoint.
package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/cnak-us/gogo/controller"
	"github.com/cnak-us/gogo/spec"
)

// Handler wires the REST routes and the SSE stream endpoint.
type Handler struct {
	ctrl   *controller.Controller
	static fs.FS // embedded SPA; may be nil
}

// New builds the chi router. Pass a non-nil staticFS to serve the SPA at "/".
func New(ctrl *controller.Controller, staticFS fs.FS) http.Handler {
	h := &Handler{ctrl: ctrl, static: staticFS}
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

// --- SSE live traffic stream ---

// TapEvent is the JSON shape sent over SSE for each observed message.
type sseEvent struct {
	Timestamp string                 `json:"ts"`
	Dropped   bool                   `json:"dropped"`
	Fields    map[string]interface{} `json:"fields"`
	Meta      map[string]string      `json:"meta"`
	Raw       string                 `json:"raw"`
}

func (h *Handler) streamListener(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ln := h.ctrl.Tap(name)
	if ln == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("listener %q not found or not running", name))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ch := ln.Tap.Subscribe()
	defer ln.Tap.Unsubscribe(ch)

	// Heartbeat every 15s so the browser doesn't time out.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case ev := <-ch:
			data := sseEvent{
				Timestamp: ev.Msg.Meta["ts"],
				Dropped:   ev.Dropped,
				Fields:    ev.Msg.Fields,
				Meta:      ev.Msg.Meta,
				Raw:       string(ev.Msg.Raw),
			}
			b, err := json.Marshal(data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
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
