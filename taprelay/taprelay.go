// Package taprelay exposes a listener process's traffic as the same typed
// ConnectRPC stream used by the portal, across Docker and Kubernetes networks.
package taprelay

import (
	"context"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/tapstream"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// StatsInterval is how often WatchFlowStats sends the counters.
const StatsInterval = time.Second

// Server relays one runtime's live traffic and counters. Every viewer shares
// one subscription to the pipeline tap (tapstream.Hub): an event is converted
// once, and a slow viewer only loses its own events.
type Server struct {
	gofiv1connect.UnimplementedGoFiServiceHandler
	tap *pipeline.Tap
	hub *tapstream.Hub
}

func New(tap *pipeline.Tap) *Server {
	return &Server{tap: tap, hub: tapstream.NewHub(tap, tapstream.MaxBytes())}
}

// Subscribers reports how many WatchTraffic viewers are attached.
func (s *Server) Subscribers() int { return s.hub.Subscribers() }

func (s *Server) WatchTraffic(ctx context.Context, req *connect.Request[gofiv1.WatchTrafficRequest], stream *connect.ServerStream[gofiv1.WatchTrafficResponse]) error {
	sub := s.hub.Subscribe(tapstream.DefaultQueue)
	defer sub.Close()
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
			if err := stream.Send(&gofiv1.WatchTrafficResponse{Heartbeat: true, Dropped: sub.TakeDropped()}); err != nil {
				return err
			}
		case event := <-sub.Events():
			if err := stream.Send(&gofiv1.WatchTrafficResponse{Flow: req.Msg.Name, Event: event, Dropped: sub.TakeDropped()}); err != nil {
				return err
			}
		}
	}
}

// WatchFlowStats sends this runtime's counters for its flow once a second.
func (s *Server) WatchFlowStats(ctx context.Context, req *connect.Request[gofiv1.WatchFlowStatsRequest], stream *connect.ServerStream[gofiv1.WatchFlowStatsResponse]) error {
	send := func() error {
		stats := tapstream.Stats(req.Msg.Name)
		stats.Up = true
		return stream.Send(&gofiv1.WatchFlowStatsResponse{Flows: []*gofiv1.FlowStats{stats}, At: timestamppb.Now()})
	}
	if err := send(); err != nil {
		return err
	}
	tick := time.NewTicker(StatsInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if err := send(); err != nil {
				return err
			}
		}
	}
}

// GetRouteHealth reports this listener process's outbound routes.
func (s *Server) GetRouteHealth(_ context.Context, req *connect.Request[gofiv1.GetRouteHealthRequest]) (*connect.Response[gofiv1.GetRouteHealthResponse], error) {
	return connect.NewResponse(&gofiv1.GetRouteHealthResponse{Routes: tapstream.Routes(req.Msg.Name)}), nil
}

// Handler serves the relay over HTTP/1.1 and cleartext HTTP/2 (h2c), so the
// controller multiplexes every stream to this runtime over one connection.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", observability.Handler())
	path, handler := gofiv1connect.NewGoFiServiceHandler(s)
	mux.Handle(path, handler)
	path, handler = gofiv1connect.NewOperationsServiceHandler(operations{})
	mux.Handle(path, handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return h2c.NewHandler(tapstream.WithWriteDeadline(mux, tapstream.WriteTimeout()), &http2.Server{IdleTimeout: 5 * time.Minute})
}
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve serves the relay on an already-bound listener until ctx ends, so a
// runtime can bind every port it needs before it starts anything.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
