// Package taprelay runs a minimal HTTP server inside a listener-mode process
// (see main.go's runListenerMode) that exposes its pipeline.Tap over the
// network. This is the piece that makes live traffic viewing work when a
// listener is running as a separate Docker container or Kubernetes Pod,
// rather than in-process: the controller's API layer reverse-proxies SSE
// requests to this server (driver.TapTarget.Addr), reusing infrastructure
// that already exists in each environment (Compose's embedded DNS,
// Kubernetes' flat Pod-IP routing) instead of adding a new hard dependency
// like NATS just for tap streaming.
package taprelay

import (
	"context"
	"net/http"

	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/tapstream"
)

// Server exposes a single listener's tap over HTTP.
type Server struct {
	tap *pipeline.Tap
}

// New builds a tap relay server for the given tap.
func New(tap *pipeline.Tap) *Server {
	return &Server{tap: tap}
}

// ListenAndServe starts the relay's HTTP server on addr and blocks until ctx
// is cancelled or the server errors. It exposes:
//
//	GET /stream   - SSE live traffic stream (see tapstream.Serve)
//	GET /healthz  - trivial liveness check
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		_ = tapstream.Serve(r.Context(), w, s.tap)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
