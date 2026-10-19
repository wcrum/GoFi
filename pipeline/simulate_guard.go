package pipeline

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/wcrum/gofi/spec"
)

// Simulate and the debugger run user-supplied frames through decoders and
// Python steps on the API goroutine. Both are bounded here: at most
// simulateConcurrency dry runs at once (callers past the limit wait up to
// simulateQueueWait, then fail), and no dry run may take longer than
// simulateTimeout. A run that overruns is abandoned to finish on its own
// goroutine, which the concurrency bound then still counts.
const (
	simulateQueueWait = 2 * time.Second
	simulateTimeout   = 10 * time.Second
)

var simulateConcurrency = max(2, runtime.GOMAXPROCS(0))

var simulateSlots = make(chan struct{}, simulateConcurrency)

// Simulation guard errors.
var (
	ErrSimulateBusy    = errors.New("simulate: too many simulations in progress; try again")
	ErrSimulateTimeout = fmt.Errorf("simulate: the dry run exceeded %v", simulateTimeout)
)

type simulateOutcome struct {
	result *SimulateResult
	target *DebugTarget
	err    error
}

// simulateGuarded runs simulate under the concurrency and time bounds.
func simulateGuarded(def spec.FlowDefinition, raw []byte, stop string) (*SimulateResult, *DebugTarget, error) {
	select {
	case simulateSlots <- struct{}{}:
	case <-time.After(simulateQueueWait):
		return nil, nil, ErrSimulateBusy
	}
	done := make(chan simulateOutcome, 1)
	go func() {
		defer func() { <-simulateSlots }()
		result, target, err := simulate(def, raw, stop)
		done <- simulateOutcome{result, target, err}
	}()
	select {
	case out := <-done:
		return out.result, out.target, out.err
	case <-time.After(simulateTimeout):
		return nil, nil, ErrSimulateTimeout
	}
}
