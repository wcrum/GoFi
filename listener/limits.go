package listener

import (
	"sync"
	"time"

	"github.com/wcrum/gofi/config"
)

// Limits are the data-plane capacity knobs of a listener, read from the
// environment once per process. They bound what one flow runtime can be made
// to hold by its senders; the defaults suit a 512 MiB pod.
//
// The values come from the shared config package (GOFI_PROFILE presets plus
// the environment variables below); see docs/configuration.md.
type Limits struct {
	// MaxConnections caps concurrent TCP connections per listen source.
	// Connections past the cap are accepted and closed at once, counted in
	// gofi_listener_connections_refused_total. GOFI_MAX_CONNECTIONS.
	MaxConnections int
	// IdleTimeout closes a TCP connection that sent nothing for this long;
	// 0 disables it. Senders are expected to reconnect. GOFI_IDLE_TIMEOUT.
	IdleTimeout time.Duration
	// UDPReadBuffer is the kernel receive buffer requested for a UDP listen
	// socket (SO_RCVBUF); the kernel may clamp it to net.core.rmem_max.
	// GOFI_UDP_READ_BUFFER (bytes).
	UDPReadBuffer int
	// UDPWorkers is how many goroutines read and process datagrams. More than
	// one processes datagrams concurrently and so out of order; stateful
	// stages (rate-limit, dedup) stay correct, ordered sinks do not.
	// GOFI_UDP_WORKERS.
	UDPWorkers int
	// SourceWorkers is how many goroutines process pulled messages for
	// sources that do not acknowledge after processing (NATS, MQTT,
	// WebSocket, SSE, Redis Pub/Sub, fetch). Acknowledging brokers (Kafka,
	// AMQP, JetStream, Redis Streams) always process in order on the
	// consumer goroutine so an ack means the pipeline ran. GOFI_SOURCE_WORKERS.
	SourceWorkers int
}

// Defaults for Limits.
const (
	DefaultMaxConnections = 1024
	DefaultIdleTimeout    = 0
	DefaultUDPReadBuffer  = 4 << 20
	DefaultUDPWorkers     = 1
	DefaultSourceWorkers  = 1
	// maxUDPWorkers bounds a misconfiguration.
	maxWorkers = 256
)

// Environment variable names.
const (
	EnvMaxConnections = "GOFI_MAX_CONNECTIONS"
	EnvIdleTimeout    = "GOFI_IDLE_TIMEOUT"
	EnvUDPReadBuffer  = "GOFI_UDP_READ_BUFFER"
	EnvUDPWorkers     = "GOFI_UDP_WORKERS"
	EnvSourceWorkers  = "GOFI_SOURCE_WORKERS"
)

// DefaultLimits returns the built-in defaults.
func DefaultLimits() Limits {
	return Limits{MaxConnections: DefaultMaxConnections, IdleTimeout: DefaultIdleTimeout, UDPReadBuffer: DefaultUDPReadBuffer, UDPWorkers: DefaultUDPWorkers, SourceWorkers: DefaultSourceWorkers}
}

// LimitsFromEnv reads the limits from the process configuration
// (config.Load: GOFI_PROFILE preset, then the environment variables above,
// validated once at startup).
func LimitsFromEnv() Limits {
	return FromConfig(config.Get().Limits)
}

// FromConfig projects the shared config.Limits onto the listener's knobs.
func FromConfig(c config.Limits) Limits {
	l := DefaultLimits()
	if c.MaxConnectionsPerListener > 0 {
		l.MaxConnections = c.MaxConnectionsPerListener
	}
	if c.ConnIdleTimeout >= 0 {
		l.IdleTimeout = c.ConnIdleTimeout
	}
	if c.UDPReadBuffer > 0 {
		l.UDPReadBuffer = c.UDPReadBuffer
	}
	if c.UDPWorkers > 0 {
		l.UDPWorkers = min(c.UDPWorkers, maxWorkers)
	}
	if c.SourceWorkers > 0 {
		l.SourceWorkers = min(c.SourceWorkers, maxWorkers)
	}
	return l
}

// processLimits is the process-wide Limits every listener starts with.
var processLimits = sync.OnceValue(LimitsFromEnv)
