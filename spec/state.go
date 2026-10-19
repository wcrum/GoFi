package spec

import "fmt"

// Flow run states (FlowDefinition.State). Empty means running.
const (
	StateRunning = "running"
	StateStopped = "stopped"
)

// Stopped reports whether the flow is declared stopped: the controller keeps
// its definition but runs no runtime for it.
func (d FlowDefinition) Stopped() bool { return d.State == StateStopped }

// ValidateState checks the declared run state.
func ValidateState(state string) error {
	switch state {
	case "", StateRunning, StateStopped:
		return nil
	}
	return fmt.Errorf("state must be %q or %q, not %q", StateRunning, StateStopped, state)
}
