package driver

import (
	"context"
	"strings"
	"testing"

	"github.com/wcrum/gofi/clusterinfo"
	"github.com/wcrum/gofi/spec"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
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
	out, err := k.AnalyzeConnectivity(ctx, def, nil)
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
	out, _ = k.AnalyzeConnectivity(ctx, def, nil)
	for _, p := range out.Paths {
		if p.Verdict != "allowed" {
			t.Errorf("%s: %s (%s)", p.Id, p.Verdict, p.Summary)
		}
	}
}
