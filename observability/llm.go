package observability

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The controller's LLM proxy (api/llm.go): one call per message of a Python
// library with AI on. Labels are bounded: library names are the deployment's
// libraries ("-" before authentication), status an HTTP status class.
var (
	llmProxyCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gofi_llm_proxy_calls_total",
		Help: "AI proxy calls by Python library and HTTP status (the proxy's own refusals included).",
	}, []string{"library", "status"})
	llmProxySeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gofi_llm_proxy_duration_seconds",
		Help:    "AI proxy call duration by Python library, from request to the end of the forwarded answer.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120},
	}, []string{"library"})
)

func init() { registry.MustRegister(llmProxyCalls, llmProxySeconds) }

// LLMProxyCall records one proxy call. status 0 means the request never got
// an HTTP answer (the connection dropped).
func LLMProxyCall(library string, status int, duration time.Duration) {
	if library == "" {
		library = "-"
	}
	llmProxyCalls.WithLabelValues(library, strconv.Itoa(status)).Inc()
	llmProxySeconds.WithLabelValues(library).Observe(duration.Seconds())
}
