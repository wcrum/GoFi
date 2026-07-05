package filter

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/cel-go/cel"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

const defaultDedupWindow = 30 * time.Second

// dedupStage drops messages whose KeyExpr value was already seen within the
// last Window. Like rateLimitStage, its `seen` map is listener-wide shared
// state — it relies on the Chain (and therefore this stage) being built
// once per listener and reused across every connection.
type dedupStage struct {
	program cel.Program
	window  time.Duration

	mu   sync.Mutex
	seen map[string]time.Time
}

func newDedup(cfg spec.StageSpec) (Stage, error) {
	if cfg.KeyExpr == "" {
		return nil, fmt.Errorf("dedup: keyExpr is required")
	}
	prog, err := CompileCEL(cfg.KeyExpr)
	if err != nil {
		return nil, fmt.Errorf("dedup: compile keyExpr %q: %w", cfg.KeyExpr, err)
	}

	window := defaultDedupWindow
	if cfg.Window != "" {
		w, err := time.ParseDuration(cfg.Window)
		if err != nil {
			return nil, fmt.Errorf("dedup: invalid window %q: %w", cfg.Window, err)
		}
		window = w
	}

	return &dedupStage{program: prog, window: window, seen: make(map[string]time.Time)}, nil
}

func (d *dedupStage) Process(msg *message.Message) (bool, error) {
	val, err := EvalCEL(d.program, msg)
	if err != nil {
		// Fail open: pass the message through unchanged rather than dedup it.
		return true, fmt.Errorf("dedup: eval keyExpr: %w", err)
	}
	key := valToString(val)

	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()

	if lastSeen, ok := d.seen[key]; ok && now.Sub(lastSeen) < d.window {
		return false, nil
	}
	d.seen[key] = now

	// Lazy cleanup of stale entries; O(n) per message but fine at this scale.
	for k, t := range d.seen {
		if now.Sub(t) >= d.window {
			delete(d.seen, k)
		}
	}

	return true, nil
}

func valToString(val interface{}) string {
	if s, ok := val.(string); ok {
		return s
	}
	return fmt.Sprint(val)
}
