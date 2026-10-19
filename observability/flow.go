package observability

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Flow holds one flow's metric handles, resolved once so the per-message
// path does no label lookups (CounterVec.WithLabelValues hashes labels and
// takes a lock on every call). Build one per listener with NewFlow and share
// it across the listener's connections.
type Flow struct {
	name          string
	frames        prometheus.Counter
	bytes         prometheus.Counter
	parseErrors   prometheus.Counter
	rejected      sync.Map // stage -> prometheus.Counter
	dispatch      sync.Map // sinkType -> *sinkDispatch
	routes        sync.Map // route -> *Route
	tapDropped    prometheus.Counter
	rejectedKnown map[string]prometheus.Counter
	// counters are the flow's readable totals (FlowSnapshot), kept in step
	// with the Prometheus handles for WatchFlowStats.
	counters *flowCounter
}

type sinkDispatch struct {
	success, failure prometheus.Counter
	seconds          prometheus.Observer
}

// NewFlow resolves the flow's handles. stages and sinkTypes pre-resolve the
// labels the pipeline will use; unknown ones are resolved (and cached) on
// first use.
func NewFlow(name string, stages []string, sinkTypes []string) *Flow {
	f := &Flow{
		name:          name,
		frames:        received.WithLabelValues(name),
		bytes:         receivedBytes.WithLabelValues(name),
		parseErrors:   parseErrors.WithLabelValues(name),
		tapDropped:    tapDropped.WithLabelValues(name),
		rejectedKnown: map[string]prometheus.Counter{},
		counters:      counterFor(name),
	}
	for _, stage := range append([]string{"parser"}, stages...) {
		f.rejectedKnown[stage] = rejected.WithLabelValues(name, stage)
	}
	for _, kind := range sinkTypes {
		f.sinkDispatch(kind)
	}
	return f
}

// Name is the flow name.
func (f *Flow) Name() string { return f.name }

// Frame counts one frame of size bytes.
func (f *Flow) Frame(size int) {
	f.frames.Inc()
	f.bytes.Add(float64(size))
	f.counters.observed.Add(1)
}

// ParseError counts one parser failure.
func (f *Flow) ParseError() {
	f.parseErrors.Inc()
	f.counters.parseErrors.Add(1)
}

// Rejected counts one message rejected by stage.
func (f *Flow) Rejected(stage string) {
	f.counters.rejected.Add(1)
	if c, ok := f.rejectedKnown[stage]; ok {
		c.Inc()
		return
	}
	c, ok := f.rejected.Load(stage)
	if !ok {
		c, _ = f.rejected.LoadOrStore(stage, rejected.WithLabelValues(f.name, stage))
	}
	c.(prometheus.Counter).Inc()
}

// TapDropped counts live traffic events a slow subscriber missed.
func (f *Flow) TapDropped(n int) {
	if n > 0 {
		f.tapDropped.Add(float64(n))
		TapDroppedAt("pipeline", uint64(n))
		f.counters.tapDropped.Add(uint64(n))
	}
}

func (f *Flow) sinkDispatch(sinkType string) *sinkDispatch {
	if sinkType == "" {
		sinkType = "stdout"
	}
	if d, ok := f.dispatch.Load(sinkType); ok {
		return d.(*sinkDispatch)
	}
	d, _ := f.dispatch.LoadOrStore(sinkType, &sinkDispatch{
		success: dispatches.WithLabelValues(f.name, sinkType, "success"),
		failure: dispatches.WithLabelValues(f.name, sinkType, "error"),
		seconds: dispatchSeconds.WithLabelValues(f.name, sinkType),
	})
	return d.(*sinkDispatch)
}

// SinkDispatch records one sink call. It is SinkDispatch with cached handles.
func (f *Flow) SinkDispatch(sinkType string, duration time.Duration, err error) {
	d := f.sinkDispatch(sinkType)
	if err != nil {
		d.failure.Inc()
		f.counters.sinkFailures.Add(1)
	} else {
		d.success.Inc()
		f.counters.delivered.Add(1)
	}
	d.seconds.Observe(duration.Seconds())
}

// Route returns the recorder for one of the flow's routes.
func (f *Flow) Route(route string) *Route {
	if r, ok := f.routes.Load(route); ok {
		return r.(*Route)
	}
	r, _ := f.routes.LoadOrStore(route, NewRoute(f.name, route))
	return r.(*Route)
}
