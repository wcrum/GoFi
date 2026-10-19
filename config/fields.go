package config

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Field describes one environment variable: how it is parsed into Config and
// how it is documented. docs/configuration.md is rendered from this table.
type Field struct {
	Env  string
	Desc string
	get  func(*Config) string
	set  func(*Config, string) error
}

// Fields returns the documented variables in display order.
func Fields() []Field { return append([]Field(nil), fields...) }

// Default renders the value this field has in the given profile preset.
func (f Field) Default(p Profile) string {
	c := Preset(p)
	return f.get(&c)
}

var fields = []Field{
	{Env: "GOFI_PROFILE", Desc: "Preset selecting every default below: edge (1-2 cores, <=1 GB), default, large.",
		get: func(c *Config) string { return string(c.Profile) },
		set: func(c *Config, v string) error { return nil }}, // handled in LoadFrom
	// Limits
	intField("GOFI_MAX_FRAME_BYTES", "Largest frame any framer accepts; oversized frames close the connection. Accepts 1Mi, 256k, 1048576.", true,
		func(c *Config) *int { return &c.Limits.MaxFrameBytes }),
	intField("GOFI_FRAMER_INITIAL_BUFFER", "Initial per-connection read buffer; grows on demand up to GOFI_MAX_FRAME_BYTES. Memory per idle connection is roughly this value.", true,
		func(c *Config) *int { return &c.Limits.FramerInitialBuffer }),
	intField("GOFI_MAX_DECODED_BYTES_PER_MESSAGE", "Total decoder output (decode step plus every parse stage) allowed for one message; past it the message is dropped, so a tiny compressed frame cannot expand without bound.", true,
		func(c *Config) *int { return &c.Limits.MaxDecodedBytesPerMessage }),
	intField("GOFI_MAX_CONNECTIONS", "Concurrent TCP connections per listen source; further accepts are closed at once (gofi_listener_connections_refused_total).", false,
		func(c *Config) *int { return &c.Limits.MaxConnectionsPerListener }),
	durField("GOFI_IDLE_TIMEOUT", "Close a TCP connection that sent nothing for this long (0 disables). Frees buffers held by dead peers on lossy links.",
		func(c *Config) *time.Duration { return &c.Limits.ConnIdleTimeout }),
	intField("GOFI_UDP_READ_BUFFER", "Datagram read buffer and SO_RCVBUF request per UDP flow.", true,
		func(c *Config) *int { return &c.Limits.UDPReadBuffer }),
	intField("GOFI_UDP_WORKERS", "Goroutines reading and processing datagrams per UDP flow; more than one processes out of order.", false,
		func(c *Config) *int { return &c.Limits.UDPWorkers }),
	intField("GOFI_SOURCE_WORKERS", "Goroutines processing pulled messages for non-acknowledging sources (NATS, MQTT, WebSocket, SSE, Redis Pub/Sub, fetch).", false,
		func(c *Config) *int { return &c.Limits.SourceWorkers }),
	intField("GOFI_PIPELINE_WORKERS", "Goroutines running filter/transform stages per flow.", false,
		func(c *Config) *int { return &c.Limits.PipelineWorkers }),
	intField("GOFI_SINK_QUEUE_DEPTH", "Messages buffered in memory per sink before the drop policy applies.", false,
		func(c *Config) *int { return &c.Limits.SinkQueueDepth }),
	intField("GOFI_SINK_BATCH_MAX", "Largest batch a sink flushes in one write/request.", false,
		func(c *Config) *int { return &c.Limits.SinkBatchMax }),
	intField("GOFI_SINK_BATCH_BYTES", "Payload bytes per batched request (Loki, HTTP) before the batch is flushed early.", true,
		func(c *Config) *int { return &c.Limits.SinkBatchBytes }),
	durField("GOFI_SINK_BATCH_INTERVAL", "How long a partial sink batch waits for more messages before it is sent.",
		func(c *Config) *time.Duration { return &c.Limits.SinkBatchInterval }),
	{Env: "GOFI_SINK_DROP_POLICY", Desc: "Behaviour of a full sink queue: block (backpressure to the source), drop-newest, drop-oldest.",
		get: func(c *Config) string { return string(c.Limits.SinkDropPolicy) },
		set: func(c *Config, v string) error {
			c.Limits.SinkDropPolicy = DropPolicy(strings.ToLower(v))
			return nil
		}},
	intField("GOFI_SINK_RETRIES", "Retries of a retryable sink delivery failure (network error, HTTP 429/5xx) before the batch is dropped.", false,
		func(c *Config) *int { return &c.Limits.SinkRetries }),
	durField("GOFI_SINK_RETRY_INITIAL_BACKOFF", "First delay after a sink delivery failure; doubles each retry.",
		func(c *Config) *time.Duration { return &c.Limits.SinkRetryInitialBackoff }),
	durField("GOFI_SINK_RETRY_MAX_BACKOFF", "Cap on the exponential backoff between sink retries during an outage.",
		func(c *Config) *time.Duration { return &c.Limits.SinkRetryMaxBackoff }),
	intField("GOFI_SINK_BREAKER_FAILURES", "Consecutive failed deliveries that open a sink's circuit breaker (sends fail fast without touching the destination); 0 disables it.", false,
		func(c *Config) *int { return &c.Limits.SinkBreakerFailures }),
	durField("GOFI_SINK_BREAKER_COOLDOWN", "How long an open sink breaker fails fast before one delivery probes the destination again.",
		func(c *Config) *time.Duration { return &c.Limits.SinkBreakerCooldown }),
	intField("GOFI_HTTP_MAX_IDLE_CONNS", "Idle HTTP connections kept per destination host by HTTP-based sinks (HTTP, Loki, Zenoh REST).", false,
		func(c *Config) *int { return &c.Limits.HTTPMaxIdleConns }),
	{Env: "GOFI_SINK_SPOOL_DIR", Desc: "Directory for the on-disk sink spill buffer (segment-file queue). Empty disables spooling; messages that overflow the memory queue are then subject to the drop policy.",
		get: func(c *Config) string { return quoteEmpty(c.Limits.SinkSpoolDir) },
		set: func(c *Config, v string) error { c.Limits.SinkSpoolDir = v; return nil }},
	int64Field("GOFI_SINK_SPOOL_MAX_BYTES", "Upper bound of on-disk spool per sink; oldest segments are dropped past it.",
		func(c *Config) *int64 { return &c.Limits.SinkSpoolMaxBytes }),
	intField("GOFI_TAP_BUFFER_PER_SUBSCRIBER", "Tap events buffered per live-traffic subscriber (UI/CLI) before that subscriber starts missing events.", false,
		func(c *Config) *int { return &c.Limits.TapBufferPerSubscriber }),
	intField("GOFI_TAP_RAW_TRUNCATE", "Raw payload bytes carried per tap event (0 = none). Bounds memory and link usage of the UI stream.", true,
		func(c *Config) *int { return &c.Limits.TapRawTruncate }),
	intField("GOFI_DEDUP_MAX_KEYS", "Keys remembered per dedup stage; oldest are evicted first.", false,
		func(c *Config) *int { return &c.Limits.DedupMaxKeys }),
	intField("GOFI_RATELIMIT_MAX_KEYS", "Keys tracked per rate-limit stage; oldest are evicted first.", false,
		func(c *Config) *int { return &c.Limits.RateLimitMaxKeys }),
	intField("GOFI_PYTHON_WORKER_POOL", "Python worker processes per flow using Python stages/framing. Each worker is ~30-60 MB RSS.", false,
		func(c *Config) *int { return &c.Limits.PythonWorkerPool }),
	intField("GOFI_HISTORY_LIMIT", "Revisions kept per resource in the history store.", false,
		func(c *Config) *int { return &c.Limits.HistoryLimit }),
	durField("GOFI_HISTORY_DELETED_RETENTION", "How long history of deleted resources is kept (0 = forever).",
		func(c *Config) *time.Duration { return &c.Limits.HistoryDeletedRetention }),
	// Observability
	{Env: "GOFI_LOG_LEVEL", Desc: "Process log level: debug, info, warn, error.",
		get: func(c *Config) string { return strings.ToLower(c.Observability.LogLevel.String()) },
		set: func(c *Config, v string) error {
			var l slog.Level
			if err := l.UnmarshalText([]byte(strings.ToUpper(v))); err != nil {
				return fmt.Errorf("want debug|info|warn|error: %w", err)
			}
			c.Observability.LogLevel = l
			return nil
		}},
	{Env: "GOFI_LOG_FORMAT", Desc: "Log encoding: json (machine ingestion) or text (journald/console).",
		get: func(c *Config) string { return c.Observability.LogFormat },
		set: func(c *Config, v string) error { c.Observability.LogFormat = strings.ToLower(v); return nil }},
	boolField("GOFI_METRICS_ENABLED", "Serve metrics and aggregate per-flow counters.",
		func(c *Config) *bool { return &c.Observability.MetricsEnabled }),
	durField("GOFI_METRICS_INTERVAL", "Aggregation/push interval for per-flow metrics. Longer on intermittent links.",
		func(c *Config) *time.Duration { return &c.Observability.MetricsInterval }),
	boolField("GOFI_TAP_ENABLED", "Allow live traffic tap subscriptions (UI message stream).",
		func(c *Config) *bool { return &c.Observability.TapEnabled }),
	intField("GOFI_WATCH_MAX_STREAMS", "Open portal live streams (traffic + stats) one controller serves; further streams get resource_exhausted.", false,
		func(c *Config) *int { return &c.Observability.WatchMaxStreams }),
	intField("GOFI_WATCH_MAX_STREAMS_PER_CLIENT", "Open portal live streams per client IP (first X-Forwarded-For hop behind an ingress).", false,
		func(c *Config) *int { return &c.Observability.WatchMaxStreamsPerClient }),
	intField("GOFI_WATCH_QUEUE", "Events queued per browser stream; a browser further behind loses its own events (reported in the stream's dropped field).", false,
		func(c *Config) *int { return &c.Observability.WatchQueue }),
	durField("GOFI_HUB_IDLE_GRACE", "How long the controller keeps a flow's runtime stream open after its last viewer leaves, so a page change reuses it.",
		func(c *Config) *time.Duration { return &c.Observability.HubIdleGrace }),
	durField("GOFI_HUB_RESYNC", "How often a watched flow's runtime address is re-resolved when no Pod watcher pushes changes.",
		func(c *Config) *time.Duration { return &c.Observability.HubResync }),
	intField("GOFI_SAMPLE_BUFFER", "Recent messages retained per flow for UI sampling.", false,
		func(c *Config) *int { return &c.Observability.SampleBufferSize }),
	intField("GOFI_BULLETIN_RETENTION", "Bulletins (warnings) retained per flow.", false,
		func(c *Config) *int { return &c.Observability.BulletinRetention }),
	// Memory
	int64Field("GOFI_MEMORY_LIMIT", "Memory budget for the process (e.g. 128Mi). 0 = read the cgroup limit; if neither is set no GOMEMLIMIT is applied. Ignored when GOMEMLIMIT is already set.",
		func(c *Config) *int64 { return &c.Memory.LimitBytes }),
	{Env: "GOFI_GOMEMLIMIT_FRACTION", Desc: "Share of the memory budget given to the Go GC as GOMEMLIMIT; the remainder is headroom for Python workers and the kernel.",
		get: func(c *Config) string { return strconv.FormatFloat(c.Memory.GOMEMLIMITFraction, 'f', -1, 64) },
		set: func(c *Config, v string) error {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}
			c.Memory.GOMEMLIMITFraction = f
			return nil
		}},
	intField("GOFI_GOMAXPROCS", "Override GOMAXPROCS (0 = runtime default, which honours the cgroup CPU quota on Go 1.25+; older runtimes get the quota applied by GoFi).", false,
		func(c *Config) *int { return &c.Memory.GOMAXPROCS }),
}

func quoteEmpty(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

func plainInt(v string) (int64, error) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("want an integer: %w", err)
	}
	return n, nil
}

// byteSize parses "64Mi", "64MiB", "64M", "64MB", "1G", "512k", "1048576".
// Both binary (Ki/Mi/Gi) and SI-looking (k/M/G) suffixes are treated as
// powers of two, matching Go's GOMEMLIMIT convention.
func byteSize(v string) (int64, error) {
	s := strings.TrimSpace(strings.ToLower(v))
	s = strings.TrimSuffix(s, "b")
	s = strings.TrimSuffix(s, "i")
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 1<<10, s[:len(s)-1]
	case strings.HasSuffix(s, "m"):
		mult, s = 1<<20, s[:len(s)-1]
	case strings.HasSuffix(s, "g"):
		mult, s = 1<<30, s[:len(s)-1]
	case strings.HasSuffix(s, "t"):
		mult, s = 1<<40, s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("want a byte size like 64Mi or 1048576: %w", err)
	}
	if n < 0 {
		return 0, fmt.Errorf("byte size must not be negative")
	}
	if mult > 1 && n > (1<<62)/mult {
		return 0, fmt.Errorf("byte size overflows")
	}
	return n * mult, nil
}

// FormatBytes renders n as the shortest exact Ki/Mi/Gi form (e.g. 1Mi), or
// decimal when it is not a whole multiple.
func FormatBytes(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%dGi", n>>30)
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%dMi", n>>20)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%dKi", n>>10)
	}
	return strconv.FormatInt(n, 10)
}

// intField declares an integer variable; bytes selects byte-size parsing
// (64Mi, 512k) and rendering instead of a plain count.
func intField(env, desc string, isBytes bool, ptr func(*Config) *int) Field {
	parse := plainInt
	if isBytes {
		parse = byteSize
	}
	return Field{Env: env, Desc: desc,
		get: func(c *Config) string {
			n := int64(*ptr(c))
			if isBytes && n >= 1<<10 {
				return FormatBytes(n)
			}
			return strconv.FormatInt(n, 10)
		},
		set: func(c *Config, v string) error {
			n, err := parse(v)
			if err != nil {
				return err
			}
			if n > int64(^uint(0)>>1) {
				return fmt.Errorf("value too large")
			}
			*ptr(c) = int(n)
			return nil
		}}
}

func int64Field(env, desc string, ptr func(*Config) *int64) Field {
	return Field{Env: env, Desc: desc,
		get: func(c *Config) string { return FormatBytes(*ptr(c)) },
		set: func(c *Config, v string) error {
			n, err := byteSize(v)
			if err != nil {
				return err
			}
			*ptr(c) = n
			return nil
		}}
}

func durField(env, desc string, ptr func(*Config) *time.Duration) Field {
	return Field{Env: env, Desc: desc,
		get: func(c *Config) string { return (*ptr(c)).String() },
		set: func(c *Config, v string) error {
			// Accept bare seconds for convenience in unit files.
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				*ptr(c) = time.Duration(n) * time.Second
				return nil
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("want a duration like 30s or 5m: %w", err)
			}
			*ptr(c) = d
			return nil
		}}
}

func boolField(env, desc string, ptr func(*Config) *bool) Field {
	return Field{Env: env, Desc: desc,
		get: func(c *Config) string { return strconv.FormatBool(*ptr(c)) },
		set: func(c *Config, v string) error {
			b, err := strconv.ParseBool(strings.ToLower(v))
			if err != nil {
				switch strings.ToLower(v) {
				case "on", "yes", "y":
					b = true
				case "off", "no", "n":
					b = false
				default:
					return fmt.Errorf("want true|false: %w", err)
				}
			}
			*ptr(c) = b
			return nil
		}}
}
