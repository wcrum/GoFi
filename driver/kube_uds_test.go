package driver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/wcrum/gofi/spec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func allowsByDescription(defs []spec.FlowDefinition) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, raw := range UDSAllows(defs) {
		entry := raw.(map[string]any)
		out[fmt.Sprint(entry["description"])] = entry
	}
	return out
}

func TestUDSAllowsCoverExposureDestinationsAndSources(t *testing.T) {
	defs := []spec.FlowDefinition{
		{Name: "radar", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 60084, Network: "udp", Service: &spec.ServiceExposure{Type: "LoadBalancer"}}},
			Sinks: []spec.SinkCfg{
				{Type: "udp", Kubernetes: &spec.KubernetesTarget{Namespace: "demeter", Selector: map[string]string{"app": "demeter"}, Port: 60084}},
				{Type: "http", URL: "https://collector.example.com/ingest"},
				{Type: "tcp", URL: "tcp://receiver.apps.svc.cluster.local:7000"},
				{Type: "udp", URL: "udp://10.1.2.3:5000"},
				{Type: "tcp", URL: "tcp://local-receiver:7000"}, // same namespace: no allow
				{Type: "stdout"},
			}},
		{Name: "gw", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000, Network: "tcp", Service: &spec.ServiceExposure{Type: "Gateway", Gateway: &spec.GatewayExposure{Namespace: "envoy-gateway-system", Name: "udp"}}}}},
		{Name: "internal", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9001}}},
		{Name: "poll", Source: spec.FlowSource{Fetch: &spec.FetchSource{HTTP: &spec.HTTPFetch{URL: "http://api.example.com/items"}}}},
	}
	allows := allowsByDescription(defs)
	if len(allows) != 7 {
		t.Fatalf("want 7 allows, got %d: %v", len(allows), allows)
	}
	lb := allows["GoFi flow radar LoadBalancer ingress"]
	if lb["remoteGenerated"] != "Anywhere" || lb["port"] != int64(60084) || lb["remoteProtocol"] != "UDP" || fmt.Sprint(lb["selector"]) != "map[gofi.io/listener:radar]" {
		t.Errorf("load balancer ingress: %v", lb)
	}
	pods := allows["GoFi flow radar to demeter pods"]
	if pods["remoteNamespace"] != "demeter" || fmt.Sprint(pods["remoteSelector"]) != "map[app:demeter]" || pods["remoteProtocol"] != "UDP" {
		t.Errorf("cross-namespace selector egress: %v", pods)
	}
	if host := allows["GoFi flow radar to collector.example.com"]; host["remoteHost"] != "collector.example.com" || host["remoteProtocol"] != "TLS" || host["port"] != int64(443) {
		t.Errorf("https egress: %v", host)
	}
	if svc := allows["GoFi flow radar to receiver.apps.svc.cluster.local"]; svc["remoteNamespace"] != "apps" || svc["port"] != int64(7000) {
		t.Errorf("service DNS egress: %v", svc)
	}
	if ip := allows["GoFi flow radar to 10.1.2.3"]; ip["remoteCidr"] != "10.1.2.3/32" || ip["remoteProtocol"] != "UDP" {
		t.Errorf("IP egress: %v", ip)
	}
	if gw := allows["GoFi flow gw ingress from Gateway envoy-gateway-system/udp"]; gw["remoteNamespace"] != "envoy-gateway-system" || gw["port"] != int64(9000) {
		t.Errorf("gateway ingress: %v", gw)
	}
	if poll := allows["GoFi flow poll source to api.example.com"]; poll["remoteHost"] != "api.example.com" || poll["remoteProtocol"] != "HTTP" || poll["port"] != int64(80) {
		t.Errorf("fetch source egress: %v", poll)
	}
	// Order does not depend on flow order, so an unchanged set does not churn.
	reversed := []spec.FlowDefinition{defs[3], defs[2], defs[1], defs[0]}
	if fmt.Sprint(UDSAllows(defs)) != fmt.Sprint(UDSAllows(reversed)) {
		t.Error("allows depend on flow order")
	}
}

func TestGatewayListenerAddedOnceAndRemovedWithoutTouchingOthers(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "GatewayList"})
	gateway := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway",
		"metadata": map[string]any{"name": "edge", "namespace": "envoy-gateway-system"},
		"spec":     map[string]any{"gatewayClassName": "envoy", "listeners": []any{map[string]any{"name": "udp-60084", "protocol": "UDP", "port": int64(60084)}}}}}
	ctx := context.Background()
	if _, err := dyn.Resource(gvr).Namespace("envoy-gateway-system").Create(ctx, gateway, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	k := &Kube{namespace: "gofi", dyn: dyn}
	listener := GatewayListenerName("gofi", "radar")
	if err := k.ensureGatewayListener(ctx, "envoy-gateway-system", "edge", listener, "UDP", "UDPRoute", 60084); err == nil || !strings.Contains(err.Error(), "udp-60084") {
		t.Fatalf("a port in use by another listener must be refused: %v", err)
	}
	for range 2 { // idempotent
		if err := k.ensureGatewayListener(ctx, "envoy-gateway-system", "edge", listener, "UDP", "UDPRoute", 60085); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := dyn.Resource(gvr).Namespace("envoy-gateway-system").Get(ctx, "edge", metav1.GetOptions{})
	listeners, _, _ := unstructured.NestedSlice(got.Object, "spec", "listeners")
	if len(listeners) != 2 || !strings.Contains(fmt.Sprint(listeners[1]), "kubernetes.io/metadata.name:gofi") {
		t.Fatalf("listener not added once with a namespace-scoped allowedRoutes: %v", listeners)
	}
	if err := k.removeGatewayListener(ctx, "envoy-gateway-system/edge/udp-60084"); err != nil {
		t.Fatal(err)
	}
	if err := k.removeGatewayListener(ctx, "envoy-gateway-system/edge/"+listener); err != nil {
		t.Fatal(err)
	}
	got, _ = dyn.Resource(gvr).Namespace("envoy-gateway-system").Get(ctx, "edge", metav1.GetOptions{})
	listeners, _, _ = unstructured.NestedSlice(got.Object, "spec", "listeners")
	if len(listeners) != 1 || fmt.Sprint(listeners[0].(map[string]any)["name"]) != "udp-60084" {
		t.Fatalf("GoFi must remove only its own listener: %v", listeners)
	}
	if name := GatewayListenerName("gofi", strings.Repeat("x", 80)); len(name) != 63 {
		t.Errorf("long listener name not truncated to a DNS label: %d", len(name))
	}
}

func TestUDSPackageKeepsBaseAndReplacesGeneratedAllows(t *testing.T) {
	base := `apiVersion: uds.dev/v1alpha1
kind: Package
metadata:
  name: gofi
  namespace: ignored
spec:
  sso:
    - clientId: uds-wcrum-gofi
  network:
    allow:
      - direction: Ingress
        remoteGenerated: IntraNamespace
        description: Controller RPC within GoFi namespace
      - direction: Ingress
        description: GoFi flow stale ingress
`
	generated := []any{map[string]any{"direction": "Ingress", "description": "GoFi flow radar LoadBalancer ingress"}}
	pkg, err := udsPackageFromBase(base, "gofi", generated)
	if err != nil {
		t.Fatal(err)
	}
	allows, _, _ := unstructured.NestedSlice(pkg.Object, "spec", "network", "allow")
	if pkg.GetNamespace() != "gofi" || len(allows) != 2 || !strings.Contains(fmt.Sprint(allows[0]), "Controller RPC") || !strings.Contains(fmt.Sprint(allows[1]), "radar") {
		t.Fatalf("package: %+v", allows)
	}
	if sso, _, _ := unstructured.NestedSlice(pkg.Object, "spec", "sso"); len(sso) != 1 {
		t.Fatal("base SSO dropped")
	}
	if _, err := udsPackageFromBase("kind: ConfigMap\napiVersion: v1\nmetadata: {name: x}", "gofi", nil); err == nil {
		t.Fatal("a base that is not a Package was accepted")
	}
}

func TestUDSAllowsLimitClusterIPSendersToChosenPackages(t *testing.T) {
	defs := []spec.FlowDefinition{{Name: "ingest", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000, Network: "udp", Service: &spec.ServiceExposure{Type: "ClusterIP",
		AllowFrom: []spec.PeerRef{{Namespace: "radar", Package: "radar", Selector: map[string]string{"app": "radar"}}}}}},
		Sinks: []spec.SinkCfg{{Type: "tcp", URL: "tcp://receiver.apps.svc.cluster.local:7000"}}}}
	allows := allowsByDescription(defs)
	from := allows["GoFi flow ingest ingress from radar/radar"]
	if from["remoteNamespace"] != "radar" || fmt.Sprint(from["remoteSelector"]) != "map[app:radar]" || from["remoteProtocol"] != "UDP" {
		t.Fatalf("sender allow: %v", allows)
	}
	if _, open := allows["GoFi flow ingest in-cluster ingress"]; open {
		t.Fatal("chosen senders must replace the any-namespace allow")
	}
	if _, ok := allows["GoFi flow ingest to receiver.apps.svc.cluster.local"]; !ok {
		t.Fatal("destination egress must still be generated")
	}
}
