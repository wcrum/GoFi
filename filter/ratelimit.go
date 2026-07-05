package filter

import (
	"fmt"
	"sync"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// rateLimitStage drops messages once more than MaxPerSecond have been seen
// within the current fixed one-second window. This is a simple fixed-window
// counter, NOT a smooth token bucket — bursts at a window boundary can let
// through up to ~2x MaxPerSecond in a short span. That tradeoff is
// deliberate: it avoids pulling in a new dependency (e.g.
// golang.org/x/time/rate) for what's a reasonably approximate feature.
//
// This stage holds mutable state (windowStart/count) that MUST be shared
// across all connections of a listener, not rebuilt per connection — see
// the Chain-construction fix in listener.New/pipeline.New: the Chain
// (and therefore this stage) is built exactly once per listener and reused
// for every accepted connection, so the counter is correctly listener-wide.
type rateLimitStage struct {
	maxPerSecond int

	mu          sync.Mutex
	windowStart time.Time
	count       int
}

func newRateLimit(cfg spec.StageSpec) (Stage, error) {
	if cfg.MaxPerSecond <= 0 {
		return nil, fmt.Errorf("rate-limit: maxPerSecond must be > 0")
	}
	return &rateLimitStage{maxPerSecond: int(cfg.MaxPerSecond)}, nil
}

func (r *rateLimitStage) Process(msg *message.Message) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if r.windowStart.IsZero() || now.Sub(r.windowStart) >= time.Second {
		r.windowStart = now
		r.count = 0
	}
	r.count++
	if r.count > r.maxPerSecond {
		return false, nil
	}
	return true, nil
}
