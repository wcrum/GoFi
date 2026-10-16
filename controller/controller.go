// Package controller maintains desired flow state and reconciles it
// against the driver. Reconciliation is event-driven: the API triggers a
// reconcile via trigger() after each mutation.
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/expressions"
	"github.com/wcrum/gofi/spec"
)

// Controller holds desired state and drives the runtime driver to match it.
type Controller struct {
	mu      sync.RWMutex
	desired map[string]spec.FlowDefinition
	drv     driver.Driver
	store   desired.Store
	expr    *expressions.Manager
	// Serialize mutations and reconciliation so a stale reconcile cannot
	// overwrite an API edit or re-create a resource after its deletion.
	syncMu      sync.Mutex
	reconcileCh chan struct{}
	// startErrors keeps each flow's last runtime start failure (guarded by mu)
	// so the portal can show it; it clears on the next successful start.
	startErrors map[string]string
	// invalid holds stored flows that failed validation (guarded by mu). They
	// do not run, but stay listed so the portal can show, fix, or delete them.
	// If an earlier valid version exists, that version keeps running.
	invalid        map[string]invalidFlow
	lastSkipped    string // guarded by syncMu
	lastFlowSetErr string // guarded by syncMu
}

type invalidFlow struct {
	def spec.FlowDefinition
	err string
}

// WithStore loads durable desired state before serving requests.
func (c *Controller) WithStore(ctx context.Context, store desired.Store) error {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	defs, err := store.Load(ctx)
	if err != nil {
		return err
	}
	if err = c.replaceDesired(ctx, defs); err != nil {
		// One bad resource must not keep the controller (and every other flow)
		// from starting; it is skipped and reported on the flow itself.
		slog.Warn("controller: skipped invalid flows", "error", err)
	}
	c.store = store
	c.trigger()
	return nil
}

func (c *Controller) Configuration() desired.Configuration {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	if c.store != nil {
		return c.store.Configuration()
	}
	return desired.Configuration{Source: "memory", APIVersion: desired.APIVersion, Kind: desired.KindFlow}
}

// replaceDesired validates the complete snapshot before changing anything.
// Failed reads/invalid manifests retain the last known good configuration.
// replaceDesired installs a store snapshot flow by flow. A flow that fails
// validation is not applied: its last valid version keeps running if there is
// one, and it is recorded as invalid. The returned error lists those flows.
func (c *Controller) replaceDesired(ctx context.Context, defs []spec.FlowDefinition) error {
	c.mu.RLock()
	old := c.desired
	c.mu.RUnlock()
	next := make(map[string]spec.FlowDefinition, len(defs))
	invalid := map[string]invalidFlow{}
	var problems []error
	for _, def := range defs {
		stored := cloneDefinition(def)
		def = cloneDefinition(def)
		err := prepareInline(&def)
		if err == nil {
			err = c.Validate(def)
		}
		if err == nil {
			if _, exists := next[def.Name]; exists {
				err = errors.New("duplicate flow name")
			}
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("flow %q: %w", def.Name, err))
			invalid[def.Name] = invalidFlow{def: stored, err: err.Error()}
			if previous, ok := old[def.Name]; ok {
				next[def.Name] = previous
			}
			continue
		}
		next[def.Name] = cloneDefinition(def)
	}
	c.mu.Lock()
	c.desired = next
	c.invalid = invalid
	c.mu.Unlock()
	inlineChanged()
	for name, def := range old {
		if updated, ok := next[name]; ok && !equivalentDefinitions(def, updated) {
			if err := c.drv.Delete(ctx, name); err != nil {
				slog.Warn("controller: delete changed flow", "name", name, "error", err)
			}
		}
	}
	return errors.Join(problems...)
}

func New(d driver.Driver) *Controller {
	return &Controller{
		desired:     make(map[string]spec.FlowDefinition),
		drv:         d,
		reconcileCh: make(chan struct{}, 1),
	}
}

// Add registers a flow definition and triggers reconciliation.
// If a definition with the same name exists, it is replaced (update).
func (c *Controller) Add(def spec.FlowDefinition) error {
	def = cloneDefinition(def)
	if err := prepareInline(&def); err != nil {
		return err
	}
	if err := validateInline(def); err != nil {
		return err
	}
	defer inlineChanged()
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	if err := c.Validate(def); err != nil {
		return err
	}
	if admission, ok := c.drv.(driver.FlowAdmission); ok {
		others := make([]spec.FlowDefinition, 0, len(c.desired))
		c.mu.RLock()
		for name, other := range c.desired {
			if name != def.Name {
				others = append(others, cloneDefinition(other))
			}
		}
		c.mu.RUnlock()
		if err := admission.AdmitFlow(context.Background(), def, others); err != nil {
			return err
		}
	}
	def = cloneDefinition(def)
	if c.store != nil {
		if err := c.store.Save(context.Background(), def); err != nil {
			return err
		}
	}
	c.mu.Lock()
	previous, exists := c.desired[def.Name]
	c.desired[def.Name] = def
	delete(c.invalid, def.Name)
	c.mu.Unlock()

	if exists && !equivalentDefinitions(previous, def) {
		// For updates, delete the old instance first so the driver re-creates it.
		if err := c.drv.Delete(context.Background(), def.Name); err != nil {
			slog.Warn("controller: delete before update", "name", def.Name, "error", err)
		}
	}

	c.trigger()
	return nil
}

// Remove deregisters a listener definition and triggers reconciliation.
func (c *Controller) Remove(name string) error {
	defer inlineChanged()
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	if c.store != nil {
		if err := c.store.Delete(context.Background(), name); err != nil {
			return err
		}
	}
	c.mu.Lock()
	delete(c.desired, name)
	delete(c.startErrors, name)
	delete(c.invalid, name)
	c.mu.Unlock()
	c.trigger()
	return nil
}

// Get returns the stored definition for a named flow, or false. For a flow
// whose stored definition is invalid, that (not running) definition is returned.
func (c *Controller) Get(name string) (spec.FlowDefinition, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if bad, ok := c.invalid[name]; ok {
		return cloneDefinition(bad.def), true
	}
	d, ok := c.desired[name]
	return cloneDefinition(d), ok
}

// List returns every stored flow definition, including invalid ones (see Get).
func (c *Controller) List() []spec.FlowDefinition {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]spec.FlowDefinition, 0, len(c.desired)+len(c.invalid))
	for name, d := range c.desired {
		if _, bad := c.invalid[name]; !bad {
			out = append(out, cloneDefinition(d))
		}
	}
	for _, bad := range c.invalid {
		out = append(out, cloneDefinition(bad.def))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Tap returns how to reach a named listener's live traffic tap, forwarding
// to the underlying driver (see driver.TapTarget).
func (c *Controller) Tap(ctx context.Context, name string) (driver.TapTarget, bool, error) {
	return c.drv.Tap(ctx, name)
}

// RuntimeStatus reports the underlying compute (container/Pod) currently
// backing a named listener, forwarding to the underlying driver (see
// driver.RuntimeInfo).
func (c *Controller) RuntimeStatus(ctx context.Context, name string) (driver.RuntimeInfo, bool, error) {
	info, found, err := c.drv.RuntimeStatus(ctx, name)
	if err != nil {
		return info, found, err
	}
	c.mu.RLock()
	failure := c.startErrors[name]
	bad, isInvalid := c.invalid[name]
	c.mu.RUnlock()
	if isInvalid {
		if found {
			info.Error = "The stored definition is invalid and was not applied; the previous version keeps running: " + bad.err
		} else {
			info = driver.RuntimeInfo{Kind: c.drv.Kind(), Status: "invalid", Error: "The stored definition is invalid, so this flow is not running: " + bad.err}
		}
		return info, true, nil
	}
	if failure != "" {
		if !found {
			info = driver.RuntimeInfo{Kind: c.drv.Kind(), Status: "failed"}
		}
		info.Error = failure
		return info, true, nil
	}
	return info, found, nil
}

func (c *Controller) recordStart(name string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		delete(c.startErrors, name)
		return
	}
	if c.startErrors == nil {
		c.startErrors = map[string]string{}
	}
	c.startErrors[name] = err.Error()
}

// Driver returns the runtime driver (for mode-specific APIs).
func (c *Controller) Driver() driver.Driver { return c.drv }

// DriverKind identifies the underlying driver implementation, surfaced via
// the GetMode RPC's driverKind field.
func (c *Controller) DriverKind() string {
	return c.drv.Kind()
}

func (c *Controller) trigger() {
	select {
	case c.reconcileCh <- struct{}{}:
	default:
	}
}

// resyncInterval is how often Run re-diffs desired vs. actual state even
// without an explicit Add/Remove trigger. This is what notices a
// container/Pod that died on its own (OOM, node reschedule, manual
// docker kill / kubectl delete) — Create sets RestartPolicy "no"/Never
// specifically so this loop, not the platform, is what makes crashes
// visible and recovers from them.
const resyncInterval = 30 * time.Second

// Run processes reconcile events until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) {
	c.trigger()
	ticker := time.NewTicker(resyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.reconcileCh:
			c.sync(ctx)
		case <-ticker.C:
			c.sync(ctx)
		}
	}
}

func (c *Controller) sync(ctx context.Context) {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	freshSnapshot := true
	lib := c.refreshLibrary(ctx)
	if c.store != nil {
		defs, err := c.store.Load(ctx)
		if err != nil {
			freshSnapshot = false
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
	c.mu.RLock()
	desired := make(map[string]spec.FlowDefinition, len(c.desired))
	for k, v := range c.desired {
		desired[k] = cloneDefinition(v)
	}
	c.mu.RUnlock()

	actual, err := c.drv.List(ctx)
	if err != nil {
		slog.Warn("controller: list error", "error", err)
		return
	}

	actualSet := make(map[string]spec.FlowDefinition, len(actual))
	for _, d := range actual {
		actualSet[d.Name] = d
	}

	for name, def := range desired {
		// Runtimes receive library-resolved definitions, so a library edit
		// shows up as drift and recreates the listeners that follow it.
		resolved, err := expressions.Resolve(def, lib)
		if err != nil {
			slog.Error("controller: CEL library reference unresolved; keeping current runtime", "name", name, "error", err)
			continue
		}
		def = resolved
		current, running := actualSet[name]
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
			if !freshSnapshot {
				continue
			} // Keep uncertain live state until storage recovers.
			if err := c.drv.Delete(ctx, name); err != nil {
				slog.Error("controller: delete drifted flow failed", "name", name, "error", err)
				continue
			}
			running = false
		}
		if !running {
			err := c.drv.Create(ctx, def)
			c.recordStart(name, err)
			if err != nil {
				slog.Error("controller: create failed", "name", name, "error", err)
			} else {
				slog.Info("controller: flow started", "name", name, "port", def.Source.Listening().Port)
			}
		}
	}

	for _, d := range actual {
		if !freshSnapshot {
			break
		}
		if _, want := desired[d.Name]; !want {
			if err := c.drv.Delete(ctx, d.Name); err != nil {
				slog.Error("controller: delete failed", "name", d.Name, "error", err)
			} else {
				slog.Info("controller: flow stopped", "name", d.Name)
			}
		}
	}

	// Objects derived from the whole flow set (UDS network allows, Gateway
	// listeners) are rebuilt only from a complete, fresh snapshot.
	if reconciler, ok := c.drv.(driver.FlowSetReconciler); ok && freshSnapshot {
		defs := make([]spec.FlowDefinition, 0, len(desired))
		for _, def := range desired {
			defs = append(defs, def)
		}
		err := reconciler.ReconcileFlowSet(ctx, defs)
		message := ""
		if err != nil {
			message = err.Error()
		}
		if message != c.lastFlowSetErr {
			c.lastFlowSetErr = message
			if err != nil {
				slog.Error("controller: flow-set reconcile failed", "error", err)
			}
		}
	}
}

// Compare the serialized contract so omitted empty slices/maps are equivalent
// to explicit empty collections after a container or file round trip.
func equivalentDefinitions(a, b spec.FlowDefinition) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && string(left) == string(right)
}

// Definitions include mutable maps/slices. Never expose controller-owned state
// to API callers, stores, or drivers that could mutate it concurrently.
func cloneDefinition(def spec.FlowDefinition) spec.FlowDefinition {
	data, _ := json.Marshal(def)
	var out spec.FlowDefinition
	_ = json.Unmarshal(data, &out)
	return out
}
