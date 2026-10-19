package observability

import (
	"context"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	clientgometrics "k8s.io/client-go/tools/metrics"
)

// client-go throttling and request metrics for the controller's Kubernetes
// client, under the standard rest_client_* names and labels that
// kube-controller-manager and the Grafana client-go panels use, so existing
// dashboards and the chart's GoFiControllerAPIThrottled rule apply unchanged.
// Time spent waiting in the client-side rate limiter is what makes Pod lookups
// slow when GOFI_KUBE_QPS is too low for the number of flows.
var (
	kubeRateLimiterSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "rest_client_rate_limiter_duration_seconds", Help: "Time Kubernetes API requests waited in client-go's client-side rate limiter (throttling)."}, []string{"verb", "host"})
	kubeRequestSeconds     = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "rest_client_request_duration_seconds", Help: "Kubernetes API request latency by verb and host, as seen by client-go."}, []string{"verb", "host"})
	kubeRequests           = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "rest_client_requests_total", Help: "Kubernetes API requests by response code, HTTP method, and host."}, []string{"code", "method", "host"})
)

type kubeLatency struct{ vec *prometheus.HistogramVec }

func (k kubeLatency) Observe(_ context.Context, verb string, u url.URL, latency time.Duration) {
	k.vec.WithLabelValues(verb, u.Host).Observe(latency.Seconds())
}

type kubeResults struct{}

func (kubeResults) Increment(_ context.Context, code, method, host string) {
	kubeRequests.WithLabelValues(code, method, host).Inc()
}

func init() {
	registry.MustRegister(kubeRateLimiterSeconds, kubeRequestSeconds, kubeRequests)
	// client-go accepts one registration per process; a later Register is a no-op.
	clientgometrics.Register(clientgometrics.RegisterOpts{
		RateLimiterLatency: kubeLatency{kubeRateLimiterSeconds},
		RequestLatency:     kubeLatency{kubeRequestSeconds},
		RequestResult:      kubeResults{},
	})
}
