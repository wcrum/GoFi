// Package config holds GoFi's typed runtime tunables.
//
// Every knob is loaded from the environment on top of a preset selected by
// GOFI_PROFILE (edge | default | large), validated once, and exposed as plain
// typed fields so the data plane never parses environment variables itself.
//
// Packages that need a limit call config.Get().Limits (process-wide, loaded
// once, safe for concurrent use) or accept a Limits value from their caller.
// main calls config.Load() + config.Apply() before anything else starts so
// GOMEMLIMIT / GOMAXPROCS and the log level are in place early.
//
// The authoritative list of variables, defaults and effects lives in the
// `fields` table in fields.go; docs/configuration.md is generated from it and
// a test keeps the two in sync.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Profile names a preset of defaults sized for a class of host.
type Profile string

const (
	// ProfileEdge targets 1-2 cores and 256 MB-1 GB RAM (ARM SBCs, vehicle
	// gateways, sensor hubs): small buffers, few workers, drop-oldest sinks.
	ProfileEdge Profile = "edge"
	// ProfileDefault is a normal server or laptop: today's historical defaults.
	ProfileDefault Profile = "default"
	// ProfileLarge targets many cores and tens of GB: deep queues, more
	// workers, large frames.
	ProfileLarge Profile = "large"
)

// DropPolicy says what a sink queue does when it is full.
type DropPolicy string

const (
	// DropBlock applies backpressure: the producer waits for room.
	DropBlock DropPolicy = "block"
	// DropNewest rejects the incoming message and keeps the queue intact.
	DropNewest DropPolicy = "drop-newest"
	// DropOldest evicts the oldest queued message to make room.
	DropOldest DropPolicy = "drop-oldest"
)

// Limits are the data-plane tunables. Zero is never a valid value after
// Load: every field is populated from the profile preset.
type Limits struct {
	// MaxFrameBytes is the upper bound of a single frame produced by any
	// framer (line, delimited, length-prefix, raw, python). Oversized frames
	// are rejected and the connection is dropped.
	MaxFrameBytes int
	// FramerInitialBuffer is the initial size of each per-connection read
	// buffer. It grows on demand up to MaxFrameBytes.
	FramerInitialBuffer int
	// MaxConnectionsPerListener caps concurrent TCP/TLS connections per flow;
	// excess accepts are closed immediately. 0 is rejected by Validate.
	MaxConnectionsPerListener int
	// ConnIdleTimeout closes a connection that has not delivered a byte for
	// this long. 0 disables the idle timer.
	ConnIdleTimeout time.Duration
	// UDPReadBuffer is the datagram read buffer (bytes) and the SO_RCVBUF hint.
	UDPReadBuffer int
	// UDPWorkers is the number of goroutines decoding datagrams per flow.
	UDPWorkers int
	// SourceWorkers is the number of goroutines processing pulled messages
	// for sources that do not acknowledge after processing.
	SourceWorkers int
	// PipelineWorkers is the number of goroutines running stages per flow.
	PipelineWorkers int
	// SinkQueueDepth is the number of messages buffered in memory per sink.
	SinkQueueDepth int
	// SinkBatchMax is the largest batch a sink flushes at once.
	SinkBatchMax int
	// SinkBatchBytes caps the payload bytes per batched request (Loki, HTTP).
	SinkBatchBytes int
	// SinkBatchInterval is how long a partial batch waits for more messages.
	SinkBatchInterval time.Duration
	// SinkDropPolicy picks the behaviour of a full sink queue.
	SinkDropPolicy DropPolicy
	// SinkRetries is how many times a retryable delivery failure (network
	// error, HTTP 429/5xx) is retried before the batch is dropped.
	SinkRetries int
	// SinkRetryInitialBackoff is the first delay after a sink delivery fails.
	SinkRetryInitialBackoff time.Duration
	// SinkRetryMaxBackoff caps the exponential backoff between sink retries.
	SinkRetryMaxBackoff time.Duration
	// SinkBreakerFailures consecutive failed deliveries open a sink's circuit
	// breaker for SinkBreakerCooldown; 0 disables the breaker.
	SinkBreakerFailures int
	// SinkBreakerCooldown is how long an open breaker fails fast before one
	// delivery probes the destination again.
	SinkBreakerCooldown time.Duration
	// HTTPMaxIdleConns is the per-host idle connection pool of HTTP sinks.
	HTTPMaxIdleConns int
	// MaxDecodedBytesPerMessage bounds the sum of all decoder output (decode
	// step plus parse stages) for one message; past it the message is dropped.
	MaxDecodedBytesPerMessage int
	// SinkSpoolDir, when set, enables the on-disk spill buffer behind sink
	// queues so short outages do not lose data. Empty disables spooling.
	SinkSpoolDir string
	// SinkSpoolMaxBytes bounds the on-disk spool per sink.
	SinkSpoolMaxBytes int64
	// TapBufferPerSubscriber is the number of tap events buffered for each
	// live-traffic subscriber before events are dropped for that subscriber.
	TapBufferPerSubscriber int
	// TapRawTruncate truncates raw payload bytes carried in tap events.
	TapRawTruncate int
	// DedupMaxKeys caps the number of keys a dedup stage remembers.
	DedupMaxKeys int
	// RateLimitMaxKeys caps the number of keys a rate-limit stage tracks.
	RateLimitMaxKeys int
	// PythonWorkerPool is the number of Python worker processes per flow
	// that uses Python stages or framing.
	PythonWorkerPool int
	// HistoryLimit is how many revisions to keep per resource.
	HistoryLimit int
	// HistoryDeletedRetention is how long history of deleted resources stays.
	HistoryDeletedRetention time.Duration
}

// Observability tunes logging, metrics and the live tap.
type Observability struct {
	// LogLevel is the slog level for the process.
	LogLevel slog.Level
	// LogFormat is "json" or "text".
	LogFormat string
	// MetricsEnabled turns the Prometheus-style metrics endpoint on or off.
	MetricsEnabled bool
	// MetricsInterval is how often per-flow metrics are aggregated/pushed.
	MetricsInterval time.Duration
	// TapEnabled turns the live traffic tap (ConnectRPC stream) on or off.
	TapEnabled bool
	// SampleBufferSize is the number of recent messages kept per flow for
	// the UI sample/preview.
	SampleBufferSize int
	// BulletinRetention is how many bulletins (warnings) are kept per flow.
	BulletinRetention int
	// WatchMaxStreams caps open portal live streams (traffic + stats) per
	// controller; WatchMaxStreamsPerClient caps them per client IP.
	WatchMaxStreams          int
	WatchMaxStreamsPerClient int
	// WatchQueue is the per-browser event queue; a browser further behind
	// loses events (reported on its stream's dropped field).
	WatchQueue int
	// HubIdleGrace is how long a flow's controller-to-runtime stream stays
	// open after its last viewer leaves.
	HubIdleGrace time.Duration
	// HubResync is how often a watched flow's runtime target is re-resolved
	// while no Pod watcher pushes changes.
	HubResync time.Duration
}

// Memory controls the Go runtime's memory and CPU sizing.
type Memory struct {
	// LimitBytes is the container/host memory budget. 0 means "discover
	// from cgroup", and if that fails no GOMEMLIMIT is applied.
	LimitBytes int64
	// GOMEMLIMITFraction is the share of LimitBytes handed to the Go GC as
	// GOMEMLIMIT; the rest is headroom for non-Go memory (Python workers,
	// page cache, stacks).
	GOMEMLIMITFraction float64
	// GOMAXPROCS overrides the scheduler's P count. 0 defers to the runtime
	// (which on Go >= 1.25 already honours cgroup CPU quota).
	GOMAXPROCS int
}

// Config is the fully resolved runtime configuration.
type Config struct {
	Profile       Profile
	Limits        Limits
	Observability Observability
	Memory        Memory
}

// Preset returns the defaults for a profile before any environment override.
func Preset(p Profile) Config {
	c := Config{
		Profile: ProfileDefault,
		Limits: Limits{
			MaxFrameBytes:             1 << 20,
			FramerInitialBuffer:       64 << 10,
			MaxConnectionsPerListener: 1024,
			ConnIdleTimeout:           0,
			UDPReadBuffer:             4 << 20,
			UDPWorkers:                1,
			SourceWorkers:             1,
			PipelineWorkers:           4,
			SinkQueueDepth:            4096,
			SinkBatchMax:              256,
			SinkBatchBytes:            1 << 20,
			SinkBatchInterval:         time.Second,
			SinkDropPolicy:            DropBlock,
			SinkRetries:               3,
			SinkRetryInitialBackoff:   200 * time.Millisecond,
			SinkRetryMaxBackoff:       30 * time.Second,
			SinkBreakerFailures:       5,
			SinkBreakerCooldown:       10 * time.Second,
			HTTPMaxIdleConns:          64,
			MaxDecodedBytesPerMessage: 8 << 20,
			SinkSpoolDir:              "",
			SinkSpoolMaxBytes:         256 << 20,
			TapBufferPerSubscriber:    1024,
			TapRawTruncate:            4096,
			DedupMaxKeys:              100_000,
			RateLimitMaxKeys:          100_000,
			PythonWorkerPool:          2,
			HistoryLimit:              50,
			HistoryDeletedRetention:   7 * 24 * time.Hour,
		},
		Observability: Observability{
			LogLevel:                 slog.LevelInfo,
			LogFormat:                "json",
			MetricsEnabled:           true,
			MetricsInterval:          5 * time.Second,
			TapEnabled:               true,
			SampleBufferSize:         100,
			BulletinRetention:        100,
			WatchMaxStreams:          256,
			WatchMaxStreamsPerClient: 16,
			WatchQueue:               256,
			HubIdleGrace:             10 * time.Second,
			HubResync:                15 * time.Second,
		},
		Memory: Memory{
			LimitBytes:         0,
			GOMEMLIMITFraction: 0.8,
			GOMAXPROCS:         0,
		},
	}
	switch p {
	case ProfileEdge:
		c.Profile = ProfileEdge
		c.Limits.MaxFrameBytes = 256 << 10
		c.Limits.FramerInitialBuffer = 4 << 10
		c.Limits.MaxConnectionsPerListener = 512
		c.Limits.ConnIdleTimeout = 2 * time.Minute
		c.Limits.UDPReadBuffer = 256 << 10
		c.Limits.UDPWorkers = 1
		c.Limits.SourceWorkers = 1
		c.Limits.PipelineWorkers = 1
		c.Limits.SinkQueueDepth = 512
		c.Limits.SinkBatchMax = 64
		c.Limits.SinkBatchBytes = 256 << 10
		c.Limits.SinkBatchInterval = 2 * time.Second
		c.Limits.SinkDropPolicy = DropOldest
		c.Limits.SinkRetries = 5
		c.Limits.SinkRetryMaxBackoff = 2 * time.Minute
		c.Limits.SinkBreakerCooldown = 30 * time.Second
		c.Limits.HTTPMaxIdleConns = 8
		c.Limits.MaxDecodedBytesPerMessage = 2 << 20
		c.Limits.SinkSpoolMaxBytes = 32 << 20
		c.Limits.TapBufferPerSubscriber = 128
		c.Limits.TapRawTruncate = 512
		c.Limits.DedupMaxKeys = 10_000
		c.Limits.RateLimitMaxKeys = 10_000
		c.Limits.PythonWorkerPool = 1
		c.Limits.HistoryLimit = 10
		c.Limits.HistoryDeletedRetention = 24 * time.Hour
		c.Observability.MetricsInterval = 15 * time.Second
		c.Observability.SampleBufferSize = 20
		c.Observability.BulletinRetention = 25
		c.Observability.WatchMaxStreams = 32
		c.Observability.WatchMaxStreamsPerClient = 8
		c.Observability.WatchQueue = 64
		c.Memory.GOMEMLIMITFraction = 0.7
	case ProfileLarge:
		c.Profile = ProfileLarge
		c.Limits.MaxFrameBytes = 16 << 20
		c.Limits.FramerInitialBuffer = 256 << 10
		c.Limits.MaxConnectionsPerListener = 65536
		c.Limits.ConnIdleTimeout = 15 * time.Minute
		c.Limits.UDPReadBuffer = 16 << 20
		c.Limits.UDPWorkers = 16
		c.Limits.SourceWorkers = 8
		c.Limits.PipelineWorkers = 16
		c.Limits.SinkQueueDepth = 65536
		c.Limits.SinkBatchMax = 2048
		c.Limits.SinkBatchBytes = 4 << 20
		c.Limits.SinkBatchInterval = 500 * time.Millisecond
		c.Limits.SinkBreakerFailures = 10
		c.Limits.HTTPMaxIdleConns = 256
		c.Limits.MaxDecodedBytesPerMessage = 64 << 20
		c.Limits.SinkSpoolMaxBytes = 4 << 30
		c.Limits.TapBufferPerSubscriber = 8192
		c.Limits.TapRawTruncate = 65536
		c.Limits.DedupMaxKeys = 5_000_000
		c.Limits.RateLimitMaxKeys = 5_000_000
		c.Limits.PythonWorkerPool = 8
		c.Limits.HistoryLimit = 500
		c.Limits.HistoryDeletedRetention = 30 * 24 * time.Hour
		c.Observability.MetricsInterval = 5 * time.Second
		c.Observability.SampleBufferSize = 500
		c.Observability.BulletinRetention = 500
		c.Observability.WatchMaxStreams = 1024
		c.Observability.WatchMaxStreamsPerClient = 32
		c.Observability.WatchQueue = 1024
		c.Memory.GOMEMLIMITFraction = 0.9
	}
	return c
}

// ParseProfile accepts "edge", "default", "large" (case-insensitive); the
// empty string means default.
func ParseProfile(s string) (Profile, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "default":
		return ProfileDefault, nil
	case "edge", "embedded", "small":
		return ProfileEdge, nil
	case "large", "big", "server":
		return ProfileLarge, nil
	}
	return "", fmt.Errorf("GOFI_PROFILE: unknown profile %q (want edge|default|large)", s)
}

// Load resolves the configuration from the process environment: preset by
// GOFI_PROFILE, then every GOFI_* override, then Validate. It never panics
// and returns the first problem found with the variable name in the message.
func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom is Load with an injectable environment, for tests.
func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	raw, _ := lookup("GOFI_PROFILE")
	profile, err := ParseProfile(raw)
	if err != nil {
		return Config{}, err
	}
	cfg := Preset(profile)
	var errs []string
	for _, f := range fields {
		v, ok := lookup(f.Env)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		if err := f.set(&cfg, strings.TrimSpace(v)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", f.Env, err))
		}
	}
	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config: %s", strings.Join(errs, "; "))
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate rejects values that would make the data plane misbehave.
func (c Config) Validate() error {
	l := c.Limits
	var errs []string
	add := func(cond bool, msg string, args ...any) {
		if cond {
			errs = append(errs, fmt.Sprintf(msg, args...))
		}
	}
	add(l.MaxFrameBytes < 64, "GOFI_MAX_FRAME_BYTES must be >= 64 (got %d)", l.MaxFrameBytes)
	add(l.FramerInitialBuffer < 64, "GOFI_FRAMER_INITIAL_BUFFER must be >= 64 (got %d)", l.FramerInitialBuffer)
	add(l.FramerInitialBuffer > l.MaxFrameBytes, "GOFI_FRAMER_INITIAL_BUFFER (%d) must not exceed GOFI_MAX_FRAME_BYTES (%d)", l.FramerInitialBuffer, l.MaxFrameBytes)
	add(l.MaxConnectionsPerListener < 1 || l.MaxConnectionsPerListener > 1<<20, "GOFI_MAX_CONNECTIONS must be in [1, 1048576]")
	add(l.ConnIdleTimeout < 0, "GOFI_IDLE_TIMEOUT must be >= 0")
	add(l.UDPReadBuffer < 64<<10 || l.UDPReadBuffer > 1<<30, "GOFI_UDP_READ_BUFFER must be in [64Ki, 1Gi]")
	add(l.UDPWorkers < 1 || l.UDPWorkers > 256, "GOFI_UDP_WORKERS must be in [1, 256]")
	add(l.SourceWorkers < 1 || l.SourceWorkers > 256, "GOFI_SOURCE_WORKERS must be in [1, 256]")
	add(l.PipelineWorkers < 1 || l.PipelineWorkers > 256, "GOFI_PIPELINE_WORKERS must be in [1, 256]")
	add(l.SinkQueueDepth < 1, "GOFI_SINK_QUEUE_DEPTH must be >= 1")
	add(l.SinkBatchMax < 1, "GOFI_SINK_BATCH_MAX must be >= 1")
	add(l.SinkBatchMax > l.SinkQueueDepth, "GOFI_SINK_BATCH_MAX (%d) must not exceed GOFI_SINK_QUEUE_DEPTH (%d)", l.SinkBatchMax, l.SinkQueueDepth)
	switch l.SinkDropPolicy {
	case DropBlock, DropNewest, DropOldest:
	default:
		add(true, "GOFI_SINK_DROP_POLICY must be block|drop-newest|drop-oldest (got %q)", l.SinkDropPolicy)
	}
	add(l.SinkBatchBytes < 1<<10 || l.SinkBatchBytes > 64<<20, "GOFI_SINK_BATCH_BYTES must be in [1Ki, 64Mi]")
	add(l.SinkBatchInterval < 0, "GOFI_SINK_BATCH_INTERVAL must be >= 0")
	add(l.SinkRetries < 0 || l.SinkRetries > 100, "GOFI_SINK_RETRIES must be in [0, 100]")
	add(l.SinkRetryInitialBackoff <= 0, "GOFI_SINK_RETRY_INITIAL_BACKOFF must be > 0")
	add(l.SinkRetryMaxBackoff < l.SinkRetryInitialBackoff, "GOFI_SINK_RETRY_MAX_BACKOFF must be >= GOFI_SINK_RETRY_INITIAL_BACKOFF")
	add(l.SinkBreakerFailures < 0 || l.SinkBreakerFailures > 1<<20, "GOFI_SINK_BREAKER_FAILURES must be in [0, 1048576]")
	add(l.SinkBreakerCooldown < 0, "GOFI_SINK_BREAKER_COOLDOWN must be >= 0")
	add(l.HTTPMaxIdleConns < 1 || l.HTTPMaxIdleConns > 10_000, "GOFI_HTTP_MAX_IDLE_CONNS must be in [1, 10000]")
	add(l.MaxDecodedBytesPerMessage < 1<<10, "GOFI_MAX_DECODED_BYTES_PER_MESSAGE must be >= 1Ki")
	add(l.SinkSpoolDir != "" && l.SinkSpoolMaxBytes < 1<<20, "GOFI_SINK_SPOOL_MAX_BYTES must be >= 1Mi when spooling is enabled")
	add(l.TapBufferPerSubscriber < 1, "GOFI_TAP_BUFFER_PER_SUBSCRIBER must be >= 1")
	add(l.TapRawTruncate < 0, "GOFI_TAP_RAW_TRUNCATE must be >= 0")
	add(l.DedupMaxKeys < 1, "GOFI_DEDUP_MAX_KEYS must be >= 1")
	add(l.RateLimitMaxKeys < 1, "GOFI_RATELIMIT_MAX_KEYS must be >= 1")
	add(l.PythonWorkerPool < 1, "GOFI_PYTHON_WORKER_POOL must be >= 1")
	add(l.HistoryLimit < 1, "GOFI_HISTORY_LIMIT must be >= 1")
	add(l.HistoryDeletedRetention < 0, "GOFI_HISTORY_DELETED_RETENTION must be >= 0")

	o := c.Observability
	add(o.LogFormat != "json" && o.LogFormat != "text", "GOFI_LOG_FORMAT must be json|text (got %q)", o.LogFormat)
	add(o.MetricsInterval < time.Second, "GOFI_METRICS_INTERVAL must be >= 1s")
	add(o.SampleBufferSize < 1, "GOFI_SAMPLE_BUFFER must be >= 1")
	add(o.BulletinRetention < 1, "GOFI_BULLETIN_RETENTION must be >= 1")
	add(o.WatchMaxStreams < 1, "GOFI_WATCH_MAX_STREAMS must be >= 1")
	add(o.WatchMaxStreamsPerClient < 1, "GOFI_WATCH_MAX_STREAMS_PER_CLIENT must be >= 1")
	add(o.WatchMaxStreamsPerClient > o.WatchMaxStreams, "GOFI_WATCH_MAX_STREAMS_PER_CLIENT (%d) must not exceed GOFI_WATCH_MAX_STREAMS (%d)", o.WatchMaxStreamsPerClient, o.WatchMaxStreams)
	add(o.WatchQueue < 1, "GOFI_WATCH_QUEUE must be >= 1")
	add(o.HubIdleGrace < 0, "GOFI_HUB_IDLE_GRACE must be >= 0")
	add(o.HubResync <= 0, "GOFI_HUB_RESYNC must be > 0")

	m := c.Memory
	add(m.LimitBytes < 0, "GOFI_MEMORY_LIMIT must be >= 0")
	add(m.LimitBytes > 0 && m.LimitBytes < 16<<20, "GOFI_MEMORY_LIMIT must be >= 16Mi")
	add(m.GOMEMLIMITFraction <= 0 || m.GOMEMLIMITFraction > 1, "GOFI_GOMEMLIMIT_FRACTION must be in (0, 1]")
	add(m.GOMAXPROCS < 0, "GOFI_GOMAXPROCS must be >= 0")

	if len(errs) > 0 {
		return fmt.Errorf("config: %s", strings.Join(errs, "; "))
	}
	return nil
}

var (
	globalMu  sync.RWMutex
	globalCfg *Config
)

// Get returns the process-wide configuration. The first call loads it from
// the environment; if loading fails the error is logged and the default
// preset is used so the data plane always has sane limits. main should call
// Load/Set explicitly to surface errors instead of relying on this fallback.
func Get() Config {
	globalMu.RLock()
	c := globalCfg
	globalMu.RUnlock()
	if c != nil {
		return *c
	}
	cfg, err := Load()
	if err != nil {
		slog.Error("config: invalid environment, using default preset", "error", err)
		cfg = Preset(ProfileDefault)
	}
	Set(cfg)
	return cfg
}

// Set installs cfg as the process-wide configuration returned by Get.
func Set(cfg Config) {
	globalMu.Lock()
	defer globalMu.Unlock()
	c := cfg
	globalCfg = &c
}

// Reset clears the process-wide configuration so the next Get reloads. Tests only.
func Reset() {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalCfg = nil
}
