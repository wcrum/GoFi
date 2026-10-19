//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/history"
	"github.com/wcrum/gofi/internal/testkit/envtest"
	"github.com/wcrum/gofi/internal/testkit/fakedriver"
	"github.com/wcrum/gofi/internal/testkit/flows"
	"github.com/wcrum/gofi/internal/testkit/invariants"
	"github.com/wcrum/gofi/spec"
)

// applyFlowCR creates a Flow custom resource the way kubectl apply would.
func applyFlowCR(t testing.TB, env *envtest.Environment, ns string, def spec.FlowDefinition) {
	t.Helper()
	_, dyn, err := env.Clients()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(def)
	var specObj map[string]any
	_ = json.Unmarshal(data, &specObj)
	delete(specObj, "name")
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": desired.APIVersion, "kind": desired.KindFlow, "metadata": map[string]any{"name": def.Name, "namespace": ns}, "spec": specObj}}
	if _, err := dyn.Resource(desired.FlowGVR).Namespace(ns).Create(context.Background(), obj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create Flow %s: %v", def.Name, err)
	}
}

// TestEnvtestConvergenceFromFlowCRs loads N Flow CRs from a real
// kube-apiserver (CRD schema enforced) and converges them; the second
// controller life must not write to the API server or the driver at all.
func TestEnvtestConvergenceFromFlowCRs(t *testing.T) {
	env := sharedEnv(t)
	for _, n := range []int{10, 100} {
		t.Run(fmt.Sprintf("%d-flows", n), func(t *testing.T) {
			rep := scenario(t, "envtest")
			ns := namespace(t, env)
			defs := flows.Many("cr", n, 30000, flows.Service("ClusterIP", 0))
			seedStart := time.Now()
			for _, def := range defs {
				applyFlowCR(t, env, ns, def)
			}
			rep.Set("seedSeconds", time.Since(seedStart).Seconds())
			_, dyn, _ := env.Clients()
			store := &desired.Kubernetes{Client: dyn, Namespace: ns}
			d := fakedriver.New("kubernetes")
			env.Counter.Reset()
			start := time.Now()
			_, stop := startController(t, store, d)
			waitFor(t, 60*time.Second, func() bool { return converged(d, defs) }, fmt.Sprintf("%d CR flows running", n))
			rep.Set("convergeSeconds", time.Since(start).Seconds())
			rep.Set("apiserverRequests", env.Counter.Total())
			rep.Set("apiserverVerbs", env.Counter.String())
			if writes := env.Counter.Writes(""); writes != 0 {
				t.Fatalf("converging from CRs must not write to the API server; saw %d (%s)", writes, env.Counter)
			}
			stop()

			env.Counter.Reset()
			d.ResetVerbs()
			_, stop = startController(t, store, d)
			defer stop()
			waitFor(t, 10*time.Second, func() bool { return d.Verbs().Lists >= 1 }, "second controller to sync")
			time.Sleep(300 * time.Millisecond)
			rep.Set("secondLifeDriverWrites", d.Verbs().Writes())
			rep.Set("secondLifeAPIWrites", env.Counter.Writes(""))
			rep.Set("secondLifeAPIRequests", env.Counter.Total())
			if d.Verbs().Writes() != 0 || env.Counter.Writes("") != 0 {
				t.Fatalf("second life wrote: driver %+v apiserver %s", d.Verbs(), env.Counter)
			}
			obs := &invariants.KubeObserver{Dynamic: dyn, Namespace: ns}
			cs, _, _ := env.Clients()
			obs.Clientset = cs
			w, err := obs.Observe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			w.Runtimes = runtimesOf(d)
			if v := invariants.Check(w, invariants.Options{RuntimesExpected: true}); len(v) > 0 {
				t.Fatalf("%s", invariants.Summary(v))
			}
		})
	}
}

// TestEnvtestCRDSchemaRejectsMalformedFlows checks the chart's CRD schema
// stops malformed resources at the API server, and that a schema-valid but
// semantically invalid Flow is isolated by the controller.
func TestEnvtestCRDSchemaRejectsMalformedFlows(t *testing.T) {
	env := sharedEnv(t)
	ns := namespace(t, env)
	_, dyn, _ := env.Clients()
	ctx := context.Background()
	for name, specObj := range map[string]map[string]any{
		"no-source":   {"framing": map[string]any{"mode": "line"}, "parser": map[string]any{"format": "json"}},
		"two-sources": {"source": map[string]any{"listen": map[string]any{"port": 1, "network": "tcp"}, "fetch": map[string]any{"http": map[string]any{"url": "http://x"}}}, "framing": map[string]any{"mode": "line"}, "parser": map[string]any{"format": "json"}},
	} {
		obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": desired.APIVersion, "kind": desired.KindFlow, "metadata": map[string]any{"name": name, "namespace": ns}, "spec": specObj}}
		if _, err := dyn.Resource(desired.FlowGVR).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{}); err == nil {
			t.Fatalf("%s: CRD schema accepted a malformed Flow", name)
		}
	}
	good := flows.Listen("good", 31000)
	bad := flows.Listen("bad-cel", 31001)
	bad.Sinks = []spec.SinkCfg{{Type: "stdout", When: "((("}}
	applyFlowCR(t, env, ns, good)
	applyFlowCR(t, env, ns, bad)
	store := &desired.Kubernetes{Client: dyn, Namespace: ns}
	d := fakedriver.New("kubernetes")
	ctrl, stop := startController(t, store, d)
	defer stop()
	waitFor(t, 10*time.Second, func() bool { _, ok := d.Running()["good"]; return ok }, "good flow running")
	if _, ok := d.Running()["bad-cel"]; ok {
		t.Fatal("semantically invalid flow was started")
	}
	info, found, _ := ctrl.RuntimeStatus(ctx, "bad-cel")
	if !found || info.Status != "invalid" {
		t.Fatalf("invalid flow not reported: %+v", info)
	}
}

// TestEnvtestHistoryFlowRevisions records revisions as FlowRevision objects
// and rolls back; the Kubernetes observer verifies the history invariants.
func TestEnvtestHistoryFlowRevisions(t *testing.T) {
	env := sharedEnv(t)
	ns := namespace(t, env)
	cs, dyn, _ := env.Clients()
	ctx := context.Background()
	store := &desired.Kubernetes{Client: dyn, Namespace: ns}
	d := fakedriver.New("kubernetes")
	ctrl, stop := startController(t, store, d)
	defer stop()
	rec := history.NewRecorder(&history.Kubernetes{Client: dyn, Namespace: ns}, ctrl, history.Options{Limit: 3})

	apply := func(def spec.FlowDefinition) history.Revision {
		rev, _, err := rec.Apply(ctx, def.Name, history.Meta{Source: history.SourcePortal, Author: "it"}, func() error { return ctrl.Add(def) })
		if err != nil {
			t.Fatal(err)
		}
		return rev
	}
	apply(flows.Listen("rev", 32000, flows.Service("NodePort", 30555)))
	apply(flows.Listen("rev", 32000, flows.Service("NodePort", 30555), flows.Transform("a", "a", `"1"`)))
	apply(flows.Listen("rev", 32000, flows.Service("NodePort", 30555), flows.Transform("a", "a", `"2"`)))
	apply(flows.Listen("rev", 32000, flows.Service("NodePort", 30555), flows.Transform("a", "a", `"3"`)))
	// Limit 3: revision 1 pruned; rolling back to 2 creates 5.
	rev, created, err := rec.Rollback(ctx, "rev", 2, history.Meta{Author: "it"}, func(def spec.FlowDefinition) error { return ctrl.Add(def) })
	if err != nil || !created || rev.Number != 5 {
		t.Fatalf("rollback: %+v %v %v", rev, created, err)
	}
	waitFor(t, 10*time.Second, func() bool {
		def, ok := d.Running()["rev"]
		return ok && len(def.Stages) == 1 && def.Stages[0].ValueExpr == `"1"`
	}, "rolled back definition running")
	obs := &invariants.KubeObserver{Clientset: cs, Dynamic: dyn, Namespace: ns}
	w, err := obs.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w.Runtimes = runtimesOf(d)
	if v := invariants.Check(w, invariants.Options{RuntimesExpected: true, HistoryExpected: true}); len(v) > 0 {
		t.Fatalf("%s", invariants.Summary(v))
	}
	numbers := []string{}
	for _, r := range w.History["rev"] {
		numbers = append(numbers, fmt.Sprint(r.Number))
	}
	if got := strings.Join(numbers, ","); got != "3,4,5" {
		t.Fatalf("retained revisions %s, want 3,4,5", got)
	}
}

// TestEnvtestUDSAllowsMatchFlowSet verifies the generated UDS allow set the
// controller hands the driver equals what the invariant expects for the
// declared flows, including after an edit and a removal.
func TestEnvtestUDSAllowsMatchFlowSet(t *testing.T) {
	env := sharedEnv(t)
	ns := namespace(t, env)
	_, dyn, _ := env.Clients()
	defs := []spec.FlowDefinition{
		flows.Listen("ingress-np", 33000, flows.Service("NodePort", 30600)),
		flows.Listen("egress-http", 33001, flows.HTTPSink("http://receiver.apps.svc.cluster.local:8080/events")),
		flows.Listen("private", 33002),
	}
	for _, def := range defs {
		applyFlowCR(t, env, ns, def)
	}
	store := &desired.Kubernetes{Client: dyn, Namespace: ns}
	d := fakedriver.New("kubernetes")
	ctrl, stop := startController(t, store, d)
	defer stop()
	waitFor(t, 10*time.Second, func() bool { return converged(d, defs) && d.Verbs().FlowSets >= 1 }, "flow set reconciled")
	check := func() {
		t.Helper()
		w := invariants.World{Desired: ctrl.Flows(), Runtimes: runtimesOf(d), Allows: allowsOf(d.LastFlowSet())}
		if v := invariants.Check(w, invariants.Options{RuntimesExpected: true, UDSExpected: true}); len(v) > 0 {
			t.Fatalf("%s", invariants.Summary(v))
		}
	}
	check()
	if err := ctrl.Remove("ingress-np"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool { return len(d.LastFlowSet()) == 2 }, "flow set shrinks after removal")
	check()
	// Stale allow planted in the observed Package is reported.
	w := invariants.World{Desired: ctrl.Flows(), Runtimes: runtimesOf(d), Allows: append(allowsOf(d.LastFlowSet()), map[string]any{"direction": "Ingress", "description": "GoFi flow ghost NodePort ingress", "port": float64(1), "remoteGenerated": "Anywhere", "remoteProtocol": "TCP", "selector": map[string]any{"gofi.io/listener": "ghost"}})}
	if v := invariants.Check(w, invariants.Options{RuntimesExpected: true, UDSExpected: true}); len(v) != 1 || v[0].Rule != "uds-allows" {
		t.Fatalf("stale allow not detected: %s", invariants.Summary(v))
	}
}

// allowsOf renders the allow entries the driver would apply, through a JSON
// round trip so numbers compare as the API server returns them.
func allowsOf(defs []spec.FlowDefinition) []any {
	data, _ := json.Marshal(driverAllows(defs))
	var out []any
	_ = json.Unmarshal(data, &out)
	return out
}

func driverAllows(defs []spec.FlowDefinition) []any { return driver.UDSAllows(defs) }
