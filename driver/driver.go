// Package driver abstracts the runtime that hosts listener instances.
// The in-process driver runs them as goroutines; Docker and Kubernetes
// drivers spawn separate containers/pods running the same binary in
// "listener mode" (see main.go's FLOW_SPEC handling).
package driver

import (
	"context"
	"time"

	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/spec"
)

// TapTarget describes how to reach a listener's live traffic tap.
// Exactly one of Local or Addr is meaningful for any given driver:
// InProcess always returns Local (a direct in-memory subscribe), while
// Docker/Kube always return Addr (a "host:port" the API layer proxies to,
// since the tap lives in a different process/container/pod).
type TapTarget struct {
	// Local is the live pipeline tap, non-nil only for the in-process driver.
	Local *pipeline.Tap
	// Addr is a "host:port" the caller uses for ConnectRPC traffic streams,
	// set only for remote drivers (Docker/Kubernetes).
	Addr string
}

// RuntimeInfo describes the underlying compute backing a running listener,
// for observability. Fields are driver-specific; a nil-value field simply
// means that driver doesn't have that concept (e.g. InProcess has no
// container ID).
type RuntimeInfo struct {
	Kind      string    `json:"kind"`             // "inprocess" | "docker" | "kubernetes"
	ID        string    `json:"id,omitempty"`     // container ID or pod name
	Status    string    `json:"status,omitempty"` // e.g. "running", "pending", "exited"
	StartedAt time.Time `json:"startedAt,omitempty"`
	Node      string    `json:"node,omitempty"` // k8s node name, if applicable
	IP        string    `json:"ip,omitempty"`   // container/pod IP, if applicable
	// Error is the controller's last failure to start this flow's runtime
	// (for example, a Service the API server rejected).
	Error string `json:"error,omitempty"`
}

// Driver creates and destroys listener instances.
type Driver interface {
	// Create starts a new listener for the given definition.
	Create(ctx context.Context, def spec.FlowDefinition) error
	// Delete stops the named listener.
	Delete(ctx context.Context, name string) error
	// List returns the specs of all currently running listeners.
	List(ctx context.Context) ([]spec.FlowDefinition, error)
	// Tap returns how to reach a named listener's live traffic tap.
	// ok is false if the listener is not found or not yet reachable.
	Tap(ctx context.Context, name string) (target TapTarget, ok bool, err error)
	// RuntimeStatus describes the compute currently backing a named listener
	// (container/Pod status, start time, etc.), for observability. ok is
	// false if the listener is not found.
	RuntimeStatus(ctx context.Context, name string) (RuntimeInfo, bool, error)
	// Kind identifies the driver implementation (e.g. "inprocess", "docker",
	// "kubernetes"), surfaced by the GetMode RPC.
	Kind() string
}

// Replacer is a driver that can update a running listener without an
// outage: Replace starts the new runtime beside the old one and the driver
// retires the old one once the new one runs (the Kubernetes driver). The
// controller uses it instead of Delete followed by Create. A driver may
// return ErrPending when the previous runtime is still being removed.
type Replacer interface {
	Replace(ctx context.Context, def spec.FlowDefinition) error
}

// FlowLookup is List for a single flow, for event-driven reconciliation of
// one flow at a time: ok is false when the flow has no live runtime.
type FlowLookup interface {
	Lookup(ctx context.Context, name string) (def spec.FlowDefinition, ok bool, err error)
}
