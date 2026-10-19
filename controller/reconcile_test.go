package controller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/spec"
)

// replacingDriver is an in-memory driver that can update in place (like
// Kubernetes). It is safe for use from the controller's goroutine and the test.
type replacingDriver struct {
	mu          sync.Mutex
	definitions map[string]spec.FlowDefinition
	creates     int
	deletes     int
	replaces    int
	createErr   error
	replaceErr  error
}

func (d *replacingDriver) Create(_ context.Context, def spec.FlowDefinition) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.createErr != nil {
		return d.createErr
	}
	d.definitions[def.Name] = def
	d.creates++
	return nil
}
func (d *replacingDriver) Delete(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deletes++
	delete(d.definitions, name)
	return nil
}
func (d *replacingDriver) List(context.Context) ([]spec.FlowDefinition, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []spec.FlowDefinition{}
	for _, def := range d.definitions {
		out = append(out, def)
	}
	return out, nil
}
func (d *replacingDriver) Tap(context.Context, string) (driver.TapTarget, bool, error) {
	return driver.TapTarget{}, false, nil
}
func (d *replacingDriver) RuntimeStatus(context.Context, string) (driver.RuntimeInfo, bool, error) {
	return driver.RuntimeInfo{}, false, nil
}
func (d *replacingDriver) Kind() string { return "test" }

func (d *replacingDriver) Replace(_ context.Context, def spec.FlowDefinition) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.replaces++
	if d.replaceErr != nil {
		return d.replaceErr
	}
	d.definitions[def.Name] = def
	return nil
}

func (d *replacingDriver) Lookup(_ context.Context, name string) (spec.FlowDefinition, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	def, ok := d.definitions[name]
	return def, ok, nil
}

// counts reads the counters under the lock.
func (d *replacingDriver) counts() (creates, deletes, replaces int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.creates, d.deletes, d.replaces
}

func (d *replacingDriver) has(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.definitions[name]
	return ok
}

func (d *replacingDriver) remove(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.definitions, name)
}

func (d *replacingDriver) port(name string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.definitions[name].Source.Listen.Port
}

func newReplacingDriver(defs ...spec.FlowDefinition) *replacingDriver {
	d := &replacingDriver{definitions: map[string]spec.FlowDefinition{}}
	for _, def := range defs {
		d.definitions[def.Name] = def
	}
	return d
}

// With a Replacer, drift is fixed by Replace, never by Delete then Create:
// the runtime is not interrupted.
func TestControllerReplacesDriftedFlowWithoutDeleting(t *testing.T) {
	def := controllerDef()
	drifted := cloneDefinition(def)
	drifted.Source.Listen.Port = 1234
	d := newReplacingDriver(drifted)
	c := New(d)
	c.desired[def.Name] = def
	c.sync(context.Background())
	if d.deletes != 0 || d.creates != 0 || d.replaces != 1 || d.port(def.Name) != 9000 {
		t.Fatalf("deletes=%d creates=%d replaces=%d", d.deletes, d.creates, d.replaces)
	}
	// An API update of an existing flow does not delete first either.
	updated := cloneDefinition(def)
	updated.Source.Listen.Port = 9100
	if err := c.Add(updated); err != nil {
		t.Fatal(err)
	}
	if d.deletes != 0 {
		t.Fatal("Add deleted the runtime before the reconcile could replace it")
	}
	c.sync(context.Background())
	if d.replaces != 2 || d.port(def.Name) != 9100 {
		t.Fatalf("update not applied by Replace: replaces=%d", d.replaces)
	}
}

// ErrPending from the driver is not a start failure: the flow shows as
// starting and is retried.
func TestControllerTreatsPendingAsRetryNotFailure(t *testing.T) {
	def := controllerDef()
	d := newReplacingDriver()
	d.createErr = errors.Join(driver.ErrPending)
	c := New(d)
	c.desired[def.Name] = def
	c.sync(context.Background())
	if info, found, _ := c.RuntimeStatus(context.Background(), def.Name); found || info.Error != "" {
		t.Fatalf("pending reported as a failure: %+v", info)
	}
	if c.queue == nil || c.queue.NumRequeues(def.Name) == 0 {
		t.Fatal("pending flow not requeued")
	}
}

// An event for one flow reconciles only that flow.
func TestEnqueueReconcilesOneFlow(t *testing.T) {
	a, b := controllerDef(), controllerDef()
	b.Name = "other"
	d := newReplacingDriver()
	c := New(d)
	c.desired[a.Name] = a
	c.desired[b.Name] = b
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	// Run's first full pass creates both; wait for it.
	waitFor(t, func() bool { creates, _, _ := d.counts(); return creates == 2 })
	// The runtime of "other" disappears (a crash): its event re-creates it alone.
	d.remove(b.Name)
	c.Enqueue(b.Name)
	waitFor(t, func() bool { creates, _, _ := d.counts(); return creates == 3 })
	if _, deletes, _ := d.counts(); !d.has(b.Name) || deletes != 0 {
		t.Fatalf("event-driven reconcile: deletes=%d", deletes)
	}
	// A flow that is no longer desired is removed when its event arrives.
	c.mu.Lock()
	delete(c.desired, b.Name)
	c.mu.Unlock()
	c.Enqueue(b.Name)
	waitFor(t, func() bool { _, deletes, _ := d.counts(); return deletes == 1 })
}

// A passive (standby) controller observes without acting.
func TestPassiveControllerDoesNotAct(t *testing.T) {
	def := controllerDef()
	d := newReplacingDriver()
	c := New(d)
	c.SetPassive(true)
	c.desired[def.Name] = def
	c.sync(context.Background())
	if d.creates != 0 {
		t.Fatal("standby created a runtime")
	}
	c.SetPassive(false)
	c.sync(context.Background())
	if d.creates != 1 {
		t.Fatal("leader did not create after takeover")
	}
}

// The flow-set reconcile runs when the set changes and on forced passes only.
func TestFlowSetReconcileSkipsUnchangedSet(t *testing.T) {
	def := controllerDef()
	d := &flowSetDriver{replacingDriver: newReplacingDriver()}
	c := New(d)
	c.desired[def.Name] = def
	snap := flowSnapshot{desired: c.desiredCopy(), fresh: true}
	c.reconcileAll(context.Background(), snap, false)
	c.reconcileAll(context.Background(), snap, false)
	if d.flowSets != 1 {
		t.Fatalf("unchanged set reconciled %d times", d.flowSets)
	}
	c.reconcileAll(context.Background(), snap, true)
	if d.flowSets != 2 {
		t.Fatal("forced pass skipped the flow set")
	}
	other := controllerDef()
	other.Name = "second"
	c.desired[other.Name] = other
	c.reconcileAll(context.Background(), flowSnapshot{desired: c.desiredCopy(), fresh: true}, false)
	if d.flowSets != 3 {
		t.Fatal("changed set not reconciled")
	}
}

type flowSetDriver struct {
	*replacingDriver
	flowSets int
}

func (d *flowSetDriver) ReconcileFlowSet(context.Context, []spec.FlowDefinition) error {
	d.flowSets++
	return nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
