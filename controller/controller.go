// Package controller maintains desired listener state and reconciles it
// against the driver. Reconciliation is event-driven: the API triggers a
// reconcile via trigger() after each mutation.
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/spec"
)

// Controller holds desired state and drives the runtime driver to match it.
type Controller struct {
	mu      sync.RWMutex
	desired map[string]spec.ListenerDefinition
	drv     driver.Driver
	store   desired.Store
	// Serialize mutations and reconciliation so a stale reconcile cannot
	// overwrite an API edit or re-create a resource after its deletion.
	syncMu      sync.Mutex
	reconcileCh chan struct{}
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
		return err
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
	return desired.Configuration{Source: "memory", APIVersion: desired.APIVersion, Kind: "Listener"}
}

// replaceDesired validates the complete snapshot before changing anything.
// Failed reads/invalid manifests retain the last known good configuration.
func (c *Controller) replaceDesired(ctx context.Context, defs []spec.ListenerDefinition) error {
	next := make(map[string]spec.ListenerDefinition, len(defs))
	for _, def := range defs {
		if err := c.Validate(def); err != nil {
			return err
		}
		if _, exists := next[def.Name]; exists {
			return fmt.Errorf("duplicate Listener %q", def.Name)
		}
		next[def.Name] = cloneDefinition(def)
	}
	c.mu.Lock()
	old := c.desired
	c.desired = next
	c.mu.Unlock()
	for name, def := range old {
		if updated, ok := next[name]; ok && !equivalentDefinitions(def, updated) {
			if err := c.drv.Delete(ctx, name); err != nil {
				slog.Warn("controller: delete changed listener", "name", name, "error", err)
			}
		}
	}
	return nil
}

func New(d driver.Driver) *Controller {
	return &Controller{
		desired:     make(map[string]spec.ListenerDefinition),
		drv:         d,
		reconcileCh: make(chan struct{}, 1),
	}
}

// Add registers a listener definition and triggers reconciliation.
// If a definition with the same name exists, it is replaced (update).
func (c *Controller) Add(def spec.ListenerDefinition) error {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	if err := c.Validate(def); err != nil {
		return err
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
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	if c.store != nil {
		if err := c.store.Delete(context.Background(), name); err != nil {
			return err
		}
	}
	c.mu.Lock()
	delete(c.desired, name)
	c.mu.Unlock()
	c.trigger()
	return nil
}

// Get returns the desired definition for a named listener, or false.
func (c *Controller) Get(name string) (spec.ListenerDefinition, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	d, ok := c.desired[name]
	return cloneDefinition(d), ok
}

// List returns all desired listener definitions.
func (c *Controller) List() []spec.ListenerDefinition {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]spec.ListenerDefinition, 0, len(c.desired))
	for _, d := range c.desired {
		out = append(out, cloneDefinition(d))
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
	return c.drv.RuntimeStatus(ctx, name)
}

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
	if c.store != nil {
		defs, err := c.store.Load(ctx)
		if err != nil {
			freshSnapshot = false
			slog.Warn("controller: desired state load failed", "error", err)
		} else if err = c.replaceDesired(ctx, defs); err != nil {
			freshSnapshot = false
			slog.Warn("controller: invalid desired state snapshot", "error", err)
		}
	}
	c.mu.RLock()
	desired := make(map[string]spec.ListenerDefinition, len(c.desired))
	for k, v := range c.desired {
		desired[k] = cloneDefinition(v)
	}
	c.mu.RUnlock()

	actual, err := c.drv.List(ctx)
	if err != nil {
		slog.Warn("controller: list error", "error", err)
		return
	}

	actualSet := make(map[string]spec.ListenerDefinition, len(actual))
	for _, d := range actual {
		actualSet[d.Name] = d
	}

	for name, def := range desired {
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
				slog.Error("controller: delete drifted listener failed", "name", name, "error", err)
				continue
			}
			running = false
		}
		if !running {
			if err := c.drv.Create(ctx, def); err != nil {
				slog.Error("controller: create failed", "name", name, "error", err)
			} else {
				slog.Info("controller: listener started", "name", name, "port", def.Source.Port)
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
				slog.Info("controller: listener stopped", "name", d.Name)
			}
		}
	}
}

// Compare the serialized contract so omitted empty slices/maps are equivalent
// to explicit empty collections after a container or file round trip.
func equivalentDefinitions(a, b spec.ListenerDefinition) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && string(left) == string(right)
}

// Definitions include mutable maps/slices. Never expose controller-owned state
// to API callers, stores, or drivers that could mutate it concurrently.
func cloneDefinition(def spec.ListenerDefinition) spec.ListenerDefinition {
	data, _ := json.Marshal(def)
	var out spec.ListenerDefinition
	_ = json.Unmarshal(data, &out)
	return out
}
