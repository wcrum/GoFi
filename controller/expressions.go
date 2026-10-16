package controller

import (
	"context"
	"log/slog"

	"github.com/wcrum/gofi/expressions"
	"github.com/wcrum/gofi/spec"
)

// SetExpressions connects the CEL expression library. Call it before
// WithStore so stored listeners that link library entries validate.
func (c *Controller) SetExpressions(m *expressions.Manager) {
	c.mu.Lock()
	c.expr = m
	c.mu.Unlock()
}

// Expressions returns the connected library manager, or nil.
func (c *Controller) Expressions() *expressions.Manager {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.expr
}

func (c *Controller) library() expressions.Library {
	if m := c.Expressions(); m != nil {
		return m.Cached()
	}
	return nil
}

// refreshLibrary reloads the library from its store during reconciliation so
// declarative file/CRD edits reach listeners. Load failures keep the last
// good snapshot.
func (c *Controller) refreshLibrary(ctx context.Context) expressions.Library {
	m := c.Expressions()
	if m == nil {
		return nil
	}
	lib, err := m.Snapshot(ctx)
	if err != nil {
		slog.Warn("controller: CEL library load failed; using last good snapshot", "error", err)
	}
	return lib
}

// Resolve replaces library references in def with expression text from the
// current library snapshot (see expressions.Resolve).
func (c *Controller) Resolve(def spec.FlowDefinition) (spec.FlowDefinition, error) {
	return expressions.Resolve(def, c.library())
}

// ValidateWith validates def after resolving its library references against
// lib. ExpressionService uses it to test a library change before saving.
func (c *Controller) ValidateWith(def spec.FlowDefinition, lib expressions.Library) error {
	resolved, err := expressions.Resolve(def, lib)
	if err != nil {
		return err
	}
	return c.validateResolved(resolved)
}
