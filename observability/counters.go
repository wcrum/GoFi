package observability

import (
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// FlowCounters is one flow's cumulative counters since this process started
// it, readable without scraping Prometheus. The pipeline increments them on
// the same calls that drive the Prometheus collectors (Frame, ParseError,
// Rejected, SinkDispatch, TapDropped); the tap relay streams them to the
// controller once a second (WatchFlowStats) so dashboards never need the
// events themselves.
type FlowCounters struct {
	Observed     uint64
	ParseErrors  uint64
	Rejected     uint64
	Delivered    uint64
	SinkFailures uint64
	TapDropped   uint64
}

type flowCounter struct {
	observed, parseErrors, rejected, delivered, sinkFailures, tapDropped atomic.Uint64
}

var flowCounters sync.Map // flow name -> *flowCounter

func counterFor(flow string) *flowCounter {
	if c, ok := flowCounters.Load(flow); ok {
		return c.(*flowCounter)
	}
	c, _ := flowCounters.LoadOrStore(flow, &flowCounter{})
	return c.(*flowCounter)
}

// FlowSnapshot reads a flow's counters. Unknown flows read as zero.
func FlowSnapshot(flow string) FlowCounters {
	c, ok := flowCounters.Load(flow)
	if !ok {
		return FlowCounters{}
	}
	fc := c.(*flowCounter)
	return FlowCounters{
		Observed: fc.observed.Load(), ParseErrors: fc.parseErrors.Load(), Rejected: fc.rejected.Load(),
		Delivered: fc.delivered.Load(), SinkFailures: fc.sinkFailures.Load(), TapDropped: fc.tapDropped.Load(),
	}
}

// ResetFlowCounters forgets a flow's counters, for a listener that stopped.
func ResetFlowCounters(flow string) { flowCounters.Delete(flow) }

// Streaming-path collectors. Layers: "pipeline" (the pipeline's broadcast to
// its direct subscribers), "runtime" (the relay's per-viewer queues in the
// listener process), "controller" (the controller hub's per-browser queues).
var (
	tapDroppedByLayer = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_tap_dropped_total", Help: "Live traffic events not delivered to a slow subscriber, by fan-out layer. The pipeline never blocks for the tap."}, []string{"layer"})
	tapSubscriptions  = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_tap_subscriptions", Help: "Open live traffic subscriptions by fan-out layer."}, []string{"layer"})
	watchStreams      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_watch_streams", Help: "Open server streams to browsers by kind (traffic, stats, logs)."}, []string{"kind"})
	watchRejected     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_watch_streams_rejected_total", Help: "Server streams refused because a cap was reached (reason: global, client)."}, []string{"reason"})
	watchWriteTimeout = prometheus.NewCounter(prometheus.CounterOpts{Name: "gofi_watch_write_timeouts_total", Help: "Server streams closed because a browser did not read for the write deadline."})
	hubUpstreams      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_hub_upstreams", Help: "Open controller-to-runtime streams by kind (traffic, stats); at most one per flow and kind, however many browsers watch."}, []string{"kind"})
	relayReconnects   = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_relay_reconnects_total", Help: "Controller-to-runtime stream reconnects by kind."}, []string{"kind"})
)

func init() {
	registry.MustRegister(tapDroppedByLayer, tapSubscriptions, watchStreams, watchRejected, watchWriteTimeout, hubUpstreams, relayReconnects)
	for _, layer := range []string{"pipeline", "runtime", "controller"} {
		tapDroppedByLayer.WithLabelValues(layer)
		tapSubscriptions.WithLabelValues(layer)
	}
	for _, kind := range []string{"traffic", "stats"} {
		watchStreams.WithLabelValues(kind)
		hubUpstreams.WithLabelValues(kind)
		relayReconnects.WithLabelValues(kind)
	}
}

// TapDroppedAt counts events a fan-out layer dropped for slow subscribers.
func TapDroppedAt(layer string, n uint64) {
	if n > 0 {
		tapDroppedByLayer.WithLabelValues(layer).Add(float64(n))
	}
}

// TapSubscribed adjusts the open-subscription gauge of a layer by delta.
func TapSubscribed(layer string, delta int) {
	tapSubscriptions.WithLabelValues(layer).Add(float64(delta))
}

// WatchStream adjusts the open browser stream gauge of a kind by delta.
func WatchStream(kind string, delta int) { watchStreams.WithLabelValues(kind).Add(float64(delta)) }

// WatchStreamRejected counts a stream refused by the global or client cap.
func WatchStreamRejected(reason string) { watchRejected.WithLabelValues(reason).Inc() }

// WatchWriteTimeout counts a stream closed by its write deadline.
func WatchWriteTimeout() { watchWriteTimeout.Inc() }

// HubUpstream adjusts the controller's open upstream gauge of a kind.
func HubUpstream(kind string, delta int) { hubUpstreams.WithLabelValues(kind).Add(float64(delta)) }

// RelayReconnect counts one controller-to-runtime reconnect.
func RelayReconnect(kind string) { relayReconnects.WithLabelValues(kind).Inc() }
