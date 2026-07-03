// Package driver abstracts the runtime that hosts listener instances.
// The in-process driver runs them as goroutines; Docker and Kubernetes
// drivers (stubbed) spawn separate containers/pods.
package driver

import (
	"context"

	"github.com/cnak-us/gogo/listener"
	"github.com/cnak-us/gogo/spec"
)

// Driver creates and destroys listener instances.
type Driver interface {
	// Create starts a new listener for the given definition.
	Create(ctx context.Context, def spec.ListenerDefinition) error
	// Delete stops the named listener.
	Delete(ctx context.Context, name string) error
	// List returns the specs of all currently running listeners.
	List(ctx context.Context) ([]spec.ListenerDefinition, error)
	// Tap returns the pipeline tap for a named listener, or nil if not found.
	// Only meaningful for the in-process driver.
	Tap(name string) *listener.Listener
}
