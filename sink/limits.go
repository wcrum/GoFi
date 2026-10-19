package sink

import (
	"sync"
	"time"

	"github.com/wcrum/gofi/config"
)

// QueueConfig tunes the asynchronous delivery wrapper (Queued): the bounded
// in-memory queue, batching, retries, and the circuit breaker. Every value
// comes from the shared config package (GOFI_PROFILE presets, GOFI_SINK_*;
// docs/configuration.md); nothing here reads the environment itself.
type QueueConfig struct {
	// Depth is the queue capacity in messages; 0 disables the queue and
	// sinks send synchronously on the pipeline goroutine. GOFI_SINK_QUEUE_DEPTH.
	Depth int
	// Policy says what Send does when the queue is full: block (back-pressure
	// to the source), drop-newest (reject the incoming message), or
	// drop-oldest (evict the oldest queued message). GOFI_SINK_DROP_POLICY.
	Policy config.DropPolicy
	// BatchMax caps messages per request for sinks that batch (Loki).
	// GOFI_SINK_BATCH_MAX.
	BatchMax int
	// BatchBytes caps the payload bytes per batched request. GOFI_SINK_BATCH_BYTES.
	BatchBytes int
	// BatchInterval is how long a partial batch waits for more messages.
	// GOFI_SINK_BATCH_INTERVAL.
	BatchInterval time.Duration
	// Retries is how many times a retryable failure (network error, HTTP
	// 429/5xx) is retried before the batch is dropped. GOFI_SINK_RETRIES.
	Retries int
	// RetryInitial and RetryMax bound the exponential backoff (with jitter)
	// between retries. GOFI_SINK_RETRY_INITIAL_BACKOFF, GOFI_SINK_RETRY_MAX_BACKOFF.
	RetryInitial time.Duration
	RetryMax     time.Duration
	// BreakerFailures consecutive failed deliveries open the circuit for
	// BreakerCooldown: sends fail fast without touching the destination, then
	// one delivery probes it. 0 disables the breaker. GOFI_SINK_BREAKER_FAILURES,
	// GOFI_SINK_BREAKER_COOLDOWN.
	BreakerFailures int
	BreakerCooldown time.Duration
	// SpoolDir enables the on-disk overflow spool (see spool.go); empty
	// disables it. SpoolMaxBytes bounds it per sink. GOFI_SINK_SPOOL_DIR,
	// GOFI_SINK_SPOOL_MAX_BYTES.
	SpoolDir      string
	SpoolMaxBytes int64
}

// DefaultQueueConfig is the configuration with every knob at the default
// profile's value (config.Preset(config.ProfileDefault)).
func DefaultQueueConfig() QueueConfig {
	return queueConfigFrom(config.Preset(config.ProfileDefault).Limits)
}

// QueueConfigFromEnv reads the process configuration (config.Get()).
func QueueConfigFromEnv() QueueConfig {
	return queueConfigFrom(config.Get().Limits)
}

// queueConfigFrom maps the shared limits onto the queue knobs.
func queueConfigFrom(l config.Limits) QueueConfig {
	q := QueueConfig{
		Depth: l.SinkQueueDepth, Policy: l.SinkDropPolicy, BatchMax: l.SinkBatchMax, BatchBytes: l.SinkBatchBytes, BatchInterval: l.SinkBatchInterval,
		Retries: l.SinkRetries, RetryInitial: l.SinkRetryInitialBackoff, RetryMax: l.SinkRetryMaxBackoff,
		BreakerFailures: l.SinkBreakerFailures, BreakerCooldown: l.SinkBreakerCooldown,
		SpoolDir: l.SinkSpoolDir, SpoolMaxBytes: l.SinkSpoolMaxBytes,
	}
	if q.Policy == "" {
		q.Policy = config.DropBlock
	}
	if q.BatchMax < 1 {
		q.BatchMax = 1
	}
	if q.RetryInitial <= 0 {
		q.RetryInitial = 200 * time.Millisecond
	}
	if q.RetryMax < q.RetryInitial {
		q.RetryMax = q.RetryInitial
	}
	return q
}

// processQueueConfig is read once per process.
var processQueueConfig = sync.OnceValue(QueueConfigFromEnv)

// httpMaxIdleConns is the per-host idle connection pool size of HTTP sinks
// (GOFI_HTTP_MAX_IDLE_CONNS).
var httpMaxIdleConns = sync.OnceValue(func() int { return config.Get().Limits.HTTPMaxIdleConns })
