package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/filter"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/spec"
	"github.com/wcrum/gofi/taprelay"
)

type streamDriver struct {
	mu      sync.RWMutex
	targets map[string]driver.TapTarget
}

func (d *streamDriver) Create(context.Context, spec.FlowDefinition) error   { return nil }
func (d *streamDriver) Delete(context.Context, string) error                { return nil }
func (d *streamDriver) List(context.Context) ([]spec.FlowDefinition, error) { return nil, nil }
func (d *streamDriver) Tap(_ context.Context, name string) (driver.TapTarget, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	target, found := d.targets[name]
	return target, found, nil
}
func (d *streamDriver) RuntimeStatus(context.Context, string) (driver.RuntimeInfo, bool, error) {
	return driver.RuntimeInfo{}, false, nil
}
func (d *streamDriver) Kind() string { return "inprocess" }

func publishTraffic(t *testing.T, ctx context.Context, taps map[string]*pipeline.Tap) {
	t.Helper()
	chain, err := filter.NewChain(nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				for name, tap := range taps {
					def := spec.FlowDefinition{Name: name, Parser: spec.ParserCfg{Format: "json"}}
					p, err := pipeline.New(def, strings.NewReader(`{"route":"`+name+`"}`), nil, nil, chain, tap)
					if err == nil {
						p.Run(ctx, "test")
					}
				}
			}
		}
	}()
}
func TestConnectWatchTrafficMultiplexesMoreThanSixListeners(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d := &streamDriver{targets: map[string]driver.TapTarget{}}
	ctrl := controller.New(d)
	taps := map[string]*pipeline.Tap{}
	for i := 0; i < 9; i++ {
		name := fmt.Sprintf("route-%d", i)
		taps[name] = pipeline.NewTap()
		d.targets[name] = driver.TapTarget{Local: taps[name]}
		if err := ctrl.Add(spec.FlowDefinition{Name: name, Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000 + i}}}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(New(ctrl, nil, deploymode.ModeBinary))
	defer server.Close()
	client := gofiv1connect.NewGoFiServiceClient(server.Client(), server.URL)
	stream, err := client.WatchTraffic(ctx, connect.NewRequest(&gofiv1.WatchTrafficRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatalf("initial heartbeat missing: %v", stream.Err())
	}
	publishTraffic(t, ctx, taps)
	seen := map[string]bool{}
	for len(seen) < len(taps) && stream.Receive() {
		event := stream.Msg()
		if event.Event == nil {
			continue
		}
		if event.Event.Fields.AsMap()["route"] != event.Flow {
			t.Fatalf("traffic mislabeled: %+v", event)
		}
		seen[event.Flow] = true
	}
	if len(seen) != len(taps) {
		t.Fatalf("received %d/%d listeners: %v", len(seen), len(taps), stream.Err())
	}
	cancel()
	if stream.Receive() {
		t.Fatal("stream continued after cancellation")
	}
}
func TestConnectWatchTrafficUsesConnectListenerRelayAndNamedFilter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	remoteTap := pipeline.NewTap()
	relay := httptest.NewServer(taprelay.New(remoteTap).Handler())
	defer relay.Close()
	d := &streamDriver{targets: map[string]driver.TapTarget{"remote": {Addr: strings.TrimPrefix(relay.URL, "http://")}, "other": {Local: pipeline.NewTap()}}}
	ctrl := controller.New(d)
	for _, name := range []string{"remote", "other"} {
		if err := ctrl.Add(spec.FlowDefinition{Name: name, Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000}}}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(New(ctrl, nil, deploymode.ModeBinary))
	defer server.Close()
	client := gofiv1connect.NewGoFiServiceClient(server.Client(), server.URL)
	stream, err := client.WatchTraffic(ctx, connect.NewRequest(&gofiv1.WatchTrafficRequest{Name: "remote"}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	publishTraffic(t, ctx, map[string]*pipeline.Tap{"remote": remoteTap, "other": d.targets["other"].Local})
	for stream.Receive() {
		event := stream.Msg()
		if event.Event == nil {
			continue
		}
		if event.Flow != "remote" || event.Event.Fields.AsMap()["route"] != "remote" {
			t.Fatalf("named filter crossed listeners: %+v", event)
		}
		return
	}
	t.Fatalf("no remote Connect traffic: %v", stream.Err())
}
