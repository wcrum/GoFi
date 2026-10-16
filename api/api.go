// Package api exposes the typed ConnectRPC management and live traffic API.
package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sandbox"
)

type Handler struct {
	gofiv1connect.UnimplementedGoFiServiceHandler
	ctrl *controller.Controller
	mode deploymode.Mode
	// sandbox runs Python debug sessions for DebugFrame (nil without workers).
	sandbox *sandbox.Supervisor
}

// New mounts ConnectRPC alongside health probes and the embedded portal.
func New(ctrl *controller.Controller, staticFS fs.FS, mode deploymode.Mode, supervisors ...*sandbox.Supervisor) http.Handler {
	h := &Handler{ctrl: ctrl, mode: mode}
	if len(supervisors) > 0 {
		h.sandbox = supervisors[0]
	}
	r := chi.NewRouter()
	r.Use(structuredRequests)
	r.Handle("/metrics", observability.Handler())
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	path, service := gofiv1connect.NewGoFiServiceHandler(h)
	r.Mount(path, service)
	if len(supervisors) > 0 && supervisors[0] != nil {
		path, service := gofiv1connect.NewSandboxServiceHandler(&sandboxHandler{manager: supervisors[0]})
		r.Mount(path, service)
	}
	path, service = gofiv1connect.NewKubernetesServiceHandler(&kubernetesHandler{ctrl: ctrl})
	r.Mount(path, service)
	if lib := ctrl.Expressions(); lib != nil {
		path, service := gofiv1connect.NewExpressionServiceHandler(&expressionHandler{ctrl: ctrl, lib: lib})
		r.Mount(path, service)
	}
	// Previous application routes are intentionally retired.
	r.Handle("/api/*", http.NotFoundHandler())
	if staticFS != nil {
		fileServer := http.FileServer(http.FS(staticFS))
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path != "" && !strings.Contains(path, ".") {
				if _, err := fs.Stat(staticFS, path); err != nil {
					request := r.Clone(r.Context())
					request.URL.Path = "/"
					fileServer.ServeHTTP(w, request)
					return
				}
			}
			fileServer.ServeHTTP(w, r)
		})
	}
	return r
}
