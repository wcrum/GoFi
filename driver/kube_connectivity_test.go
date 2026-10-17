package driver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wcrum/gofi/clusterinfo"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/spec"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestConnectivityVerifiesBothPackages(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GOFI_UDS_PACKAGE_BASE", "gofi-uds-package")
	k, cs := newTestKube("gofi")
	base := "apiVersion: uds.dev/v1alpha1\nkind: Package\nmetadata:\n  name: gofi\nspec:\n  network:\n    allow: []\n"
	if _, err := cs.CoreV1().ConfigMaps("gofi").Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "gofi-uds-package"}, Data: map[string]string{"package.yaml": base}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{udsPackages: "PackageList"})
	demeter := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "uds.dev/v1alpha1", "kind": "Package", "metadata": map[string]any{"name": "demeter", "namespace": "demeter"},
		"spec": map[string]any{"network": map[string]any{"allow": []any{
			// TCP only: a UDP sender is not admitted.
			map[string]any{"direction": "Ingress", "selector": map[string]any{"app": "demeter"}, "remoteNamespace": "gofi", "port": int64(60084)},
		}}}}}
	if _, err := dyn.Resource(udsPackages).Namespace("demeter").Create(ctx, demeter, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	k.dyn = dyn
	k.cluster = clusterinfo.New(cs, dyn, clusterinfo.Options{Namespace: "gofi", ClusterWide: true, ManageUDSAllows: true})

	def := spec.FlowDefinition{Name: "radar", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 60084, Network: "udp", Service: &spec.ServiceExposure{Type: "LoadBalancer"}}},
		Sinks: []spec.SinkCfg{{Type: "udp", Kubernetes: &spec.KubernetesTarget{Namespace: "demeter", Selector: map[string]string{"app": "demeter"}, Port: 60084}}}}
	out, err := k.AnalyzeConnectivity(ctx, def, nil, ConnectivityRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, p := range out.Paths {
		paths[p.Id] = p.Verdict
		if p.Id == "egress-sink-0" {
			if len(p.Fixes) != 1 || !strings.Contains(p.Fixes[0].Title, "demeter/demeter") || !strings.Contains(p.Fixes[0].Yaml, "remoteProtocol: UDP") || !strings.Contains(p.Fixes[0].Yaml, "remoteNamespace: gofi") {
				t.Fatalf("fix for the peer Package: %+v", p.Fixes)
			}
		}
	}
	if paths["ingress-external"] != "allowed" {
		t.Errorf("GoFi generates its own ingress allow: %v", paths)
	}
	if paths["egress-sink-0"] != "blocked" {
		t.Errorf("a TCP-only peer allow must block UDP: %v", paths)
	}
	// The peer adds the UDP allow: the path is allowed.
	allow, _, _ := unstructured.NestedSlice(demeter.Object, "spec", "network", "allow")
	allow[0].(map[string]any)["remoteProtocol"] = "UDP"
	_ = unstructured.SetNestedSlice(demeter.Object, allow, "spec", "network", "allow")
	if _, err := dyn.Resource(udsPackages).Namespace("demeter").Update(ctx, demeter, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	out, _ = k.AnalyzeConnectivity(ctx, def, nil, ConnectivityRuntime{})
	for _, p := range out.Paths {
		if p.Verdict != "allowed" {
			t.Errorf("%s: %s (%s)", p.Id, p.Verdict, p.Summary)
		}
	}
}

// managedKube is a Kube that manages GoFi's Package from an empty base, with
// the live Package as given (nil: none).
func managedKube(t *testing.T, live *unstructured.Unstructured) *Kube {
	t.Helper()
	ctx := context.Background()
	t.Setenv("GOFI_UDS_PACKAGE_BASE", "gofi-uds-package")
	k, cs := newTestKube("gofi")
	base := "apiVersion: uds.dev/v1alpha1\nkind: Package\nmetadata:\n  name: gofi\nspec:\n  network:\n    allow: []\n"
	if _, err := cs.CoreV1().ConfigMaps("gofi").Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "gofi-uds-package"}, Data: map[string]string{"package.yaml": base}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{udsPackages: "PackageList"})
	if live != nil {
		if _, err := dyn.Resource(udsPackages).Namespace("gofi").Create(ctx, live, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// The fake client has no server-side apply: store the applied object
	// (or only accept it, for a dry run).
	dyn.PrependReactor("patch", "packages", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchActionImpl)
		if patch.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		object := &unstructured.Unstructured{}
		if err := object.UnmarshalJSON(patch.GetPatch()); err != nil {
			return true, nil, err
		}
		// Like the real API server, fill in a CRD default the request omits,
		// so stored Packages never equal GoFi's raw desired spec.
		_ = unstructured.SetNestedField(object.Object, true, "spec", "defaulted")
		if len(patch.PatchOptions.DryRun) > 0 {
			return true, object, nil
		}
		tracker := dyn.Tracker()
		if _, err := tracker.Get(udsPackages, patch.GetNamespace(), patch.GetName()); err != nil {
			return true, object, tracker.Create(udsPackages, object, patch.GetNamespace())
		}
		return true, object, tracker.Update(udsPackages, object, patch.GetNamespace())
	})
	k.dyn = dyn
	k.cluster = clusterinfo.New(cs, dyn, clusterinfo.Options{Namespace: "gofi", ClusterWide: true, ManageUDSAllows: true})
	return k
}

func emptyGoFiPackage() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "uds.dev/v1alpha1", "kind": "Package", "metadata": map[string]any{"name": "gofi", "namespace": "gofi"},
		"spec": map[string]any{"network": map[string]any{"allow": []any{}}}}}
}

func TestConnectivityJoinsObservedFailures(t *testing.T) {
	ctx := context.Background()
	k := managedKube(t, emptyGoFiPackage())
	def := spec.FlowDefinition{Name: "radar", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000}},
		Sinks: []spec.SinkCfg{{Type: "tcp", URL: "tcp://10.1.2.3:7000"}}}
	failing := &gofiv1.RouteHealth{Route: "egress-sink-0", Failures: 3, Failing: true, ErrorClass: "reset", LastError: "tcp sink: write: connection reset by peer"}

	// GoFi would allow it, but the live Package lags: not allowed, and the
	// fix is GoFi's to apply.
	out, err := k.AnalyzeConnectivity(ctx, def, nil, ConnectivityRuntime{Saved: true, Observed: []*gofiv1.RouteHealth{failing}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Paths) != 1 {
		t.Fatalf("paths: %+v", out.Paths)
	}
	p := out.Paths[0]
	if p.Verdict != "blocked" || p.Diagnosis != "not-allowed" || p.Observed != failing || !strings.HasPrefix(p.Summary, "Not allowed: ") || !strings.Contains(p.Summary, "Istio ambient") {
		t.Fatalf("lagging live Package with failures: %+v", p)
	}
	if len(p.Fixes) != 1 || p.Fixes[0].Action != "apply-gofi-package" {
		t.Fatalf("GoFi applies its own Package: %+v", p.Fixes)
	}

	// Unsaved, GoFi applies it on save: no apply action.
	out, _ = k.AnalyzeConnectivity(ctx, def, nil, ConnectivityRuntime{})
	if fixes := out.Paths[0].Fixes; len(fixes) != 1 || fixes[0].Action != "" || out.Paths[0].Diagnosis != "" {
		t.Fatalf("unsaved flow: %+v", out.Paths[0])
	}

	// The live Package allows it, yet sends fail: unreachable, not policy.
	live := emptyGoFiPackage()
	_ = unstructured.SetNestedSlice(live.Object, []any{map[string]any{"direction": "Egress", "remoteCidr": "10.1.2.3/32", "port": int64(7000)}}, "spec", "network", "allow")
	k = managedKube(t, live)
	failing.ErrorClass = "refused"
	out, _ = k.AnalyzeConnectivity(ctx, def, nil, ConnectivityRuntime{Saved: true, Observed: []*gofiv1.RouteHealth{failing}})
	if p := out.Paths[0]; p.Verdict != "allowed" || p.Diagnosis != "unreachable" || !strings.Contains(p.Summary, "refused") {
		t.Fatalf("allowed but failing: %+v", p)
	}
}

func TestConnectivityFlagsSucceedingBlockedPath(t *testing.T) {
	ctx := context.Background()
	k := managedKube(t, emptyGoFiPackage())
	def := spec.FlowDefinition{Name: "radar", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000}},
		Sinks: []spec.SinkCfg{{Type: "tcp", URL: "tcp://10.1.2.3:7000"}}}
	ok := &gofiv1.RouteHealth{Route: "egress-sink-0", Successes: 5}
	out, _ := k.AnalyzeConnectivity(ctx, def, nil, ConnectivityRuntime{Saved: true, Observed: []*gofiv1.RouteHealth{ok}})
	if p := out.Paths[0]; p.Verdict != "blocked" || p.Diagnosis != "mismatch" {
		t.Fatalf("blocked by intent, working in practice: %+v", p)
	}
}

func TestPlanAndApplyGoFiPackage(t *testing.T) {
	ctx := context.Background()
	k := managedKube(t, emptyGoFiPackage())
	flows := []spec.FlowDefinition{{Name: "radar", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000}},
		Sinks: []spec.SinkCfg{{Type: "tcp", URL: "tcp://10.1.2.3:7000"}}}}
	plan := k.PlanUDSPackage(ctx, flows)
	if plan.Error != "" || plan.InSync || plan.Digest == "" || !strings.Contains(plan.DesiredYaml, "remoteCidr: 10.1.2.3/32") || strings.Contains(plan.LiveYaml, "remoteCidr") {
		t.Fatalf("plan: %+v", plan)
	}
	var refused *PackageRefusal
	if _, err := k.ApplyUDSPackage(ctx, flows, "stale"); !errors.As(err, &refused) {
		t.Fatalf("a stale plan is refused: %v", err)
	}
	applied, err := k.ApplyUDSPackage(ctx, flows, plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.InSync {
		t.Fatalf("after apply: %+v", applied)
	}

	k.cluster.Opts.ManageUDSAllows = false
	if _, err := k.ApplyUDSPackage(ctx, flows, applied.Digest); !errors.As(err, &refused) {
		t.Fatalf("an unmanaged Package is never written: %v", err)
	}
}
