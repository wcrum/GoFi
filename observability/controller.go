package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Controller availability metrics (charts/gofi/templates/prometheusrule.yaml
// alerts on them): which replica leads, how often leadership changes, watch
// failures, and when the last reconcile pass completed.
var (
	leader = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_controller_leader",
		Help: "1 on the replica that reconciles (the Lease holder, or the only replica when election is off), 0 on standbys."}, []string{"pod"})
	leaderTransitions = prometheus.NewCounter(prometheus.CounterOpts{Name: "gofi_controller_leader_transitions_total",
		Help: "Times this replica gained or lost leadership."})
	watchErrors = prometheus.NewCounter(prometheus.CounterOpts{Name: "gofi_controller_watch_errors_total",
		Help: "Informer list/watch failures against the Kubernetes API."})
	reconcileLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{Name: "gofi_controller_reconcile_last_success_timestamp_seconds",
		Help: "Unix time of the last completed full reconcile pass."})
)

func init() { registry.MustRegister(leader, leaderTransitions, watchErrors, reconcileLastSuccess) }

// SetLeader records whether this replica (pod) reconciles.
func SetLeader(pod string, leading bool) {
	value := 0.0
	if leading {
		value = 1
	}
	leader.WithLabelValues(pod).Set(value)
}

// LeaderTransition counts a change of this replica's leadership.
func LeaderTransition() { leaderTransitions.Inc() }

// WatchError counts one informer list/watch failure.
func WatchError() { watchErrors.Inc() }

// ReconcileSucceeded marks the completion of a full reconcile pass.
func ReconcileSucceeded() { reconcileLastSuccess.Set(float64(time.Now().Unix())) }
