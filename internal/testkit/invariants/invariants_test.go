package invariants

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/history"
	"github.com/wcrum/gofi/spec"
)

func def(name string, port int) spec.FlowDefinition {
	return spec.FlowDefinition{Name: name, Source: spec.FlowSource{Listen: &spec.ListenSource{Port: port, Network: "tcp"}}, Framing: spec.Framing{Mode: "line"}, Parser: spec.ParserCfg{Format: "json"}, Sinks: []spec.SinkCfg{{Type: "stdout"}}}
}

func running(d spec.FlowDefinition) Runtime {
	copy := d
	return Runtime{Flow: d.Name, ID: "rt-" + d.Name, Status: "running", Def: &copy}
}

func rules(violations []Violation) string {
	seen := map[string]bool{}
	var out []string
	for _, v := range violations {
		if !seen[v.Rule] {
			seen[v.Rule] = true
			out = append(out, v.Rule)
		}
	}
	return strings.Join(out, ",")
}

func TestCheckConvergedWorldHasNoViolations(t *testing.T) {
	a, b := def("a", 1), def("b", 2)
	stopped := def("c", 3)
	stopped.State = spec.StateStopped
	exposed := def("d", 4)
	exposed.Source.Listen.Service = &spec.ServiceExposure{Type: "NodePort", NodePort: 30004}
	ready, answers := true, true
	w := World{
		Desired:  []spec.FlowDefinition{a, b, stopped, exposed},
		Runtimes: []Runtime{running(a), running(b), running(exposed)},
		Services: []Service{{Name: "gofi-listener-d", Flow: "d", Type: "NodePort", Ports: []ServicePort{{Port: 4, TargetPort: 4, NodePort: 30004, Protocol: "TCP"}}, Selector: map[string]string{"gofi.io/managed-by": "true", "gofi.io/listener": "d"}}},
		History:  map[string][]Revision{"a": {{Number: 1, Hash: history.Hash(a), Def: &a}}, "gone": {{Number: 3, Hash: "x"}, {Number: 4, Deleted: true}}},
		Metrics:  map[string]bool{"http://ctrl/metrics": true},
		Ready:    &ready, APIAnswers: &answers,
	}
	if v := Check(w, Options{RuntimesExpected: true}); len(v) != 0 {
		t.Fatalf("unexpected violations: %s", Summary(v))
	}
}

func TestCheckDetectsEachBrokenInvariant(t *testing.T) {
	a := def("a", 1)
	edited := def("a", 1)
	edited.Stages = []spec.StageSpec{{ID: "x", Kind: "transform", TransformOp: "set", Field: "f", ValueExpr: `"1"`}}
	stopped := def("s", 2)
	stopped.State = spec.StateStopped
	exposed := def("d", 4)
	exposed.Source.Listen.Service = &spec.ServiceExposure{Type: "ClusterIP"}
	notReady, answers := false, true
	cases := map[string]struct {
		world World
		opts  Options
		want  string
	}{
		"missing runtime":          {World{Desired: []spec.FlowDefinition{a}}, Options{RuntimesExpected: true}, "one-runtime"},
		"duplicate runtime":        {World{Desired: []spec.FlowDefinition{a}, Runtimes: []Runtime{running(a), {Flow: "a", ID: "rt-a-2", Status: "running"}}}, Options{RuntimesExpected: true}, "one-runtime"},
		"exited runtime":           {World{Desired: []spec.FlowDefinition{a}, Runtimes: []Runtime{{Flow: "a", ID: "rt-a", Status: "exited"}}}, Options{RuntimesExpected: true}, "one-runtime"},
		"stale definition":         {World{Desired: []spec.FlowDefinition{edited}, Runtimes: []Runtime{running(a)}}, Options{RuntimesExpected: true}, "one-runtime"},
		"stopped flow running":     {World{Desired: []spec.FlowDefinition{stopped}, Runtimes: []Runtime{running(stopped)}}, Options{RuntimesExpected: true}, "stopped-no-runtime"},
		"orphan runtime":           {World{Desired: []spec.FlowDefinition{a}, Runtimes: []Runtime{running(a), running(def("ghost", 9))}}, Options{RuntimesExpected: true}, "no-orphan"},
		"observer orphan":          {World{Desired: []spec.FlowDefinition{a}, Runtimes: []Runtime{running(a)}, Orphans: []string{"secret/x"}}, Options{RuntimesExpected: true}, "no-orphan"},
		"service wrong port":       {World{Desired: []spec.FlowDefinition{exposed}, Runtimes: []Runtime{running(exposed)}, Services: []Service{{Flow: "d", Type: "ClusterIP", Ports: []ServicePort{{Port: 5, TargetPort: 4, Protocol: "TCP"}}, Selector: map[string]string{"gofi.io/managed-by": "true", "gofi.io/listener": "d"}}}}, Options{RuntimesExpected: true}, "service-shape"},
		"service wrong selector":   {World{Desired: []spec.FlowDefinition{exposed}, Runtimes: []Runtime{running(exposed)}, Services: []Service{{Flow: "d", Type: "ClusterIP", Ports: []ServicePort{{Port: 4, TargetPort: 4, Protocol: "TCP"}}, Selector: map[string]string{"app": "x"}}}}, Options{RuntimesExpected: true}, "service-shape"},
		"service for stopped flow": {World{Desired: []spec.FlowDefinition{a}, Runtimes: []Runtime{running(a)}, Services: []Service{{Flow: "a", Type: "ClusterIP"}}}, Options{RuntimesExpected: true}, "service-shape"},
		"missing service":          {World{Desired: []spec.FlowDefinition{exposed, a}, Runtimes: []Runtime{running(exposed), running(a)}, Services: []Service{{Flow: "zzz", Type: "ClusterIP"}}}, Options{RuntimesExpected: true}, "no-orphan,service-shape"},
		"history gap":              {World{Desired: []spec.FlowDefinition{a}, History: map[string][]Revision{"a": {{Number: 1, Hash: history.Hash(a)}, {Number: 3, Hash: history.Hash(a)}}}}, Options{}, "history"},
		"history not monotonic":    {World{Desired: []spec.FlowDefinition{a}, History: map[string][]Revision{"a": {{Number: 2, Hash: history.Hash(a)}, {Number: 1, Hash: history.Hash(a)}}}}, Options{}, "history"},
		"history hash mismatch":    {World{Desired: []spec.FlowDefinition{a}, History: map[string][]Revision{"a": {{Number: 1, Hash: "deadbeef", Def: &a}}}}, Options{}, "history"},
		"history head stale":       {World{Desired: []spec.FlowDefinition{edited}, History: map[string][]Revision{"a": {{Number: 1, Hash: history.Hash(a)}}}}, Options{}, "history"},
		"history tombstone live":   {World{Desired: []spec.FlowDefinition{a}, History: map[string][]Revision{"a": {{Number: 1, Hash: history.Hash(a)}, {Number: 2, Deleted: true}}}}, Options{}, "history"},
		"history missing":          {World{Desired: []spec.FlowDefinition{a}, History: map[string][]Revision{}}, Options{HistoryExpected: true}, "history"},
		"binding token mismatch":   {World{Desired: []spec.FlowDefinition{a}, Bindings: map[string]map[string]string{"a": {"lib": "t1"}}, Secrets: []Secret{{Name: "gofi-python-lib", Library: "lib", Token: "t2"}}}, Options{}, "bindings"},
		"secret orphan":            {World{Desired: []spec.FlowDefinition{a}, Libraries: []string{"lib"}, Secrets: []Secret{{Name: "s", Library: "old", Token: "t"}}}, Options{}, "no-orphan"},
		"uds package missing":      {World{Desired: []spec.FlowDefinition{a}}, Options{UDSExpected: true}, "uds-allows"},
		"uds stale allow":          {World{Desired: []spec.FlowDefinition{a}, Allows: []any{map[string]any{"direction": "Ingress", "description": "GoFi flow ghost NodePort ingress", "port": float64(1), "remoteGenerated": "Anywhere", "remoteProtocol": "TCP", "selector": map[string]any{"gofi.io/listener": "ghost"}}}}, Options{UDSExpected: true}, "uds-allows"},
		"metrics down":             {World{Desired: []spec.FlowDefinition{a}, Metrics: map[string]bool{"http://ctrl/metrics": false}}, Options{}, "metrics"},
		"readyz lies":              {World{Desired: []spec.FlowDefinition{a}, Ready: &notReady, APIAnswers: &answers}, Options{}, "readyz"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := rules(Check(tc.world, tc.opts))
			if got != tc.want {
				t.Fatalf("rules %q, want %q: %s", got, tc.want, Summary(Check(tc.world, tc.opts)))
			}
		})
	}
}

func TestUDSAllowsMatchGeneratedSet(t *testing.T) {
	exposed := def("d", 4)
	exposed.Source.Listen.Service = &spec.ServiceExposure{Type: "NodePort", NodePort: 30004}
	w := World{Desired: []spec.FlowDefinition{exposed}, Allows: normalizedAllowsFor([]spec.FlowDefinition{exposed})}
	if v := Check(w, Options{UDSExpected: true}); len(v) != 0 {
		t.Fatalf("generated allows must satisfy the invariant: %s", Summary(v))
	}
}

func TestPendingRuntimesOnlyWithOption(t *testing.T) {
	a := def("a", 1)
	w := World{Desired: []spec.FlowDefinition{a}, Runtimes: []Runtime{{Flow: "a", ID: "pod", Status: "Pending"}}}
	if v := Check(w, Options{RuntimesExpected: true}); rules(v) != "one-runtime" {
		t.Fatalf("pending must be unhealthy by default: %s", Summary(v))
	}
	if v := Check(w, Options{RuntimesExpected: true, PendingIsHealthy: true}); len(v) != 0 {
		t.Fatalf("pending must be healthy with the option: %s", Summary(v))
	}
}

// normalizedAllowsFor renders the driver's generated allows through a JSON
// round trip, as the API server would return them.
func normalizedAllowsFor(defs []spec.FlowDefinition) []any {
	data, _ := json.Marshal(driver.UDSAllows(defs))
	var out []any
	_ = json.Unmarshal(data, &out)
	return out
}
