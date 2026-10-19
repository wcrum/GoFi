package sink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wcrum/gofi/config"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spool"
)

// Batcher is a Sink that can deliver several messages in one request. The
// queue worker hands it batches bounded by QueueConfig.BatchMax/BatchBytes/
// BatchInterval; a sink without it receives messages one at a time.
type Batcher interface {
	SendBatch(msgs []*message.Message) error
}

// Async is implemented by sinks whose Send only enqueues: delivery happens
// later and the sink records each delivery's outcome itself (route health,
// gofi_sink_dispatch_*). The pipeline then treats Send's error as an enqueue
// failure (queue full) rather than a destination failure.
type Async interface {
	Async() bool
}

// Errors Send returns from a queued sink.
var (
	ErrQueueFull   = errors.New("sink queue full; message dropped")
	ErrSinkClosed  = errors.New("sink closed")
	ErrBreakerOpen = errors.New("circuit breaker open; destination failing")
)

// QueuedTypes are the sink types Queued wraps: HTTP-family destinations whose
// per-request latency would otherwise stall the pipeline. Loki also batches.
// NATS buffers in its client, network sinks write to a local socket, stdout
// is buffered, and Python sinks are bounded by their worker pool.
var QueuedTypes = map[string]bool{"loki": true, "http": true, "aster": true, "zenoh": true}

// Queued wraps s in a bounded asynchronous queue when its type is in
// QueuedTypes and the configured depth is positive; otherwise it returns s
// unchanged. flow and route name the pipeline destination for route health
// and metrics.
func Queued(s Sink, sinkType, flow, route string) Sink {
	if !QueuedTypes[sinkType] {
		return s
	}
	cfg := processQueueConfig()
	if cfg.Depth <= 0 {
		return s
	}
	return NewQueued(s, sinkType, flow, route, cfg)
}

// NewQueued wraps s in an asynchronous queue with cfg, whatever its type.
func NewQueued(s Sink, sinkType, flow, route string, cfg QueueConfig) *QueuedSink {
	if cfg.BatchMax < 1 {
		cfg.BatchMax = 1
	}
	if cfg.Depth < 1 {
		cfg.Depth = 1
	}
	if cfg.RetryInitial <= 0 {
		cfg.RetryInitial = 200 * time.Millisecond
	}
	if cfg.RetryMax < cfg.RetryInitial {
		cfg.RetryMax = cfg.RetryInitial
	}
	q := &QueuedSink{
		inner:    s,
		kind:     sinkType,
		flow:     flow,
		routeID:  route,
		cfg:      cfg,
		queue:    make(chan *message.Message, cfg.Depth),
		done:     make(chan struct{}),
		stopped:  make(chan struct{}),
		metrics:  observability.NewSinkQueue(flow, sinkType),
		route:    observability.NewRoute(flow, route),
		dispatch: observability.NewFlow(flow, nil, []string{sinkType}),
	}
	q.batcher, _ = s.(Batcher)
	q.spool = openSpool(cfg.SpoolDir, flow, route, cfg.SpoolMaxBytes, cfg.Policy == config.DropOldest)
	go q.work()
	return q
}

// QueuedSink delivers through a single worker goroutine from a bounded
// queue. Send never waits on the destination: it enqueues, and under a full
// queue applies the drop policy. The worker batches (for a Batcher), retries
// transient failures with exponential backoff and jitter, and opens a circuit
// breaker after consecutive failures so a dead destination costs nothing
// but queue drops until it recovers.
type QueuedSink struct {
	inner    Sink
	batcher  Batcher
	kind     string
	flow     string
	routeID  string
	cfg      QueueConfig
	queue    chan *message.Message
	done     chan struct{} // closed by Close: the worker stops
	stopped  chan struct{} // closed when the worker returned
	closed   atomic.Bool
	pending  atomic.Int64 // enqueued and not yet delivered or dropped
	metrics  *observability.SinkQueue
	route    *observability.Route
	dispatch *observability.Flow

	// Breaker state, worker-only except for the open timestamp.
	consecutiveFailures int
	openUntil           atomic.Int64 // unix nanoseconds; 0 = closed
	dropLog             time.Time
	closeOnce           sync.Once

	// spool is the durable overflow store, nil when not configured.
	spool    *spool.Queue
	spoolLog time.Time
}

// Async marks Send as enqueue-only.
func (q *QueuedSink) Async() bool { return true }

// Send enqueues msg. It returns ErrQueueFull when the queue is full under a
// drop policy, ErrSinkClosed after Close, and otherwise nil: the delivery
// outcome is recorded by the worker.
func (q *QueuedSink) Send(msg *message.Message) error {
	if q.closed.Load() {
		return ErrSinkClosed
	}
	q.pending.Add(1)
	select {
	case q.queue <- msg:
		q.metrics.Depth.Set(float64(len(q.queue)))
		return nil
	default:
	}
	// The memory queue is full: durable overflow first, then the policy.
	if q.spoolMessage(msg) {
		q.pending.Add(-1)
		return nil
	}
	switch q.cfg.Policy {
	case config.DropNewest:
		q.pending.Add(-1)
		q.dropped(1)
		return ErrQueueFull
	case config.DropOldest:
		// Evict one, then enqueue. Under contention the eviction may free a
		// slot another sender takes; fall back to dropping the newest.
		select {
		case <-q.queue:
			q.pending.Add(-1)
			q.dropped(1)
		default:
		}
		select {
		case q.queue <- msg:
			return nil
		default:
			q.pending.Add(-1)
			q.dropped(1)
			return ErrQueueFull
		}
	default: // block
		q.metrics.Blocked.Inc()
		select {
		case q.queue <- msg:
			return nil
		case <-q.done:
			q.pending.Add(-1)
			return ErrSinkClosed
		}
	}
}

// dropLogInterval rate-limits the queue-drop warning; closeWait bounds how
// long Close waits for a delivery in progress.
const (
	dropLogInterval = 10 * time.Second
	closeWait       = 10 * time.Second
)

func (q *QueuedSink) dropped(n int) {
	q.metrics.Dropped.Add(float64(n))
	// A warning every interval, not per message; the counter has the rate.
	if time.Since(q.dropLog) >= dropLogInterval {
		q.dropLog = time.Now()
		slog.Warn("sink queue full; dropping messages", "flow", q.flow, "sink", q.kind, "policy", q.cfg.Policy, "depth", q.cfg.Depth)
		observability.Report(q.flow, observability.SeverityWarning, observability.RouteComponent(q.routeID), fmt.Sprintf("%s sink queue full (%d); messages are being dropped", q.kind, q.cfg.Depth))
	}
}

// work is the single delivery goroutine.
func (q *QueuedSink) work() {
	defer close(q.stopped)
	var poll <-chan time.Time
	if q.spool != nil {
		ticker := time.NewTicker(spoolPollInterval)
		defer ticker.Stop()
		poll = ticker.C
	}
	for {
		var first *message.Message
		select {
		case first = <-q.queue:
		case <-poll:
			// Idle: drain spooled overflow, oldest first, while nothing new
			// is waiting in memory.
			for q.drainSpool() && len(q.queue) == 0 {
				select {
				case <-q.done:
					return
				default:
				}
			}
			continue
		case <-q.done:
			return
		}
		batch := q.collect(first)
		q.metrics.Depth.Set(float64(len(q.queue)))
		q.deliver(batch)
		q.pending.Add(-int64(len(batch)))
	}
}

// collect gathers a batch after first: for a Batcher, up to BatchMax
// messages or BatchBytes bytes, waiting at most BatchInterval for more;
// otherwise just first.
func (q *QueuedSink) collect(first *message.Message) []*message.Message {
	batch := []*message.Message{first}
	if q.batcher == nil || q.cfg.BatchMax <= 1 {
		return batch
	}
	size := approxSize(first)
	var timer *time.Timer
	var wait <-chan time.Time
	if q.cfg.BatchInterval > 0 {
		timer = time.NewTimer(q.cfg.BatchInterval)
		defer timer.Stop()
		wait = timer.C
	}
	for len(batch) < q.cfg.BatchMax && (q.cfg.BatchBytes <= 0 || size < q.cfg.BatchBytes) {
		select {
		case m := <-q.queue:
			batch = append(batch, m)
			size += approxSize(m)
		case <-wait:
			return batch
		case <-q.done:
			return batch
		}
		if wait == nil && len(q.queue) == 0 {
			return batch // no interval: take what is already queued
		}
	}
	return batch
}

func approxSize(m *message.Message) int {
	if m.Payload != nil {
		return len(m.Payload)
	}
	return len(m.Raw) + 64*len(m.Fields)
}

// deliver sends one batch with retries and the breaker, then records the
// outcome once per message.
func (q *QueuedSink) deliver(batch []*message.Message) {
	started := time.Now()
	var err error
	if q.breakerOpen() {
		if q.spool != nil {
			// Keep the batch for when the destination recovers.
			kept := 0
			for _, m := range batch {
				if q.spoolMessage(m) {
					kept++
				}
			}
			if kept == len(batch) {
				return
			}
		}
		err = ErrBreakerOpen
		q.metrics.BreakerRejected.Add(float64(len(batch)))
	} else {
		err = q.attempt(batch)
		q.observe(err)
	}
	q.record(len(batch), time.Since(started), err)
}

// attempt tries the batch up to 1+Retries times.
func (q *QueuedSink) attempt(batch []*message.Message) error {
	var err error
	for try := 0; ; try++ {
		if q.batcher != nil {
			err = q.batcher.SendBatch(batch)
		} else {
			err = q.inner.Send(batch[0])
		}
		if err == nil || try >= q.cfg.Retries || !Retryable(err) {
			return err
		}
		q.metrics.Retries.Inc()
		delay := q.backoff(try)
		slog.Debug("sink send failed; retrying", "flow", q.flow, "sink", q.kind, "attempt", try+1, "retryIn", delay, "error", err)
		select {
		case <-time.After(delay):
		case <-q.done:
			return err
		}
	}
}

// backoff is RetryInitial * 2^try, capped at RetryMax, with up to 25% jitter.
func (q *QueuedSink) backoff(try int) time.Duration {
	d := q.cfg.RetryInitial
	for i := 0; i < try && d < q.cfg.RetryMax; i++ {
		d *= 2
	}
	if d > q.cfg.RetryMax {
		d = q.cfg.RetryMax
	}
	jitter := time.Duration(rand.Int64N(int64(d)/4 + 1))
	return d - jitter
}

func (q *QueuedSink) breakerOpen() bool {
	until := q.openUntil.Load()
	if until == 0 {
		return false
	}
	if time.Now().UnixNano() >= until {
		// Half-open: let one delivery probe the destination.
		q.openUntil.Store(0)
		return false
	}
	return true
}

// observe updates the breaker after an attempt.
func (q *QueuedSink) observe(err error) {
	if err == nil {
		if q.consecutiveFailures > 0 || q.metricsBreakerWasOpen() {
			q.metrics.BreakerOpen.Set(0)
		}
		q.consecutiveFailures = 0
		return
	}
	q.consecutiveFailures++
	if q.cfg.BreakerFailures > 0 && q.consecutiveFailures >= q.cfg.BreakerFailures && q.cfg.BreakerCooldown > 0 {
		q.openUntil.Store(time.Now().Add(q.cfg.BreakerCooldown).UnixNano())
		q.metrics.BreakerOpen.Set(1)
		if q.consecutiveFailures == q.cfg.BreakerFailures {
			slog.Warn("sink circuit breaker opened", "flow", q.flow, "sink", q.kind, "failures", q.consecutiveFailures, "cooldown", q.cfg.BreakerCooldown, "error", err)
		}
	}
}

func (q *QueuedSink) metricsBreakerWasOpen() bool { return q.openUntil.Load() != 0 }

// record reports one batch outcome: route health once, dispatch counters per
// message, the batch duration once.
func (q *QueuedSink) record(n int, elapsed time.Duration, err error) {
	q.route.Result(err)
	for i := 0; i < n; i++ {
		q.dispatch.SinkDispatch(q.kind, elapsed, err)
	}
	if q.batcher != nil {
		q.metrics.BatchSize.Observe(float64(n))
	}
	if err != nil {
		slog.Warn("sink error", "flow", q.flow, "sink", q.kind, "messages", n, "error", err)
	}
}

// Flush waits until everything enqueued so far was delivered or dropped,
// then flushes the wrapped sink. It returns ctx's error if that comes first.
func (q *QueuedSink) Flush(ctx context.Context) error {
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for q.pending.Load() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-q.stopped:
			return ErrSinkClosed
		case <-tick.C:
		}
	}
	// Spooled overflow drains while ctx allows; what remains survives on
	// disk for the next start.
	for q.spool != nil && q.spool.Pending() > 0 && ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-q.stopped:
			return ErrSinkClosed
		case <-tick.C:
		}
	}
	return Flush(ctx, q.inner)
}

// Close stops the worker (messages still queued are abandoned; call Flush
// first to drain) and closes the wrapped sink.
func (q *QueuedSink) Close() error {
	q.closeOnce.Do(func() {
		q.closed.Store(true)
		close(q.done)
	})
	// A wrapped sink stuck in Send (its own timeouts bound this) must not
	// hold Close forever.
	select {
	case <-q.stopped:
	case <-time.After(closeWait):
		slog.Warn("sink worker did not stop in time; closing anyway", "flow", q.flow, "sink", q.kind)
	}
	if n := q.pending.Load(); n > 0 {
		q.metrics.Dropped.Add(float64(n))
		slog.Warn("sink closed with messages still queued", "flow", q.flow, "sink", q.kind, "abandoned", n)
	}
	err := q.inner.Close()
	if serr := q.closeSpool(); serr != nil && err == nil {
		err = serr
	}
	return err
}

// Depth is how many messages are queued (for tests and status).
func (q *QueuedSink) Depth() int { return len(q.queue) }
