package observability

import "github.com/prometheus/client_golang/prometheus"

// Data-plane capacity metrics: connection caps, kernel-side UDP loss, sink
// queues, and slow pulled-source consumers. They describe where a flow is
// losing or refusing data, which the per-message counters cannot show.
var (
	connections        = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_listener_connections", Help: "Open TCP connections on a listen source."}, []string{"listener"})
	connectionsRefused = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_listener_connections_refused_total", Help: "TCP connections closed at accept because the listener was at its connection cap (GOFI_MAX_CONNECTIONS)."}, []string{"listener"})
	connectionsIdle    = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_listener_connections_idle_closed_total", Help: "TCP connections closed after sending nothing for the idle timeout (GOFI_IDLE_TIMEOUT)."}, []string{"listener"})
	udpKernelDrops     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_udp_kernel_drops_total", Help: "Datagrams the kernel dropped on the listen socket because its receive buffer was full (SO_RXQ_OVFL); the process never saw them."}, []string{"listener"})
	udpReadBuffer      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_udp_read_buffer_bytes", Help: "Effective kernel receive buffer of the UDP listen socket."}, []string{"listener"})

	sinkQueueDepth      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_sink_queue_depth", Help: "Messages waiting in a sink's async queue."}, []string{"listener", "sink_type"})
	sinkQueueDropped    = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_sink_queue_dropped_total", Help: "Messages dropped because a sink's queue was full and its policy is drop."}, []string{"listener", "sink_type"})
	sinkQueueBlocked    = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_sink_queue_blocked_total", Help: "Sends that waited for room in a full sink queue (policy block)."}, []string{"listener", "sink_type"})
	sinkRetries         = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_sink_retries_total", Help: "Retried sink sends after a retryable failure."}, []string{"listener", "sink_type"})
	sinkBreakerOpen     = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_sink_breaker_open", Help: "1 while a sink's circuit breaker is open and sends fail fast."}, []string{"listener", "sink_type"})
	sinkBreakerRejected = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_sink_breaker_rejected_total", Help: "Sends failed fast by an open circuit breaker."}, []string{"listener", "sink_type"})
	sinkSpooled         = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_sink_spooled_total", Help: "Messages written to a sink's on-disk overflow spool because its memory queue was full or its breaker open."}, []string{"listener", "sink_type"})
	sinkSpoolDepth      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_sink_spool_depth", Help: "Messages waiting in a sink's on-disk overflow spool."}, []string{"listener", "sink_type"})
	sinkBatches         = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "gofi_sink_batch_size", Help: "Messages per batched sink request.", Buckets: []float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000}}, []string{"listener", "sink_type"})

	sourceSlowConsumer = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_source_slow_consumer_total", Help: "Messages a pulled source's client dropped because the pipeline fell behind (NATS slow consumer)."}, []string{"listener", "source"})
)

func init() {
	registry.MustRegister(connections, connectionsRefused, connectionsIdle, udpKernelDrops, udpReadBuffer,
		sinkQueueDepth, sinkQueueDropped, sinkQueueBlocked, sinkRetries, sinkBreakerOpen, sinkBreakerRejected, sinkBatches, sinkSpooled, sinkSpoolDepth,
		sourceSlowConsumer)
}

// Connections is the open-connection gauge of a listener.
func Connections(listener string) prometheus.Gauge { return connections.WithLabelValues(listener) }

// ConnectionRefused counts one connection closed at the cap.
func ConnectionRefused(listener string) { connectionsRefused.WithLabelValues(listener).Inc() }

// ConnectionIdleClosed counts one connection closed by the idle timeout.
func ConnectionIdleClosed(listener string) { connectionsIdle.WithLabelValues(listener).Inc() }

// UDPKernelDrops adds datagrams the kernel reported dropped.
func UDPKernelDrops(listener string, n uint64) {
	if n > 0 {
		udpKernelDrops.WithLabelValues(listener).Add(float64(n))
	}
}

// UDPReadBuffer records the socket's effective receive buffer.
func UDPReadBuffer(listener string, size int) {
	udpReadBuffer.WithLabelValues(listener).Set(float64(size))
}

// SinkQueue holds one sink queue's metric handles.
type SinkQueue struct {
	Depth           prometheus.Gauge
	Dropped         prometheus.Counter
	Blocked         prometheus.Counter
	Retries         prometheus.Counter
	BreakerOpen     prometheus.Gauge
	BreakerRejected prometheus.Counter
	BatchSize       prometheus.Observer
	Spooled         prometheus.Counter
	SpoolDepth      prometheus.Gauge
}

// NewSinkQueue resolves the handles for one sink of a flow.
func NewSinkQueue(listener, sinkType string) *SinkQueue {
	if sinkType == "" {
		sinkType = "stdout"
	}
	return &SinkQueue{
		Depth:           sinkQueueDepth.WithLabelValues(listener, sinkType),
		Dropped:         sinkQueueDropped.WithLabelValues(listener, sinkType),
		Blocked:         sinkQueueBlocked.WithLabelValues(listener, sinkType),
		Retries:         sinkRetries.WithLabelValues(listener, sinkType),
		BreakerOpen:     sinkBreakerOpen.WithLabelValues(listener, sinkType),
		BreakerRejected: sinkBreakerRejected.WithLabelValues(listener, sinkType),
		BatchSize:       sinkBatches.WithLabelValues(listener, sinkType),
		Spooled:         sinkSpooled.WithLabelValues(listener, sinkType),
		SpoolDepth:      sinkSpoolDepth.WithLabelValues(listener, sinkType),
	}
}

// SourceSlowConsumer counts messages a source client dropped.
func SourceSlowConsumer(listener, source string, n uint64) {
	if n > 0 {
		sourceSlowConsumer.WithLabelValues(listener, source).Add(float64(n))
	}
}

var decodeBudgetExceeded = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_decode_budget_exceeded_total", Help: "Messages dropped because their decoders (decode step plus parse stages) produced more than the per-message budget (GOFI_MAX_DECODED_BYTES_PER_MESSAGE)."}, []string{"listener"})

func init() { registry.MustRegister(decodeBudgetExceeded) }

// DecodeBudgetExceeded counts one message dropped over the decoded-bytes budget.
func DecodeBudgetExceeded(listener string) { decodeBudgetExceeded.WithLabelValues(listener).Inc() }
