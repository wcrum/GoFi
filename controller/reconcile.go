package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sort"
	"time"

	"k8s.io/client-go/util/workqueue"

	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/expressions"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// Reconciliation is structured so that a controller with many flows does
// little work and holds no lock across the API server:
//
//   - snapshot refreshes desired state (store, CEL library) under syncMu,
//     the lock API edits take, and returns a copy. syncMu is released before
//     any driver call, so saves never wait on Kubernetes.
//   - reconcileAll makes the runtime match a snapshot under reconcileMu,
//     which only serializes driver work (full passes and queue items).
//     Reads come from the driver, which on Kubernetes serves them from the
//     informer cache and writes only what differs: an unchanged flow set
//     costs no API request.
//   - The work queue holds flows named by events (Flow, Pod, and Service
//     informers; Enqueue). The worker reconciles one flow per item with
//     exponential backoff and jitter on failure, so a crash-looping flow
//     cannot starve the others or hammer the API server.
//   - The flow-set reconcile (UDS Package, orphan collection) runs when the
//     set of definitions changed and on the periodic full pass.

// flowQueue is the per-flow work queue.
type flowQueue = workqueue.TypedRateLimitingInterface[string]

// newFlowQueue builds the queue: exponential backoff from 200ms to 5 minutes
// per flow, with up to 20% jitter so retries of many flows spread out.
func newFlowQueue() flowQueue {
	base := workqueue.NewTypedItemExponentialFailureRateLimiter[string](200*time.Millisecond, 5*time.Minute)
	return workqueue.NewTypedRateLimitingQueueWithConfig(jitterLimiter{base}, workqueue.TypedRateLimitingQueueConfig[string]{Name: "gofi-flows"})
}

type jitterLimiter struct {
	base workqueue.TypedRateLimiter[string]
}

func (j jitterLimiter) When(item string) time.Duration {
	d := j.base.When(item)
	return d + time.Duration(rand.Int64N(int64(d)/5+1))
}
func (j jitterLimiter) Forget(item string)          { j.base.Forget(item) }
func (j jitterLimiter) NumRequeues(item string) int { return j.base.NumRequeues(item) }

// flowSnapshot is what one reconcile pass works from.
type flowSnapshot struct {
	desired map[string]spec.FlowDefinition
	// fresh is false when the store could not be read: live state is then
	// preserved (nothing is deleted or replaced) until storage recovers.
	fresh bool
	lib   expressions.Library
}

// SetResyncInterval changes the full-pass period (default resyncInterval).
// With informers delivering events, 5 minutes is plenty.
func (c *Controller) SetResyncInterval(d time.Duration) {
	if d > 0 {
		c.resync = d
	}
}

// SetPassive makes this controller observe without acting: it refreshes
// desired state and publishes flow states (so the read API stays current)
// but creates, replaces, and deletes nothing. A replica that is not the
// leader runs passive.
func (c *Controller) SetPassive(passive bool) { c.passive.Store(passive) }

// Passive reports whether this controller only observes.
func (c *Controller) Passive() bool { return c.passive.Load() }

// Enqueue asks for one flow to be reconciled soon: informer handlers call it
// with the flow a Pod, Service, or Flow event belongs to. It is safe before
// Run and from any goroutine.
func (c *Controller) Enqueue(name string) {
	if name == "" {
		return
	}
	c.ensureQueue().Add(name)
}

func (c *Controller) ensureQueue() flowQueue {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.queue == nil {
		c.queue = newFlowQueue()
	}
	return c.queue
}

func (c *Controller) resyncInterval() time.Duration {
	if c.resync > 0 {
		return c.resync
	}
	return resyncInterval
}

// run is the reconcile loop behind Run.
func (c *Controller) run(ctx context.Context) {
	queue := c.ensureQueue()
	c.trigger()
	ticker := time.NewTicker(c.resyncInterval())
	defer ticker.Stop()
	items := make(chan string)
	go func() {
		for {
			name, shutdown := queue.Get()
			if shutdown {
				close(items)
				return
			}
			select {
			case items <- name:
			case <-ctx.Done():
				queue.Done(name)
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			queue.ShutDown()
			return
		case <-c.reconcileCh:
			c.reconcileAll(ctx, c.snapshot(ctx), false)
		case <-ticker.C:
			c.reconcileAll(ctx, c.snapshot(ctx), true)
		case name, ok := <-items:
			if !ok {
				return
			}
			c.reconcileOne(ctx, name)
		}
	}
}

// snapshot refreshes desired state from the store and the CEL library and
// returns a copy. It holds syncMu only for that: no driver call runs under
// the lock API edits take.
func (c *Controller) snapshot(ctx context.Context) flowSnapshot {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	snap := flowSnapshot{fresh: true}
	snap.lib = c.refreshLibrary(ctx)
	if c.store != nil {
		defs, err := c.store.Load(ctx)
		if err != nil {
			snap.fresh = false
			slog.Warn("controller: desired state load failed", "error", err)
		} else if err = c.replaceDesired(ctx, defs); err != nil && err.Error() != c.lastSkipped {
			// Invalid flows were skipped; the rest of the snapshot applied.
			// Log once per change, not on every resync.
			c.lastSkipped = err.Error()
			slog.Warn("controller: skipped invalid flows", "error", err)
		} else if err == nil {
			c.lastSkipped = ""
		}
	}
	snap.desired = c.desiredCopy()
	return snap
}

// desiredCopy is the in-memory desired state (no store read).
func (c *Controller) desiredCopy() map[string]spec.FlowDefinition {
	c.mu.RLock()
	defer c.mu.RUnlock()
	desired := make(map[string]spec.FlowDefinition, len(c.desired))
	for k, v := range c.desired {
		desired[k] = cloneDefinition(v)
	}
	return desired
}

// reconcileAll makes every runtime match the snapshot. force also runs the
// flow-set reconcile when the flow set is unchanged (the periodic pass).
func (c *Controller) reconcileAll(ctx context.Context, snap flowSnapshot, force bool) {
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	actual, err := c.drv.List(ctx)
	c.noteDriverList(err)
	if err != nil {
		slog.Warn("controller: list error", "error", err)
		return
	}
	// The pass counts as completed once it ends (or, for a passive
	// replica, once it has observed): Ready then reports true.
	defer c.ready.firstPass.Store(true)
	actualSet := make(map[string]spec.FlowDefinition, len(actual))
	for _, d := range actual {
		actualSet[d.Name] = d
	}
	publishFlowStates(snap.desired, actualSet) // as found; the next reconcile shows repairs
	if c.passive.Load() {
		return
	}
	names := make([]string, 0, len(snap.desired))
	for name := range snap.desired {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		current, running := actualSet[name]
		if err := c.reconcileFlow(ctx, name, snap.desired[name], current, running, snap); err != nil && !errors.Is(err, errKeepRuntime) {
			// Retry sooner than the next full pass, with backoff.
			c.ensureQueue().AddRateLimited(name)
		}
	}
	for _, d := range actual {
		if !snap.fresh {
			break
		}
		if _, want := snap.desired[d.Name]; !want {
			if err := c.drv.Delete(ctx, d.Name); err != nil {
				slog.Error("controller: delete failed", "name", d.Name, "error", err)
			} else {
				slog.Info("controller: flow stopped", "name", d.Name)
			}
		}
	}
	c.reconcileFlowSet(ctx, snap, force)
	if snap.fresh {
		observability.ReconcileSucceeded()
	}
}

// errKeepRuntime marks a flow whose runtime was deliberately left as is
// (unresolved library reference, stale snapshot): no retry is scheduled.
var errKeepRuntime = errors.New("runtime kept")

// reconcileFlow brings one flow's runtime in line with its definition.
// current/running describe what the driver reports for it.
func (c *Controller) reconcileFlow(ctx context.Context, name string, def, current spec.FlowDefinition, running bool, snap flowSnapshot) error {
	if def.Stopped() {
		if snap.fresh {
			c.stopRuntime(ctx, name, running)
		}
		return nil
	}
	def = runtimeDefinition(def)
	// Runtimes receive library-resolved definitions, so a library edit
	// shows up as drift and recreates the listeners that follow it.
	resolved, err := expressions.Resolve(def, snap.lib)
	if err != nil {
		slog.Error("controller: CEL library reference unresolved; keeping current runtime", "name", name, "error", err)
		return errKeepRuntime
	}
	def = resolved
	bindingsDrifted := false
	if checker, ok := c.drv.(driver.PythonBindingsChecker); ok && running && def.UsesPython() {
		matches, err := checker.PythonBindingsCurrent(ctx, def)
		if err != nil {
			// A starting/unavailable worker is not evidence that the current
			// listener is stale. Preserve it until fresh bindings are known.
			slog.Warn("controller: Python bindings unavailable", "name", name, "error", err)
		} else {
			bindingsDrifted = !matches
		}
	}
	if running && (!equivalentDefinitions(current, def) || bindingsDrifted) {
		if !snap.fresh {
			return errKeepRuntime // Keep uncertain live state until storage recovers.
		}
		if replacer, ok := c.drv.(driver.Replacer); ok {
			// Zero-downtime update: the new runtime starts beside the old one,
			// which keeps serving until the driver sees the new one running.
			err := replacer.Replace(ctx, def)
			return c.recordRuntimeStart(name, def, err, "flow updated")
		}
		if err := c.drv.Delete(ctx, name); err != nil {
			slog.Error("controller: delete drifted flow failed", "name", name, "error", err)
			return err
		}
		running = false
	}
	if !running {
		c.noteRuntimeExit(ctx, name)
		err := c.drv.Create(ctx, def)
		return c.recordRuntimeStart(name, def, err, "flow started")
	}
	return nil
}

// recordRuntimeStart records the outcome of a Create or Replace. ErrPending
// (the previous runtime is still going away) is not a start failure: the
// flow is retried, and the portal shows it starting.
func (c *Controller) recordRuntimeStart(name string, def spec.FlowDefinition, err error, event string) error {
	if errors.Is(err, driver.ErrPending) {
		c.recordStart(name, nil)
		slog.Info("controller: runtime start deferred", "name", name, "reason", err)
		return err
	}
	c.recordStart(name, err)
	if err != nil {
		slog.Error("controller: create failed", "name", name, "error", err)
		return err
	}
	slog.Info("controller: "+event, "name", name, "port", def.Source.Listening().Port)
	return nil
}

// reconcileOne handles one queue item: the flow named by an event.
func (c *Controller) reconcileOne(ctx context.Context, name string) {
	queue := c.ensureQueue()
	defer queue.Done(name)
	if c.passive.Load() {
		queue.Forget(name)
		return
	}
	snap := flowSnapshot{desired: c.desiredCopy(), fresh: true, lib: c.library()}
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	def, want := snap.desired[name]
	var (
		current spec.FlowDefinition
		running bool
		err     error
	)
	if lookup, ok := c.drv.(driver.FlowLookup); ok {
		current, running, err = lookup.Lookup(ctx, name)
	} else {
		var actual []spec.FlowDefinition
		if actual, err = c.drv.List(ctx); err == nil {
			for _, d := range actual {
				if d.Name == name {
					current, running = d, true
				}
			}
		}
	}
	switch {
	case err != nil:
		slog.Warn("controller: lookup failed", "name", name, "error", err)
	case !want:
		if running {
			if err = c.drv.Delete(ctx, name); err != nil {
				slog.Error("controller: delete failed", "name", name, "error", err)
			} else {
				slog.Info("controller: flow stopped", "name", name)
			}
		}
	default:
		err = c.reconcileFlow(ctx, name, def, current, running, snap)
	}
	if err != nil && !errors.Is(err, errKeepRuntime) {
		queue.AddRateLimited(name)
		return
	}
	queue.Forget(name)
}

// reconcileFlowSet rebuilds the objects derived from the whole flow set (UDS
// network allows, orphaned exposure objects) from a complete, fresh
// snapshot: when the set changed since the last successful pass, or on the
// periodic pass (force).
func (c *Controller) reconcileFlowSet(ctx context.Context, snap flowSnapshot, force bool) {
	reconciler, ok := c.drv.(driver.FlowSetReconciler)
	if !ok || !snap.fresh {
		return
	}
	names := make([]string, 0, len(snap.desired))
	for name := range snap.desired {
		names = append(names, name)
	}
	sort.Strings(names)
	defs := make([]spec.FlowDefinition, 0, len(names))
	sum := sha256.New()
	for _, name := range names {
		defs = append(defs, snap.desired[name])
		raw, _ := json.Marshal(snap.desired[name])
		sum.Write(raw)
		sum.Write([]byte{0})
	}
	hash := hex.EncodeToString(sum.Sum(nil))
	if !force && hash == c.lastFlowSetHash && c.lastFlowSetErr == "" {
		return
	}
	err := reconciler.ReconcileFlowSet(ctx, defs)
	message := ""
	if err != nil {
		message = err.Error()
	}
	c.lastFlowSetHash = hash
	if message != c.lastFlowSetErr {
		c.lastFlowSetErr = message
		if err != nil {
			slog.Error("controller: flow-set reconcile failed", "error", err)
		}
	}
}
