package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var sourceEvents = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_source_events_total", Help: "Pulled-source activity: polls and connections by outcome (ok, error, reconnect)."}, []string{"listener", "source", "outcome"})
var sourceLastSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_source_last_success_timestamp_seconds", Help: "Unix time of a pulled source's last successful poll or received message."}, []string{"listener", "source"})

func init() { registry.MustRegister(sourceEvents, sourceLastSuccess) }

// SourceEvent counts one poll or connection outcome of a fetch/subscribe source.
func SourceEvent(listener, source, outcome string) {
	sourceEvents.WithLabelValues(listener, source, outcome).Inc()
	if outcome == "ok" {
		sourceLastSuccess.WithLabelValues(listener, source).Set(float64(time.Now().Unix()))
	}
}
