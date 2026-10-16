package controller

import (
	"context"
	"sync"
	"time"

	"github.com/wcrum/gofi/spec"
)

// NotifyRuntimeChange schedules reconciliation after worker readiness or
// credentials change. Only listeners carrying stale bindings are recreated.
func (c *Controller) NotifyRuntimeChange() {
	c.trigger()
}

// InlineHooks connect the controller to the process that owns Python workers.
// Validate statically checks inline stage source (never executing it) before
// an API edit is accepted; Changed asks the owner to reconcile the managed
// libraries that hold listeners' inline stages.
type InlineHooks struct {
	Validate func(context.Context, spec.FlowDefinition) error
	Changed  func()
}

var inlineMu sync.RWMutex
var inlineHooks InlineHooks

func SetInlineHooks(h InlineHooks) { inlineMu.Lock(); inlineHooks = h; inlineMu.Unlock() }

func hooks() InlineHooks { inlineMu.RLock(); defer inlineMu.RUnlock(); return inlineHooks }

// InvalidInlineError marks an inline-stage definition the caller must fix
// (as opposed to a storage or runtime failure).
type InvalidInlineError struct{ Err error }

func (e InvalidInlineError) Error() string { return e.Err.Error() }
func (e InvalidInlineError) Unwrap() error { return e.Err }

// prepareInline derives managed-library references for inline stages. It runs
// on every load path (API, file, CRD) so declarative resources may carry only
// the inline source.
func prepareInline(def *spec.FlowDefinition) error {
	def.ResolveInlinePython()
	if err := def.ValidateInlinePlacement(); err != nil {
		return InvalidInlineError{err}
	}
	return nil
}

// validateInline runs the static source check for API edits.
func validateInline(def spec.FlowDefinition) error {
	h := hooks()
	if h.Validate == nil || len(def.InlineStages()) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := h.Validate(ctx, def); err != nil {
		return InvalidInlineError{err}
	}
	return nil
}

func inlineChanged() {
	if h := hooks(); h.Changed != nil {
		h.Changed()
	}
}
