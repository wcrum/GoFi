package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// ErrFlowNotFound is returned for operations on a flow that does not exist.
var ErrFlowNotFound = errors.New("flow not found")

// FlowPhase values (FlowStatus.Phase).
const (
	PhaseRunning  = "running"
	PhaseStarting = "starting"
	PhaseStopping = "stopping"
	PhaseStopped  = "stopped"
	PhaseCrashed  = "crashed"
	PhaseFailed   = "failed"
	PhaseInvalid  = "invalid"
)

// operations is the controller's runtime bookkeeping: restarts of runtimes
// that exited on their own. It lives in memory and resets with the controller.
type operations struct {
	mu       sync.Mutex
	restarts map[string]restartRecord
}

type restartRecord struct {
	count int
	last  time.Time
}

// FlowStatus is a flow's declared and observed run state.
type FlowStatus struct {
	Name          string
	Desired       string // spec.StateRunning or spec.StateStopped
	Phase         string
	Restarts      int
	LastRestartAt time.Time
	RuntimeStatus string
	StartedAt     time.Time
	Message       string
}

// SetState stores the flow's declared run state through the normal save path
// (Add), so the change is persisted, versioned, and reconciled like an edit.
// Running is stored as an omitted state, keeping manifests minimal.
func (c *Controller) SetState(name, state string) error {
	if err := spec.ValidateState(state); err != nil {
		return err
	}
	if state == spec.StateRunning {
		state = ""
	}
	c.mu.RLock()
	bad, isInvalid := c.invalid[name]
	def, exists := c.desired[name]
	c.mu.RUnlock()
	if isInvalid {
		return fmt.Errorf("flow %q has an invalid definition; fix it before starting or stopping it: %s", name, bad.err)
	}
	if !exists {
		return ErrFlowNotFound
	}
	if def.State == state {
		return nil
	}
	def = cloneDefinition(def)
	def.StripDerivedPython() // Add derives the managed library again
	def.State = state
	return c.Add(def)
}

// Restart recreates a running flow's runtime without changing its definition.
// The old runtime drains first; the reconcile it triggers starts a new one.
func (c *Controller) Restart(ctx context.Context, name string) error {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	c.mu.RLock()
	def, exists := c.desired[name]
	bad, isInvalid := c.invalid[name]
	c.mu.RUnlock()
	switch {
	case isInvalid:
		return fmt.Errorf("flow %q has an invalid definition and does not run: %s", name, bad.err)
	case !exists:
		return ErrFlowNotFound
	case def.Stopped():
		return fmt.Errorf("flow %q is stopped; start it instead", name)
	}
	if _, found, err := c.drv.RuntimeStatus(ctx, name); err != nil {
		return err
	} else if found {
		if err := c.drv.Delete(ctx, name); err != nil {
			return fmt.Errorf("stop runtime: %w", err)
		}
	}
	slog.Info("controller: flow restart requested", "name", name)
	c.trigger()
	return nil
}

// Status reports a flow's run state. ok is false for an unknown flow.
func (c *Controller) Status(ctx context.Context, name string) (FlowStatus, bool) {
	def, exists := c.Get(name)
	if !exists {
		return FlowStatus{}, false
	}
	status := FlowStatus{Name: name, Desired: spec.StateRunning}
	if def.Stopped() {
		status.Desired = spec.StateStopped
	}
	c.ops.mu.Lock()
	record := c.ops.restarts[name]
	c.ops.mu.Unlock()
	status.Restarts, status.LastRestartAt = record.count, record.last

	c.mu.RLock()
	bad, isInvalid := c.invalid[name]
	startErr := c.startErrors[name]
	c.mu.RUnlock()
	info, found, err := c.drv.RuntimeStatus(ctx, name)
	if found {
		status.RuntimeStatus, status.StartedAt, status.Message = info.Status, info.StartedAt, info.Error
	}
	runtimeState := strings.ToLower(info.Status)
	switch {
	case isInvalid && !found:
		status.Phase, status.Message = PhaseInvalid, bad.err
	case err != nil:
		status.Phase, status.Message = "unknown", err.Error()
	case def.Stopped():
		status.Phase = PhaseStopped
		if found && isLive(runtimeState) {
			status.Phase = PhaseStopping
		}
	case startErr != "":
		status.Phase, status.Message = PhaseFailed, startErr
	case !found:
		status.Phase = PhaseStarting
		if record.count > 0 && time.Since(record.last) < resyncInterval*2 {
			status.Phase = PhaseCrashed
		}
	case runtimeState == "pending" || runtimeState == "created" || runtimeState == "restarting":
		status.Phase = PhaseStarting
	case isLive(runtimeState):
		status.Phase = PhaseRunning
	default:
		status.Phase = PhaseCrashed
	}
	return status, true
}

func isLive(runtimeState string) bool {
	switch runtimeState {
	case "running", "ready", "active", "pending", "created", "restarting":
		return true
	}
	return false
}

// noteRuntimeExit runs before the reconcile starts a runtime that should
// already be running. A runtime the driver still reports (an exited container,
// a failed Pod) ended on its own: count the restart and post a bulletin.
func (c *Controller) noteRuntimeExit(ctx context.Context, name string) {
	info, found, err := c.drv.RuntimeStatus(ctx, name)
	if err != nil || !found || isLive(strings.ToLower(info.Status)) {
		return
	}
	c.ops.mu.Lock()
	if c.ops.restarts == nil {
		c.ops.restarts = map[string]restartRecord{}
	}
	record := c.ops.restarts[name]
	record.count++
	record.last = time.Now()
	c.ops.restarts[name] = record
	c.ops.mu.Unlock()
	message := fmt.Sprintf("runtime exited (%s); restarting it", info.Status)
	slog.Warn("controller: flow runtime exited", "name", name, "status", info.Status, "restarts", record.count)
	observability.Report(name, observability.SeverityError, observability.ComponentRuntime, message)
	observability.FlowRestarted(name)
}

// publishFlowStates exports declared and observed state for alerting.
func publishFlowStates(desired map[string]spec.FlowDefinition, actual map[string]spec.FlowDefinition) {
	states := make(map[string]observability.FlowState, len(desired))
	for name, def := range desired {
		_, up := actual[name]
		states[name] = observability.FlowState{DesiredRunning: !def.Stopped(), RuntimeUp: up}
	}
	observability.SetFlowStates(states)
}

// stopRuntime tears down a stopped flow's runtime. Its Kubernetes Service and
// routes go with it: they belong to the runtime's Pod.
func (c *Controller) stopRuntime(ctx context.Context, name string, running bool) {
	c.recordStart(name, nil)
	if !running {
		return
	}
	if err := c.drv.Delete(ctx, name); err != nil {
		slog.Error("controller: stop flow failed", "name", name, "error", err)
		return
	}
	slog.Info("controller: flow stopped (declared state)", "name", name)
}

// runtimeDefinition is what the driver runs: the declared state is the
// controller's concern and never reaches (or drifts) a runtime.
func runtimeDefinition(def spec.FlowDefinition) spec.FlowDefinition {
	def.State = ""
	// Runtimes receive explicit Python libraries (bindings are computed from
	// them); the stored definition keeps the authored, default-library form.
	prepared, err := def.Prepared()
	if err != nil {
		// Validation rejects such definitions before they are desired.
		slog.Error("controller: flow definition could not be prepared for its runtime", "name", def.Name, "error", err)
		return def
	}
	def = prepared
	return def
}

// equivalentRuntimes compares definitions as runtimes see them, so a change
// of declared state alone never recreates a runtime.
func equivalentRuntimes(a, b spec.FlowDefinition) bool {
	return equivalentDefinitions(runtimeDefinition(a), runtimeDefinition(b))
}
