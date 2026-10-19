package observability

import "github.com/prometheus/client_golang/prometheus"

// Controller-side flow state, for alerting on flows that should run but do
// not. The controller sets these on every reconcile.
var (
	flowDesiredRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_flow_desired_running", Help: "1 when the flow's declared state is running, 0 when it is stopped."}, []string{"listener"})
	flowRuntimeUp      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gofi_flow_runtime_up", Help: "1 when the driver reports a live runtime (process, container, or Pod) for the flow."}, []string{"listener"})
	flowRestarts       = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_flow_restarts_total", Help: "Runtimes the controller restarted after they exited on their own."}, []string{"listener"})
)

func init() { registry.MustRegister(flowDesiredRunning, flowRuntimeUp, flowRestarts) }

// FlowState is one flow's declared and observed state.
type FlowState struct {
	DesiredRunning bool
	RuntimeUp      bool
}

// SetFlowStates replaces the flow state gauges, so deleted flows disappear.
func SetFlowStates(states map[string]FlowState) {
	flowDesiredRunning.Reset()
	flowRuntimeUp.Reset()
	for name, state := range states {
		flowDesiredRunning.WithLabelValues(name).Set(boolFloat(state.DesiredRunning))
		flowRuntimeUp.WithLabelValues(name).Set(boolFloat(state.RuntimeUp))
	}
}

// FlowRestarted counts a restart of a runtime that exited on its own.
func FlowRestarted(name string) { flowRestarts.WithLabelValues(name).Inc() }

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
