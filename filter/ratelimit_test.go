package filter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestNewRateLimit_RequiresPositiveMax(t *testing.T) {
	for _, max := range []float64{0, -1} {
		_, err := New(spec.StageSpec{ID: "r1", Kind: "rate-limit", MaxPerSecond: max})
		if err == nil {
			t.Fatalf("expected error for maxPerSecond=%v, got nil", max)
		}
	}
}

func TestRateLimit_AllowsUpToMaxThenDrops(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "r1", Kind: "rate-limit", MaxPerSecond: 2})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")

	for i := 0; i < 2; i++ {
		pass, err := st.Process(msg)
		if err != nil {
			t.Fatalf("Process error = %v", err)
		}
		if !pass {
			t.Fatalf("message %d should have passed within limit", i)
		}
	}

	pass, err := st.Process(msg)
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if pass {
		t.Fatal("3rd message within the same window should have been dropped")
	}
}

func TestRateLimit_ResetsAfterWindow(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "r1", Kind: "rate-limit", MaxPerSecond: 1})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")

	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("first message: pass=%v err=%v, want true, nil", pass, err)
	}

	pass, _ = st.Process(msg)
	if pass {
		t.Fatal("second message in same window should have been dropped")
	}

	time.Sleep(1100 * time.Millisecond)

	pass, err = st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("after window reset: pass=%v err=%v, want true, nil", pass, err)
	}
}

// TestRateLimit_ConcurrentAccess fires many goroutines at a single shared
// rateLimitStage (as it would be shared listener-wide across connections,
// per the doc comment on rateLimitStage) and confirms, under -race, both
// that there's no data race on the mutex-guarded windowStart/count state and
// that the number of passes within the single window never exceeds
// MaxPerSecond. All calls happen well within one window (the implementation
// is a real-time fixed window with no injectable clock), so this test is
// intentionally short-lived to avoid a window boundary crossing mid-run.
func TestRateLimit_ConcurrentAccess(t *testing.T) {
	const maxPerSecond = 50
	const numGoroutines = 200

	st, err := New(spec.StageSpec{ID: "r1", Kind: "rate-limit", MaxPerSecond: maxPerSecond})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	var passed int64
	var wg sync.WaitGroup
	msg := message.New(nil, "l", "r")

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			pass, err := st.Process(msg)
			if err != nil {
				t.Errorf("Process error = %v, want nil", err)
			}
			if pass {
				atomic.AddInt64(&passed, 1)
			}
		}()
	}
	wg.Wait()

	if passed > maxPerSecond {
		t.Fatalf("passed = %d, want <= %d (MaxPerSecond) within one window", passed, maxPerSecond)
	}
	if passed == 0 {
		t.Fatal("expected at least some messages to pass")
	}
}
