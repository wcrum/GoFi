package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/tapstream"
)

// layerController labels the hub's per-browser queues in metrics.
const layerController = "controller"

// trafficHub serves every WatchTraffic viewer from one upstream subscription
// per flow. An event arrives from the runtime already converted (or is
// converted once for in-process flows), is wrapped in one immutable response,
// and is offered to each viewer's bounded queue; a viewer that falls behind
// loses events, counted on its stream, and never delays another viewer or the
// runtime.
type trafficHub struct {
	ctrl    *controller.Controller
	cfg     hubConfig
	clients *upstreamClients
	set     *upstreamSet

	mu      sync.RWMutex
	viewers map[*trafficViewer]struct{}
}

type trafficViewer struct {
	flow    string // "" watches every flow
	ch      chan *gofiv1.WatchTrafficResponse
	dropped atomic.Uint64
}

func newTrafficHub(ctrl *controller.Controller, cfg hubConfig, clients *upstreamClients) *trafficHub {
	h := &trafficHub{ctrl: ctrl, cfg: cfg, clients: clients, viewers: map[*trafficViewer]struct{}{}}
	h.set = newUpstreamSet("traffic", cfg, func() []string { return flowNames(ctrl) }, h.run)
	return h
}

func flowNames(ctrl *controller.Controller) []string {
	defs := ctrl.List()
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

// subscribe registers a viewer; release detaches it.
func (h *trafficHub) subscribe(flow string) (viewer *trafficViewer, release func()) {
	viewer = &trafficViewer{flow: flow, ch: make(chan *gofiv1.WatchTrafficResponse, h.cfg.queue)}
	h.mu.Lock()
	h.viewers[viewer] = struct{}{}
	h.mu.Unlock()
	observability.TapSubscribed(layerController, 1)
	unwant := h.set.want(flow)
	return viewer, func() {
		h.mu.Lock()
		delete(h.viewers, viewer)
		h.mu.Unlock()
		observability.TapSubscribed(layerController, -1)
		unwant()
	}
}

// viewerCount reports how many browser streams the hub serves.
func (h *trafficHub) viewerCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.viewers)
}

// dispatch offers one converted event to every interested viewer.
func (h *trafficHub) dispatch(flow string, event *gofiv1.TrafficEvent) {
	response := &gofiv1.WatchTrafficResponse{Flow: flow, Event: event}
	var dropped uint64
	h.mu.RLock()
	for viewer := range h.viewers {
		if viewer.flow != "" && viewer.flow != flow {
			continue
		}
		select {
		case viewer.ch <- response:
		default:
			viewer.dropped.Add(1)
			dropped++
		}
	}
	h.mu.RUnlock()
	observability.TapDroppedAt(layerController, dropped)
}

// run is one flow's upstream: resolve the runtime, stream from it, repeat.
func (h *trafficHub) run(ctx context.Context, name string, kick <-chan struct{}) {
	h.set.resolveLoop(ctx, h.ctrl, name, kick,
		func(ctx context.Context, target driver.TapTarget) error {
			sub := tapstream.Shared(target.Local, tapstream.MaxBytes()).Subscribe(h.cfg.queue)
			defer sub.Close()
			for {
				select {
				case <-ctx.Done():
					return nil
				case event := <-sub.Events():
					h.dispatch(name, event)
				}
			}
		},
		func(ctx context.Context, target driver.TapTarget) error {
			return h.streamRemote(ctx, name, target.Addr)
		})
}

// streamRemote relays one WatchTraffic session from a runtime. It tries the
// shared h2c client first and falls back to HTTP/1.1 for a runtime that
// never answered h2c, remembering the choice per address.
func (h *trafficHub) streamRemote(ctx context.Context, name, addr string) error {
	var lastErr error
	for i, client := range h.clients.order(addr) {
		remote := gofiv1connect.NewGoFiServiceClient(client, "http://"+addr)
		stream, err := remote.WatchTraffic(ctx, connect.NewRequest(&gofiv1.WatchTrafficRequest{Name: name}))
		if err != nil {
			lastErr = err
			continue
		}
		received := false
		for stream.Receive() {
			received = true
			msg := stream.Msg()
			if msg.Event != nil {
				h.dispatch(name, msg.Event)
			}
		}
		lastErr = stream.Err()
		_ = stream.Close()
		if received {
			h.clients.learn(addr, i > 0)
			return lastErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

// WatchTraffic streams live events for one flow or every flow. Each message
// carries how many events this stream skipped since the previous one.
func (h *Handler) WatchTraffic(ctx context.Context, req *connect.Request[gofiv1.WatchTrafficRequest], stream *connect.ServerStream[gofiv1.WatchTrafficResponse]) error {
	if req.Msg.Name != "" {
		if _, found := h.ctrl.Get(req.Msg.Name); !found {
			return rpcError(connect.CodeNotFound, errors.New("flow not found"))
		}
	}
	release, err := h.caps.acquire("traffic", clientKey(req))
	if err != nil {
		return err
	}
	defer release()
	viewer, detach := h.traffic.subscribe(req.Msg.Name)
	defer detach()
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
			if err := stream.Send(&gofiv1.WatchTrafficResponse{Heartbeat: true, Dropped: viewer.dropped.Swap(0)}); err != nil {
				return err
			}
		case response := <-viewer.ch:
			if dropped := viewer.dropped.Swap(0); dropped > 0 {
				// The shared response is immutable; a copy carries this viewer's count.
				response = &gofiv1.WatchTrafficResponse{Flow: response.Flow, Event: response.Event, Dropped: dropped}
			}
			if err := stream.Send(response); err != nil {
				return err
			}
		}
	}
}

// streamingHandlerOptions compresses unary responses above 1 KiB when the
// client accepts it (browsers always send Accept-Encoding: gzip) and leaves
// small messages alone.
func handlerOptions() []connect.HandlerOption {
	return []connect.HandlerOption{connect.WithCompressMinBytes(1024)}
}

// withStreamDeadlines wraps the mounted service with per-write deadlines on
// its streaming procedures.
func withStreamDeadlines(service http.Handler) http.Handler {
	return tapstream.WithWriteDeadline(service, tapstream.WriteTimeout())
}
