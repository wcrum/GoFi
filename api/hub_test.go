package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/config"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/spec"
	"github.com/wcrum/gofi/taprelay"
)

// withHubIdleGrace shortens the hub's idle grace (GOFI_HUB_IDLE_GRACE) for one
// test through the process configuration the hub reads.
func withHubIdleGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	cfg := config.Get()
	cfg.Observability.HubIdleGrace = grace
	config.Set(cfg)
	t.Cleanup(config.Reset)
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Many browsers watching one remote flow cost the runtime one subscription,
// and the upstream closes once the idle grace passes with no viewer.
func TestHubSharesOneUpstreamAcrossViewersAndClosesWhenIdle(t *testing.T) {
	withHubIdleGrace(t, 200*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	remoteTap := pipeline.NewTap()
	relayServer := taprelay.New(remoteTap)
	relay := httptest.NewServer(relayServer.Handler())
	defer relay.Close()
	d := &streamDriver{targets: map[string]driver.TapTarget{"remote": {Addr: strings.TrimPrefix(relay.URL, "http://")}}}
	ctrl := controller.New(d)
	if err := ctrl.Add(spec.FlowDefinition{Name: "remote", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000}}}); err != nil {
		t.Fatal(err)
	}
	api := newTestAPI(t, ctrl)
	defer api.server.Close()
	publishTraffic(t, ctx, map[string]*pipeline.Tap{"remote": remoteTap})

	const viewers = 6
	var received atomic.Int32
	cancels := make([]context.CancelFunc, 0, viewers)
	for i := 0; i < viewers; i++ {
		viewerCtx, stop := context.WithCancel(ctx)
		cancels = append(cancels, stop)
		stream, err := api.client.WatchTraffic(viewerCtx, connect.NewRequest(&gofiv1.WatchTrafficRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			defer stream.Close()
			for stream.Receive() {
				if stream.Msg().Event != nil {
					received.Add(1)
				}
			}
		}()
	}
	eventually(t, 5*time.Second, func() bool { return received.Load() >= viewers }, "every viewer to receive traffic")
	if got := relayServer.Subscribers(); got != 1 {
		t.Fatalf("runtime subscriptions = %d, want 1 for %d viewers", got, viewers)
	}
	if got := api.handler.traffic.set.Upstreams(); got != 1 {
		t.Fatalf("hub upstreams = %d, want 1", got)
	}
	if got := api.handler.traffic.viewerCount(); got != viewers {
		t.Fatalf("viewers = %d, want %d", got, viewers)
	}
	for _, stop := range cancels {
		stop()
	}
	eventually(t, 3*time.Second, func() bool { return api.handler.traffic.viewerCount() == 0 }, "viewers to detach")
	eventually(t, 3*time.Second, func() bool { return api.handler.traffic.set.Upstreams() == 0 && relayServer.Subscribers() == 0 }, "idle upstream to close")
	if api.handler.caps.open() != 0 {
		t.Fatalf("stream cap still holds %d", api.handler.caps.open())
	}
}

// A viewer that never reads loses its own events and only those; the
// dispatcher never blocks and other viewers keep receiving.
func TestHubSlowViewerDropsWithoutStallingOthers(t *testing.T) {
	ctrl := controller.New(&streamDriver{targets: map[string]driver.TapTarget{}})
	cfg := loadHubConfig()
	cfg.queue = 4
	hub := newTrafficHub(ctrl, cfg, newUpstreamClients())
	slow, releaseSlow := hub.subscribe("")
	defer releaseSlow()
	fast, releaseFast := hub.subscribe("flow")
	defer releaseFast()
	other, releaseOther := hub.subscribe("other-flow")
	defer releaseOther()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			hub.dispatch("flow", &gofiv1.TrafficEvent{Ts: fmt.Sprint(i)})
			if i%2 == 1 { // the fast viewer keeps up with every other event
				<-fast.ch
				<-fast.ch
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch blocked on a slow viewer")
	}
	if got := slow.dropped.Load(); got != 96 {
		t.Fatalf("slow viewer dropped %d, want 96 (100 events, queue 4)", got)
	}
	if len(slow.ch) != 4 {
		t.Fatalf("slow viewer queued %d, want 4", len(slow.ch))
	}
	if fast.dropped.Load() != 0 || len(fast.ch) != 0 {
		t.Fatalf("fast viewer dropped %d queued %d, want 0/0", fast.dropped.Load(), len(fast.ch))
	}
	if len(other.ch) != 0 {
		t.Fatalf("viewer of another flow received %d events", len(other.ch))
	}
}

// Stats come from the runtimes' counters: an in-process flow and a remote
// relay both report, and a named request reports only that flow.
func TestWatchFlowStatsAggregatesLocalAndRemote(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	remoteTap, localTap := pipeline.NewTap(), pipeline.NewTap()
	relay := httptest.NewServer(taprelay.New(remoteTap).Handler())
	defer relay.Close()
	d := &streamDriver{targets: map[string]driver.TapTarget{"stats-remote": {Addr: strings.TrimPrefix(relay.URL, "http://")}, "stats-local": {Local: localTap}}}
	ctrl := controller.New(d)
	for _, name := range []string{"stats-remote", "stats-local"} {
		if err := ctrl.Add(spec.FlowDefinition{Name: name, Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000}}}); err != nil {
			t.Fatal(err)
		}
	}
	api := newTestAPI(t, ctrl)
	defer api.server.Close()
	publishTraffic(t, ctx, map[string]*pipeline.Tap{"stats-remote": remoteTap, "stats-local": localTap})
	stream, err := api.client.WatchFlowStats(ctx, connect.NewRequest(&gofiv1.WatchFlowStatsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for stream.Receive() {
		msg := stream.Msg()
		if len(msg.Flows) != 2 {
			t.Fatalf("stats for %d flows, want 2", len(msg.Flows))
		}
		ready := 0
		for _, flow := range msg.Flows {
			if flow.Up && flow.Observed > 0 {
				ready++
			}
		}
		if ready == 2 {
			break
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	named, err := api.client.WatchFlowStats(ctx, connect.NewRequest(&gofiv1.WatchFlowStatsRequest{Name: "stats-local"}))
	if err != nil {
		t.Fatal(err)
	}
	defer named.Close()
	if !named.Receive() || len(named.Msg().Flows) != 1 || named.Msg().Flows[0].Flow != "stats-local" {
		t.Fatalf("named stats = %+v (%v)", named.Msg(), named.Err())
	}
	if got := api.handler.stats.set.Upstreams(); got != 2 {
		t.Fatalf("stats upstreams = %d, want 2 (one per flow, shared by both viewers)", got)
	}
}

// ListFlows polls cost one round trip while nothing changed.
func TestListFlowsRevisionShortCircuits(t *testing.T) {
	ctrl := controller.New(&streamDriver{targets: map[string]driver.TapTarget{}})
	if err := ctrl.Add(spec.FlowDefinition{Name: "one", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000}}}); err != nil {
		t.Fatal(err)
	}
	api := newTestAPI(t, ctrl)
	defer api.server.Close()
	ctx := context.Background()
	first, err := api.client.ListFlows(ctx, connect.NewRequest(&gofiv1.ListFlowsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Msg.Flows) != 1 || first.Msg.Revision == 0 || first.Msg.Unchanged {
		t.Fatalf("first list = %+v", first.Msg)
	}
	again, err := api.client.ListFlows(ctx, connect.NewRequest(&gofiv1.ListFlowsRequest{IfRevision: first.Msg.Revision}))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Msg.Unchanged || len(again.Msg.Flows) != 0 || again.Msg.Revision != first.Msg.Revision {
		t.Fatalf("unchanged poll = %+v", again.Msg)
	}
	if err := ctrl.Add(spec.FlowDefinition{Name: "two", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9001}}}); err != nil {
		t.Fatal(err)
	}
	changed, err := api.client.ListFlows(ctx, connect.NewRequest(&gofiv1.ListFlowsRequest{IfRevision: first.Msg.Revision}))
	if err != nil {
		t.Fatal(err)
	}
	if changed.Msg.Unchanged || len(changed.Msg.Flows) != 2 || changed.Msg.Revision <= first.Msg.Revision {
		t.Fatalf("changed poll = %+v", changed.Msg)
	}
	if err := ctrl.Remove("one"); err != nil {
		t.Fatal(err)
	}
	removed, err := api.client.ListFlows(ctx, connect.NewRequest(&gofiv1.ListFlowsRequest{IfRevision: changed.Msg.Revision}))
	if err != nil {
		t.Fatal(err)
	}
	if removed.Msg.Unchanged || len(removed.Msg.Flows) != 1 {
		t.Fatalf("poll after delete = %+v", removed.Msg)
	}
}

// Stream caps refuse the stream past the limit and free the slot on release.
func TestStreamCaps(t *testing.T) {
	caps := newStreamCaps(hubConfig{maxStreams: 3, maxPerClient: 2})
	releaseA1, err := caps.acquire("traffic", "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = caps.acquire("traffic", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err = caps.acquire("traffic", "a"); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("third stream from one client: %v", err)
	}
	if _, err = caps.acquire("stats", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err = caps.acquire("stats", "c"); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("fourth stream overall: %v", err)
	}
	releaseA1()
	releaseA1() // idempotent
	if _, err = caps.acquire("stats", "c"); err != nil {
		t.Fatalf("after release: %v", err)
	}
	if caps.open() != 3 {
		t.Fatalf("open = %d, want 3", caps.open())
	}
}

type testAPI struct {
	server  *httptest.Server
	client  gofiv1connect.GoFiServiceClient
	handler *Handler
}

// newTestAPI serves the handler and keeps a reference to it for assertions.
func newTestAPI(t *testing.T, ctrl *controller.Controller) *testAPI {
	t.Helper()
	h := &Handler{ctrl: ctrl, mode: deploymode.ModeBinary}
	cfg := loadHubConfig()
	clients := newUpstreamClients()
	h.caps = newStreamCaps(cfg)
	h.traffic = newTrafficHub(ctrl, cfg, clients)
	h.stats = newStatsHub(ctrl, cfg, clients)
	path, service := gofiv1connect.NewGoFiServiceHandler(h, handlerOptions()...)
	mux := httptest.NewServer(withStreamDeadlines(service))
	_ = path
	return &testAPI{server: mux, client: gofiv1connect.NewGoFiServiceClient(mux.Client(), mux.URL), handler: h}
}
