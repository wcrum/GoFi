// Package controller maintains desired listener state and reconciles it
// against the driver. Reconciliation is event-driven: the API triggers a
// reconcile via trigger() after each mutation.
package controller

import (
	"context"
	"log/slog"
	"sync"

	"github.com/cnak-us/gogo/driver"
	"github.com/cnak-us/gogo/listener"
	"github.com/cnak-us/gogo/spec"
)

// Controller holds desired state and drives the runtime driver to match it.
type Controller struct {
	mu        sync.RWMutex
	desired   map[string]spec.ListenerDefinition
	drv       driver.Driver
	reconcileCh chan struct{}
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
	c.mu.Lock()
	_, exists := c.desired[def.Name]
	c.desired[def.Name] = def
	c.mu.Unlock()

	if exists {
		// For updates, delete the old instance first so the driver re-creates it.
		if err := c.drv.Delete(context.Background(), def.Name); err != nil {
			slog.Warn("controller: delete before update", "name", def.Name, "error", err)
		}
	}

	c.trigger()
	return nil
}

// Remove deregisters a listener definition and triggers reconciliation.
func (c *Controller) Remove(name string) {
	c.mu.Lock()
	delete(c.desired, name)
	c.mu.Unlock()
	c.trigger()
}

// Get returns the desired definition for a named listener, or false.
func (c *Controller) Get(name string) (spec.ListenerDefinition, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	d, ok := c.desired[name]
	return d, ok
}

// List returns all desired listener definitions.
func (c *Controller) List() []spec.ListenerDefinition {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]spec.ListenerDefinition, 0, len(c.desired))
	for _, d := range c.desired {
		out = append(out, d)
	}
	return out
}

// Tap returns the live traffic tap for a named listener (in-process only).
func (c *Controller) Tap(name string) *listener.Listener {
	return c.drv.Tap(name)
}

func (c *Controller) trigger() {
	select {
	case c.reconcileCh <- struct{}{}:
	default:
	}
}

// Run processes reconcile events until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.reconcileCh:
			c.sync(ctx)
		}
	}
}

func (c *Controller) sync(ctx context.Context) {
	c.mu.RLock()
	desired := make(map[string]spec.ListenerDefinition, len(c.desired))
	for k, v := range c.desired {
		desired[k] = v
	}
	c.mu.RUnlock()

	actual, err := c.drv.List(ctx)
	if err != nil {
		slog.Warn("controller: list error", "error", err)
		return
	}

	actualSet := make(map[string]struct{}, len(actual))
	for _, d := range actual {
		actualSet[d.Name] = struct{}{}
	}

	for name, def := range desired {
		if _, running := actualSet[name]; !running {
			if err := c.drv.Create(ctx, def); err != nil {
				slog.Error("controller: create failed", "name", name, "error", err)
			} else {
				slog.Info("controller: listener started", "name", name, "port", def.Source.Port)
			}
		}
	}

	for _, d := range actual {
		if _, want := desired[d.Name]; !want {
			if err := c.drv.Delete(ctx, d.Name); err != nil {
				slog.Error("controller: delete failed", "name", d.Name, "error", err)
			} else {
				slog.Info("controller: listener stopped", "name", d.Name)
			}
		}
	}
}
