// Package taprelay exposes a listener process's traffic as the same typed
// ConnectRPC stream used by the portal, across Docker and Kubernetes networks.
package taprelay

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/tapstream"
)

type Server struct {
	gofiv1connect.UnimplementedGoFiServiceHandler
	tap *pipeline.Tap
}

func New(tap *pipeline.Tap) *Server { return &Server{tap: tap} }

func (s *Server) WatchTraffic(ctx context.Context, req *connect.Request[gofiv1.WatchTrafficRequest], stream *connect.ServerStream[gofiv1.WatchTrafficResponse]) error {
	ch := s.tap.Subscribe()
	defer s.tap.Unsubscribe(ch)
	if err := stream.Send(&gofiv1.WatchTrafficResponse{Heartbeat: true}); err != nil {
		return err
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-heartbeat.C:
			if err := stream.Send(&gofiv1.WatchTrafficResponse{Heartbeat: true}); err != nil {
				return err
			}
		case event := <-ch:
			converted, err := tapstream.Event(event)
			if err != nil || converted == nil {
				continue
			}
			if err := stream.Send(&gofiv1.WatchTrafficResponse{Flow: req.Msg.Name, Event: converted}); err != nil {
				return err
			}
		}
	}
}

// GetRouteHealth reports this listener process's outbound routes.
func (s *Server) GetRouteHealth(_ context.Context, req *connect.Request[gofiv1.GetRouteHealthRequest]) (*connect.Response[gofiv1.GetRouteHealthResponse], error) {
	return connect.NewResponse(&gofiv1.GetRouteHealthResponse{Routes: tapstream.Routes(req.Msg.Name)}), nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", observability.Handler())
	path, handler := gofiv1connect.NewGoFiServiceHandler(s)
	mux.Handle(path, handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = srv.Close()
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
