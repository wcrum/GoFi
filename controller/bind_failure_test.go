package controller

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// TestInProcessBindFailureReportsAndKeepsOtherFlows: with the in-process
// driver (one process, many flows) a flow whose port is already taken does
// not stop the controller or the other flows. It is reported on the flow
// (runtime status and a bulletin) and retried; everything else runs.
func TestInProcessBindFailureReportsAndKeepsOtherFlows(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	taken := occupied.Addr().(*net.TCPAddr).Port
	free := freePort(t)

	d := driver.NewInProcess()
	c := New(d)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { _ = d.DrainAll(context.Background()) }()
	blocked := spec.FlowDefinition{Name: "blocked", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: taken, Network: "tcp"}}, Framing: spec.Framing{Mode: "line"}, Parser: spec.ParserCfg{Format: "json"}, Sinks: []spec.SinkCfg{{Type: "stdout"}}}
	fine := blocked
	fine.Name, fine.Source = "fine", spec.FlowSource{Listen: &spec.ListenSource{Port: free, Network: "tcp"}}
	for _, def := range []spec.FlowDefinition{blocked, fine} {
		if err := c.Add(def); err != nil {
			t.Fatalf("add %s: %v", def.Name, err)
		}
	}
	c.sync(ctx)

	running, _ := d.List(ctx)
	if len(running) != 1 || running[0].Name != "fine" {
		t.Fatalf("expected only the bindable flow to run, got %+v", running)
	}
	info, found, err := c.RuntimeStatus(ctx, "blocked")
	if err != nil || !found || info.Status != "failed" || !strings.Contains(info.Error, "bind") {
		t.Fatalf("blocked flow status = %+v found=%v err=%v", info, found, err)
	}
	var reported bool
	for _, b := range observability.Bulletins("blocked") {
		reported = reported || strings.Contains(b.Message, "start failed")
	}
	if !reported {
		t.Fatalf("no bulletin for the bind failure: %+v", observability.Bulletins("blocked"))
	}
	if ok, _ := c.Ready(); !ok {
		t.Fatal("a flow that cannot bind must not make the controller not ready")
	}

	// Once the port frees up, the next pass starts the flow.
	occupied.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.sync(ctx)
		if running, _ = d.List(ctx); len(running) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("blocked flow did not start after its port was released: %+v", running)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, found, _ := c.RuntimeStatus(ctx, "blocked"); !found {
		t.Fatal("started flow has no runtime status")
	}
	if info, _, _ := c.RuntimeStatus(ctx, "blocked"); info.Error != "" {
		t.Fatalf("start error not cleared: %s", info.Error)
	}
}

// TestReadyFollowsStoreLoadFirstPassAndDriver pins Ready's three conditions.
func TestReadyFollowsStoreLoadFirstPassAndDriver(t *testing.T) {
	d := &fakeDriver{definitions: map[string]spec.FlowDefinition{}}
	c := New(d)
	if ok, reason := c.Ready(); ok || reason != "first reconcile pass not completed" {
		t.Fatalf("before any pass: %v %q", ok, reason)
	}
	ctx := context.Background()
	c.sync(ctx)
	if ok, reason := c.Ready(); !ok {
		t.Fatalf("after a pass: %q", reason)
	}
	// A driver that stops answering makes the controller not ready until
	// it answers again.
	failing := &listFailingDriver{fakeDriver: d}
	c.drv = failing
	c.sync(ctx)
	if ok, reason := c.Ready(); ok || !strings.Contains(reason, "runtime driver unreachable") {
		t.Fatalf("driver down: %v %q", ok, reason)
	}
	c.drv = d
	c.sync(ctx)
	if ok, _ := c.Ready(); !ok {
		t.Fatal("driver back: not ready")
	}
}

type listFailingDriver struct{ *fakeDriver }

func (d *listFailingDriver) List(context.Context) ([]spec.FlowDefinition, error) {
	return nil, fmt.Errorf("Cannot connect to the Docker daemon")
}
