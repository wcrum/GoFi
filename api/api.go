// Package api exposes the typed ConnectRPC management and live traffic API.
package api

import (
	"context"
	"github.com/wcrum/gofi/assistant"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/history"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sandbox"
)

type Handler struct {
	gofiv1connect.UnimplementedGoFiServiceHandler
	ctrl *controller.Controller
	mode deploymode.Mode
	// sandbox runs Python debug sessions for DebugFrame (nil without workers).
	sandbox *sandbox.Supervisor
	// history records a revision for every save and delete (nil: none).
	history *history.Recorder
	// Live streaming: one upstream per watched flow shared by every browser
	// (stream.go, stats.go), bounded by caps (limits.go).
	caps    *streamCaps
	traffic *trafficHub
	stats   *statsHub
	// flows caches the ListFlows response per controller revision (connect.go).
	flows flowSnapshot
}

// New mounts ConnectRPC alongside health probes and the embedded portal.
func New(ctrl *controller.Controller, staticFS fs.FS, mode deploymode.Mode, supervisors ...*sandbox.Supervisor) http.Handler {
	return NewWithHistory(ctrl, staticFS, mode, nil, supervisors...)
}

// NewWithHistory is New plus FlowHistoryService, backed by rec.
func NewWithHistory(ctrl *controller.Controller, staticFS fs.FS, mode deploymode.Mode, rec *history.Recorder, supervisors ...*sandbox.Supervisor) http.Handler {
	return NewWithDeps(ctrl, staticFS, mode, Deps{History: rec}, supervisors...)
}

// Deps are the optional services the API mounts when present.
type Deps struct {
	History *history.Recorder
	// Assistant mounts AssistantService (GoFiBot); Docs is the documentation
	// it can read (docs/*.md), or nil.
	Assistant *assistant.Manager
	Docs      fs.FS
}

// NewWithDeps is New plus every optional service in deps.
func NewWithDeps(ctrl *controller.Controller, staticFS fs.FS, mode deploymode.Mode, deps Deps, supervisors ...*sandbox.Supervisor) http.Handler {
	rec := deps.History
	h := &Handler{ctrl: ctrl, mode: mode, history: rec}
	if len(supervisors) > 0 {
		h.sandbox = supervisors[0]
	}
	cfg := loadHubConfig()
	clients := newUpstreamClients()
	h.caps = newStreamCaps(cfg)
	h.traffic = newTrafficHub(ctrl, cfg, clients)
	h.stats = newStatsHub(ctrl, cfg, clients)
	// A Pod watcher (Kubernetes driver informer) re-resolves a flow's runtime
	// the moment its Pod changes; without one the hubs re-resolve on stream
	// failure and every GOFI_HUB_RESYNC.
	watchPods(context.Background(), ctrl.Driver(), func(flow string) { h.traffic.set.kick(flow); h.stats.set.kick(flow) })
	options := handlerOptions()
	r := chi.NewRouter()
	r.Use(structuredRequests)
	r.Handle("/metrics", observability.Handler())
	// /healthz is liveness only: the process serves HTTP. /readyz is 503
	// until the controller has loaded its store and completed a reconcile
	// pass, and while the runtime driver does not answer (controller.Ready).
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ok, reason := ctrl.Ready(); !ok {
			http.Error(w, "not ready: "+reason, http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	path, service := gofiv1connect.NewGoFiServiceHandler(h, options...)
	r.Mount(path, withStreamDeadlines(service))
	var sb *sandboxHandler
	if len(supervisors) > 0 && supervisors[0] != nil {
		sb = newSandboxHandler(supervisors[0])
		path, service := gofiv1connect.NewSandboxServiceHandler(sb, options...)
		r.Mount(path, withStreamDeadlines(service))
	}
	path, service = gofiv1connect.NewKubernetesServiceHandler(&kubernetesHandler{ctrl: ctrl}, options...)
	r.Mount(path, service)
	ops := newOperationsHandler(ctrl, rec)
	path, service = gofiv1connect.NewOperationsServiceHandler(ops, options...)
	r.Mount(path, service)
	var expr *expressionHandler
	if lib := ctrl.Expressions(); lib != nil {
		expr = &expressionHandler{ctrl: ctrl, lib: lib}
		path, service := gofiv1connect.NewExpressionServiceHandler(expr, options...)
		r.Mount(path, service)
	}
	if deps.Assistant != nil {
		path, service := gofiv1connect.NewAssistantServiceHandler(newAssistantHandler(h, expr, sb, ops, deps.Assistant, deps.Docs), options...)
		r.Mount(path, withStreamDeadlines(service))
		// The AI proxy (llm.go) is deliberately not mounted here: it has
		// its own listener, so a worker's egress never reaches these RPCs.
	}
	if rec != nil {
		path, service := gofiv1connect.NewFlowHistoryServiceHandler(&historyHandler{ctrl: ctrl, rec: rec}, options...)
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
