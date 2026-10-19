// Package fakedriver is an in-memory runtime driver for controller
// convergence tests. It behaves like the Docker and Kubernetes drivers from
// the controller's point of view (a runtime is one named instance, Delete
// blocks until gone, List reports what runs) while counting every verb,
// optionally adding latency, injecting failures, and letting a test "crash"
// a runtime or leave an orphan behind, exactly as a dead container or a Pod
// the controller never created would.
package fakedriver

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/spec"
)

// Verbs counts driver calls by kind.
type Verbs struct {
	Creates, Deletes, Lists, Status, FlowSets int64
}

// Driver is the fake. Zero value is usable; New returns one ready to use.
type Driver struct {
	mu       sync.Mutex
	running  map[string]*runtime
	verbs    Verbs
	creates  atomic.Int64
	deletes  atomic.Int64
	lists    atomic.Int64
	status   atomic.Int64
	flowSets atomic.Int64
	// Latency is added to Create and Delete (simulating container start/stop).
	Latency time.Duration
	// FailCreate, when set, makes Create of a matching flow fail with that error.
	FailCreate func(def spec.FlowDefinition) error
	// FailDelete, when set, makes Delete of a matching flow fail.
	FailDelete func(name string) error
	// FailList, when non-nil, is returned by List (store/apiserver outage).
	FailList error
	// Mirror receives the flow set the controller last reconciled (UDS allows).
	lastFlowSet []spec.FlowDefinition
	kind        string
	history     []string
}

type runtime struct {
	def       spec.FlowDefinition
	startedAt time.Time
	status    string
	ip        string
}

// New returns a driver that reports kind (default "fake").
func New(kind string) *Driver {
	if kind == "" {
		kind = "fake"
	}
	return &Driver{running: map[string]*runtime{}, kind: kind}
}

func (d *Driver) sleep(ctx context.Context) error {
	if d.Latency <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d.Latency):
		return nil
	}
}

// Create starts a runtime. Like the Docker driver it refuses a duplicate.
func (d *Driver) Create(ctx context.Context, def spec.FlowDefinition) error {
	d.creates.Add(1)
	if d.FailCreate != nil {
		if err := d.FailCreate(def); err != nil {
			return err
		}
	}
	if err := d.sleep(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.running[def.Name]; exists {
		return fmt.Errorf("fake driver: flow %q already running", def.Name)
	}
	d.running[def.Name] = &runtime{def: def, startedAt: time.Now(), status: "running", ip: fmt.Sprintf("10.244.0.%d", len(d.running)+2)}
	d.history = append(d.history, "create "+def.Name)
	return nil
}

// Delete stops a runtime; a missing runtime is not an error (Kubernetes
// semantics; the controller tolerates both).
func (d *Driver) Delete(ctx context.Context, name string) error {
	d.deletes.Add(1)
	if d.FailDelete != nil {
		if err := d.FailDelete(name); err != nil {
			return err
		}
	}
	if err := d.sleep(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.running, name)
	d.history = append(d.history, "delete "+name)
	return nil
}

// List reports running runtimes, sorted by name.
func (d *Driver) List(context.Context) ([]spec.FlowDefinition, error) {
	d.lists.Add(1)
	if d.FailList != nil {
		return nil, d.FailList
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]spec.FlowDefinition, 0, len(d.running))
	for _, r := range d.running {
		if r.status == "running" || r.status == "pending" {
			out = append(out, r.def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (d *Driver) Tap(context.Context, string) (driver.TapTarget, bool, error) {
	return driver.TapTarget{}, false, nil
}

func (d *Driver) RuntimeStatus(_ context.Context, name string) (driver.RuntimeInfo, bool, error) {
	d.status.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.running[name]
	if !ok {
		return driver.RuntimeInfo{}, false, nil
	}
	return driver.RuntimeInfo{Kind: d.kind, ID: "fake-" + name, Status: r.status, StartedAt: r.startedAt, IP: r.ip}, true, nil
}

func (d *Driver) Kind() string { return d.kind }

// ReconcileFlowSet records the flow set (driver.FlowSetReconciler).
func (d *Driver) ReconcileFlowSet(_ context.Context, defs []spec.FlowDefinition) error {
	d.flowSets.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastFlowSet = append([]spec.FlowDefinition(nil), defs...)
	return nil
}

// LastFlowSet is the flow set of the most recent ReconcileFlowSet call.
func (d *Driver) LastFlowSet() []spec.FlowDefinition {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]spec.FlowDefinition(nil), d.lastFlowSet...)
}

// Crash makes a runtime disappear as if its container died (List no longer
// reports it); the controller's next sync must recreate it.
func (d *Driver) Crash(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.running, name)
	d.history = append(d.history, "crash "+name)
}

// Orphan plants a runtime the controller never asked for, as a leftover
// container from an earlier deployment would be.
func (d *Driver) Orphan(def spec.FlowDefinition) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running[def.Name] = &runtime{def: def, startedAt: time.Now(), status: "running"}
}

// Running returns the definitions currently running, keyed by name.
func (d *Driver) Running() map[string]spec.FlowDefinition {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]spec.FlowDefinition, len(d.running))
	for name, r := range d.running {
		out[name] = r.def
	}
	return out
}

// Verbs returns the call counts so far.
func (d *Driver) Verbs() Verbs {
	return Verbs{Creates: d.creates.Load(), Deletes: d.deletes.Load(), Lists: d.lists.Load(), Status: d.status.Load(), FlowSets: d.flowSets.Load()}
}

// Writes is the number of mutating calls (creates + deletes).
func (v Verbs) Writes() int64 { return v.Creates + v.Deletes }

// History is the ordered log of mutations ("create x", "delete x", "crash x").
func (d *Driver) History() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.history...)
}

// ResetVerbs zeroes the counters (between phases of a test).
func (d *Driver) ResetVerbs() {
	d.creates.Store(0)
	d.deletes.Store(0)
	d.lists.Store(0)
	d.status.Store(0)
	d.flowSets.Store(0)
}

var _ driver.Driver = (*Driver)(nil)
var _ driver.FlowSetReconciler = (*Driver)(nil)
