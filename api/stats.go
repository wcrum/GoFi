package api

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/tapstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// statsInterval is how often WatchFlowStats sends; statsStale is how long
// after the last sample a flow still reads as up.
const (
	statsInterval = time.Second
	statsStale    = 3 * time.Second
)

// statsHub keeps one counters stream per watched flow and answers every
// WatchFlowStats viewer from the cache, so dashboards cost one small message
// per flow per second regardless of traffic or viewers.
type statsHub struct {
	ctrl    *controller.Controller
	cfg     hubConfig
	clients *upstreamClients
	set     *upstreamSet

	mu     sync.RWMutex
	latest map[string]statsSample
}

type statsSample struct {
	stats *gofiv1.FlowStats
	at    time.Time
}

func newStatsHub(ctrl *controller.Controller, cfg hubConfig, clients *upstreamClients) *statsHub {
	h := &statsHub{ctrl: ctrl, cfg: cfg, clients: clients, latest: map[string]statsSample{}}
	h.set = newUpstreamSet("stats", cfg, func() []string { return flowNames(ctrl) }, h.run)
	return h
}

func (h *statsHub) record(stats *gofiv1.FlowStats) {
	h.mu.Lock()
	h.latest[stats.Flow] = statsSample{stats: stats, at: time.Now()}
	h.mu.Unlock()
}

// snapshot builds the stats of the named flow, or of every flow, as of now.
// A flow without a fresh sample reads as down with its last known counters.
func (h *statsHub) snapshot(names []string, now time.Time) []*gofiv1.FlowStats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*gofiv1.FlowStats, 0, len(names))
	for _, name := range names {
		sample, ok := h.latest[name]
		if !ok {
			out = append(out, &gofiv1.FlowStats{Flow: name})
			continue
		}
		stats := sample.stats
		up := now.Sub(sample.at) < statsStale
		if stats.Up != up {
			copied := proto.Clone(stats).(*gofiv1.FlowStats)
			copied.Up = up
			stats = copied
		}
		out = append(out, stats)
	}
	return out
}

func (h *statsHub) run(ctx context.Context, name string, kick <-chan struct{}) {
	h.set.resolveLoop(ctx, h.ctrl, name, kick,
		func(ctx context.Context, _ driver.TapTarget) error {
			tick := time.NewTicker(statsInterval)
			defer tick.Stop()
			for {
				stats := tapstream.Stats(name)
				stats.Up = true
				h.record(stats)
				select {
				case <-ctx.Done():
					return nil
				case <-tick.C:
				}
			}
		},
		func(ctx context.Context, target driver.TapTarget) error {
			var lastErr error
			for i, client := range h.clients.order(target.Addr) {
				remote := gofiv1connect.NewGoFiServiceClient(client, "http://"+target.Addr)
				stream, err := remote.WatchFlowStats(ctx, connect.NewRequest(&gofiv1.WatchFlowStatsRequest{Name: name}))
				if err != nil {
					lastErr = err
					continue
				}
				received := false
				for stream.Receive() {
					received = true
					for _, stats := range stream.Msg().Flows {
						stats.Flow = name
						h.record(stats)
					}
				}
				lastErr = stream.Err()
				_ = stream.Close()
				if received {
					h.clients.learn(target.Addr, i > 0)
					return lastErr
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
			return lastErr
		})
}

// WatchFlowStats sends per-flow counters once a second.
func (h *Handler) WatchFlowStats(ctx context.Context, req *connect.Request[gofiv1.WatchFlowStatsRequest], stream *connect.ServerStream[gofiv1.WatchFlowStatsResponse]) error {
	if req.Msg.Name != "" {
		if _, found := h.ctrl.Get(req.Msg.Name); !found {
			return rpcError(connect.CodeNotFound, errors.New("flow not found"))
		}
	}
	release, err := h.caps.acquire("stats", clientKey(req))
	if err != nil {
		return err
	}
	defer release()
	defer h.stats.set.want(req.Msg.Name)()
	names := func() []string {
		if req.Msg.Name != "" {
			return []string{req.Msg.Name}
		}
		names := flowNames(h.ctrl)
		sort.Strings(names)
		return names
	}
	send := func() error {
		now := time.Now()
		return stream.Send(&gofiv1.WatchFlowStatsResponse{Flows: h.stats.snapshot(names(), now), At: timestamppb.New(now)})
	}
	if err := send(); err != nil {
		return err
	}
	tick := time.NewTicker(statsInterval)
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
