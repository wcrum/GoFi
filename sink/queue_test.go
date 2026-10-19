package sink

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wcrum/gofi/config"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// gateSink records sends and can be held so the queue fills.
type gateSink struct {
	mu      sync.Mutex
	got     []string
	gate    chan struct{} // when non-nil, Send waits for a receive
	fail    atomic.Int64  // remaining sends that fail with failErr
	failErr error
	calls   atomic.Int64
	flushed atomic.Bool
	closed  atomic.Bool
}

func (s *gateSink) Send(m *message.Message) error {
	s.calls.Add(1)
	if s.gate != nil {
		<-s.gate
	}
	if s.fail.Load() > 0 {
		s.fail.Add(-1)
		return s.failErr
	}
	s.mu.Lock()
	s.got = append(s.got, string(m.Raw))
	s.mu.Unlock()
	return nil
}
func (s *gateSink) Flush(context.Context) error { s.flushed.Store(true); return nil }
func (s *gateSink) Close() error                { s.closed.Store(true); return nil }
func (s *gateSink) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func testQueueConfig() QueueConfig {
	c := DefaultQueueConfig()
	c.Depth = 4
	c.Retries = 2
	c.RetryInitial = 5 * time.Millisecond
	c.RetryMax = 20 * time.Millisecond
	c.BreakerFailures = 3
	c.BreakerCooldown = 100 * time.Millisecond
	c.BatchInterval = 20 * time.Millisecond
	return c
}

func msg(s string) *message.Message {
	return &message.Message{Raw: []byte(s), Fields: map[string]any{}, Meta: map[string]string{}}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

// Send never waits on the destination: the worker is held, Send still
// returns, and once released everything arrives in order.
func TestQueuedSendIsAsynchronousAndOrdered(t *testing.T) {
	inner := &gateSink{gate: make(chan struct{})}
	q := NewQueued(inner, "test", "flow", "egress-sink-0", testQueueConfig())
	defer q.Close()
	if !q.Async() {
		t.Fatal("queued sink must report Async")
	}
	for i, s := range []string{"a", "b", "c", "d"} {
		started := time.Now()
		if err := q.Send(msg(s)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		if time.Since(started) > 500*time.Millisecond {
			t.Fatal("Send waited on the destination")
		}
	}
	if got := inner.received(); len(got) != 0 {
		t.Fatalf("delivered before release: %v", got)
	}
	close(inner.gate)
	waitUntil(t, func() bool { return len(inner.received()) == 4 })
	if got := inner.received(); got[0] != "a" || got[3] != "d" {
		t.Fatalf("order lost: %v", got)
	}
}

// Policy block: a full queue makes Send wait for room (back-pressure);
// policy drop-newest rejects with ErrQueueFull; drop-oldest evicts.
func TestQueuedDropPolicies(t *testing.T) {
	t.Run("block", func(t *testing.T) {
		inner := &gateSink{gate: make(chan struct{})}
		q := NewQueued(inner, "test", "flow", "r", testQueueConfig())
		defer q.Close()
		// Worker takes one, four fill the queue, the sixth must block.
		for i := 0; i < 5; i++ {
			if err := q.Send(msg("x")); err != nil {
				t.Fatal(err)
			}
		}
		unblocked := make(chan error, 1)
		go func() { unblocked <- q.Send(msg("y")) }()
		select {
		case err := <-unblocked:
			t.Fatalf("Send on a full queue returned %v instead of blocking", err)
		case <-time.After(100 * time.Millisecond):
		}
		inner.gate <- struct{}{} // deliver one, freeing a slot
		select {
		case err := <-unblocked:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Send did not resume after room was freed")
		}
		close(inner.gate)
	})
	t.Run("drop-newest", func(t *testing.T) {
		inner := &gateSink{gate: make(chan struct{})}
		cfg := testQueueConfig()
		cfg.Policy = config.DropNewest
		q := NewQueued(inner, "test", "flow", "r", cfg)
		defer q.Close()
		var dropped int
		_ = q.Send(msg("a"))
		waitUntil(t, func() bool { return inner.calls.Load() == 1 }) // "a" is in the worker
		for i := 1; i < 8; i++ {
			if err := q.Send(msg(string(rune('a' + i)))); errors.Is(err, ErrQueueFull) {
				dropped++
			} else if err != nil {
				t.Fatal(err)
			}
		}
		if dropped != 3 { // 1 in the worker + 4 queued, 3 rejected
			t.Fatalf("dropped %d, want 3", dropped)
		}
		close(inner.gate)
		waitUntil(t, func() bool { return len(inner.received()) == 5 })
		if got := inner.received(); got[4] != "e" {
			t.Fatalf("newest should have been dropped: %v", got)
		}
	})
	t.Run("drop-oldest", func(t *testing.T) {
		inner := &gateSink{gate: make(chan struct{})}
		cfg := testQueueConfig()
		cfg.Policy = config.DropOldest
		q := NewQueued(inner, "test", "flow", "r", cfg)
		defer q.Close()
		_ = q.Send(msg("a"))
		waitUntil(t, func() bool { return inner.calls.Load() == 1 })
		for i := 1; i < 8; i++ {
			if err := q.Send(msg(string(rune('a' + i)))); err != nil {
				t.Fatal(err)
			}
		}
		close(inner.gate)
		waitUntil(t, func() bool { return len(inner.received()) == 5 })
		if got := inner.received(); got[0] != "a" || got[4] != "h" {
			t.Fatalf("oldest queued should have been evicted: %v", got)
		}
	})
}

// Flush returns once everything enqueued was delivered, then flushes the
// wrapped sink; Close after that closes it.
func TestQueuedFlushDrainsBeforeClose(t *testing.T) {
	inner := &gateSink{}
	q := NewQueued(inner, "test", "flow", "r", testQueueConfig())
	for i := 0; i < 20; i++ {
		if err := q.Send(msg("m")); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := q.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := inner.received(); len(got) != 20 || !inner.flushed.Load() {
		t.Fatalf("after Flush: %d delivered, inner flushed=%v", len(got), inner.flushed.Load())
	}
	if err := q.Close(); err != nil || !inner.closed.Load() {
		t.Fatalf("Close: %v closed=%v", err, inner.closed.Load())
	}
	if err := q.Send(msg("late")); !errors.Is(err, ErrSinkClosed) {
		t.Fatalf("Send after Close = %v", err)
	}
}

// A flush that cannot complete in time reports the context error.
func TestQueuedFlushHonorsDeadline(t *testing.T) {
	inner := &gateSink{gate: make(chan struct{})}
	q := NewQueued(inner, "test", "flow", "r", testQueueConfig())
	defer q.Close()
	defer close(inner.gate) // runs first: the worker must be released before Close waits for it
	_ = q.Send(msg("stuck"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := q.Flush(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Flush = %v", err)
	}
}

// Transient failures are retried with backoff until they succeed; permanent
// ones are not retried.
func TestQueuedRetriesTransientFailures(t *testing.T) {
	inner := &gateSink{failErr: transientError{errors.New("boom")}}
	inner.fail.Store(2)
	q := NewQueued(inner, "test", "flow", "r", testQueueConfig())
	defer q.Close()
	if err := q.Send(msg("retry-me")); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return len(inner.received()) == 1 })
	if inner.calls.Load() != 3 {
		t.Fatalf("%d attempts, want 3", inner.calls.Load())
	}

	permanent := &gateSink{failErr: errors.New("bad request")}
	permanent.fail.Store(1)
	q2 := NewQueued(permanent, "test", "flow", "r", testQueueConfig())
	defer q2.Close()
	_ = q2.Send(msg("once"))
	_ = q2.Send(msg("after"))
	waitUntil(t, func() bool { return len(permanent.received()) == 1 })
	if permanent.calls.Load() != 2 {
		t.Fatalf("permanent failure retried: %d calls", permanent.calls.Load())
	}
}

// After BreakerFailures consecutive failures the breaker opens: deliveries
// fail fast without calling the destination until the cooldown passes, then
// one probe closes it again on success.
func TestQueuedCircuitBreaker(t *testing.T) {
	inner := &gateSink{failErr: errors.New("HTTP 500")}
	inner.fail.Store(1 << 30)
	cfg := testQueueConfig()
	cfg.Retries = 0
	q := NewQueued(inner, "test", "flow", "r", cfg)
	defer q.Close()
	for i := 0; i < 3; i++ {
		_ = q.Send(msg("fail"))
	}
	waitUntil(t, func() bool { return q.breakerOpen() })
	calls := inner.calls.Load()
	for i := 0; i < 5; i++ {
		_ = q.Send(msg("fast-fail"))
	}
	waitUntil(t, func() bool { return q.pending.Load() == 0 })
	if inner.calls.Load() != calls {
		t.Fatalf("destination called while the breaker was open: %d -> %d", calls, inner.calls.Load())
	}
	inner.fail.Store(0)
	time.Sleep(cfg.BreakerCooldown + 20*time.Millisecond)
	_ = q.Send(msg("probe"))
	waitUntil(t, func() bool { return len(inner.received()) == 1 })
	if q.breakerOpen() {
		t.Fatal("breaker still open after a successful probe")
	}
}

// Retry and breaker against a real HTTP server: an HTTP sink behind the
// queue recovers from 503s and delivers.
func TestQueuedHTTPSinkRecoversFromServerErrors(t *testing.T) {
	var hits atomic.Int64
	var delivered atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		delivered.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	s, err := newHTTP(spec.SinkCfg{Type: "http", URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueued(s, "http", "flow", "egress-sink-0", testQueueConfig())
	defer q.Close()
	if err := q.Send(msg(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := q.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 || delivered.Load() != 1 {
		t.Fatalf("hits %d delivered %d", hits.Load(), delivered.Load())
	}
}

// Loki batches: many queued messages arrive as one push with one value each.
func TestQueuedLokiBatches(t *testing.T) {
	var pushes, values atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got lokiPush
		if err := decodeJSON(r, &got); err != nil {
			t.Error(err)
		}
		pushes.Add(1)
		for _, s := range got.Streams {
			values.Add(int64(len(s.Values)))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	s, err := newLoki(spec.SinkCfg{URL: server.URL}, "flow")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testQueueConfig()
	cfg.Depth = 1000
	cfg.BatchMax = 50
	cfg.BatchInterval = 50 * time.Millisecond
	q := NewQueued(s, "loki", "flow", "egress-sink-0", cfg)
	defer q.Close()
	for i := 0; i < 200; i++ {
		if err := q.Send(msg("line")); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := q.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if values.Load() != 200 || pushes.Load() > 20 {
		t.Fatalf("%d values in %d pushes; want 200 values in a few batches", values.Load(), pushes.Load())
	}
}

func TestRetryableClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("plain"), false},
		{NewStatusError(400, nil), false},
		{NewStatusError(429, nil), true},
		{NewStatusError(503, nil), true},
		{transientError{errors.New("x")}, true},
		{&timeoutErr{}, true},
	} {
		if got := Retryable(tc.err); got != tc.want {
			t.Errorf("Retryable(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

type timeoutErr struct{}

func (*timeoutErr) Error() string   { return "timeout" }
func (*timeoutErr) Timeout() bool   { return true }
func (*timeoutErr) Temporary() bool { return true }

// Queued wraps only HTTP-family sink types.
func TestQueuedWrapsOnlyQueuedTypes(t *testing.T) {
	inner := &gateSink{}
	if _, ok := Queued(inner, "stdout", "f", "r").(*QueuedSink); ok {
		t.Fatal("stdout should not be queued")
	}
	wrapped := Queued(inner, "http", "f", "r")
	q, ok := wrapped.(*QueuedSink)
	if !ok {
		t.Fatal("http should be queued")
	}
	q.Close()
}

func decodeJSON(r *http.Request, into any) error { return json.NewDecoder(r.Body).Decode(into) }

// With a spool configured, a full memory queue overflows to disk instead of
// dropping, and the worker drains the spool once the queue is idle.
func TestQueuedOverflowsToSpoolAndDrains(t *testing.T) {
	inner := &gateSink{gate: make(chan struct{})}
	cfg := testQueueConfig()
	cfg.Policy = config.DropNewest
	cfg.SpoolDir = t.TempDir()
	cfg.SpoolMaxBytes = 8 << 20
	q := NewQueued(inner, "test", "flow", "egress-sink-0", cfg)
	defer q.Close()
	_ = q.Send(msg("a"))
	waitUntil(t, func() bool { return inner.calls.Load() == 1 })
	for i := 1; i < 12; i++ {
		if err := q.Send(msg(string(rune('a' + i)))); err != nil {
			t.Fatalf("send %d: %v (should have spooled, not dropped)", i, err)
		}
	}
	if q.spool.Pending() != 7 { // 1 in flight + 4 queued + 7 spooled
		t.Fatalf("%d spooled, want 7", q.spool.Pending())
	}
	close(inner.gate)
	waitUntil(t, func() bool { return len(inner.received()) == 12 })
	got := inner.received()
	if got[0] != "a" || got[4] != "e" || got[5] != "f" || got[11] != "l" {
		t.Fatalf("spooled messages lost or reordered: %v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := q.Flush(ctx); err != nil || q.spool.Pending() != 0 {
		t.Fatalf("flush: %v, spooled %d", err, q.spool.Pending())
	}
}

// Spooled messages survive a restart of the sink.
func TestSpoolSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := testQueueConfig()
	cfg.SpoolDir = dir
	cfg.SpoolMaxBytes = 8 << 20
	held := &gateSink{gate: make(chan struct{})}
	q := NewQueued(held, "test", "flow", "egress-sink-0", cfg)
	_ = q.Send(msg("x"))
	waitUntil(t, func() bool { return held.calls.Load() == 1 })
	for i := 0; i < 6; i++ {
		_ = q.Send(msg("durable"))
	}
	spooled := q.spool.Pending()
	if spooled == 0 {
		t.Fatal("nothing spooled")
	}
	close(held.gate)
	q.Close()

	inner := &gateSink{}
	q2 := NewQueued(inner, "test", "flow", "egress-sink-0", cfg)
	defer q2.Close()
	waitUntil(t, func() bool { return len(inner.received()) == spooled })
}
