// Package observability exposes bounded pipeline counters and Prometheus collectors.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"time"
)

var registry = prometheus.NewRegistry()
var received = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_frames_received_total", Help: "Frames read from flow sources before decoding."}, []string{"listener"})
var receivedBytes = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_source_bytes_total", Help: "Original framed source bytes received, before decoding."}, []string{"listener"})
var parseErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_parser_errors_total", Help: "Parser failures, including failures whose configured policy permits processing."}, []string{"listener"})
var rejected = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_messages_rejected_total", Help: "Messages rejected by a configured pipeline stage or the parser."}, []string{"listener", "stage"})
var dispatches = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_sink_dispatch_total", Help: "Sink Send calls by outcome. Success describes a successful sink call, not end-to-end durable delivery."}, []string{"listener", "sink_type", "outcome"})
var dispatchSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "gofi_sink_dispatch_duration_seconds", Help: "Wall-clock duration of sink Send calls.", Buckets: prometheus.DefBuckets}, []string{"listener", "sink_type"})

func init() {
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}), received, receivedBytes, parseErrors, rejected, dispatches, dispatchSeconds)
}
func Handler() http.Handler { return promhttp.HandlerFor(registry, promhttp.HandlerOpts{}) }
func Frame(listener string, size int) {
	received.WithLabelValues(listener).Inc()
	receivedBytes.WithLabelValues(listener).Add(float64(size))
	counterFor(listener).observed.Add(1)
}

var tapDropped = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_tap_events_dropped_total", Help: "Live traffic events not delivered to a slow tap subscriber (the pipeline itself never blocks or drops for the tap)."}, []string{"listener"})

func init() { registry.MustRegister(tapDropped) }

// TapDropped counts live traffic events a slow subscriber missed.
func TapDropped(listener string, n int) {
	if n > 0 {
		tapDropped.WithLabelValues(listener).Add(float64(n))
		TapDroppedAt("pipeline", uint64(n))
		counterFor(listener).tapDropped.Add(uint64(n))
	}
}
func ParseError(listener string) {
	parseErrors.WithLabelValues(listener).Inc()
	counterFor(listener).parseErrors.Add(1)
}
func Rejected(listener, stage string) {
	rejected.WithLabelValues(listener, stage).Inc()
	counterFor(listener).rejected.Add(1)
}
func SinkDispatch(listener, sinkType string, duration time.Duration, err error) {
	if sinkType == "" {
		sinkType = "stdout"
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
		counterFor(listener).sinkFailures.Add(1)
	} else {
		counterFor(listener).delivered.Add(1)
	}
	dispatches.WithLabelValues(listener, sinkType, outcome).Inc()
	dispatchSeconds.WithLabelValues(listener, sinkType).Observe(duration.Seconds())
}

var stageErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_stage_errors_total", Help: "Stage evaluation errors by stage ID (CEL errors, parse stage failures, Python errors), whether or not the stage then dropped the message."}, []string{"listener", "stage"})

func init() { registry.MustRegister(stageErrors) }

// StageError counts one stage evaluation error.
func StageError(listener, stage string) { stageErrors.WithLabelValues(listener, stage).Inc() }
