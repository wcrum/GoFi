package clusterinfo

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func obj(apiVersion, kind, namespace, name string, fields map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind}}
	for k, v := range fields {
		u.Object[k] = v
	}
	u.SetName(name)
	if namespace != "" {
		u.SetNamespace(namespace)
	}
	return u
}

func testClient(t *testing.T) *Client {
	t.Helper()
	lbClass := "metallb"
	cs := fake.NewSimpleClientset(
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "envoy-udp", Namespace: "envoy-gateway-system"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &lbClass, Ports: []corev1.ServicePort{{Port: 60084, Protocol: corev1.ProtocolUDP}}},
			Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.50"}}}}},
		&netv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "deny", Namespace: "gofi"}, Spec: netv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"gofi.io/managed-by": "true"}}, PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress}}},
	)
	disc := cs.Discovery().(*fakediscovery.FakeDiscovery)
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "gateway.networking.k8s.io/v1", APIResources: []metav1.APIResource{{Name: "gateways", Namespaced: true, Kind: "Gateway"}, {Name: "gatewayclasses", Kind: "GatewayClass"}, {Name: "udproutes", Namespaced: true, Kind: "UDPRoute"}}},
		{GroupVersion: "metallb.io/v1beta1", APIResources: []metav1.APIResource{{Name: "ipaddresspools", Namespaced: true, Kind: "IPAddressPool"}}},
		{GroupVersion: "uds.dev/v1alpha1", APIResources: []metav1.APIResource{{Name: "packages", Namespaced: true, Kind: "Package"}}},
		{GroupVersion: "networking.k8s.io/v1", APIResources: []metav1.APIResource{{Name: "networkpolicies", Namespaced: true, Kind: "NetworkPolicy"}}},
	}
	scheme := runtime.NewScheme()
	lists := map[schema.GroupVersionResource]string{
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}:       "GatewayList",
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses"}: "GatewayClassList",
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "udproutes"}:      "UDPRouteList",
		{Group: "metallb.io", Version: "v1beta1", Resource: "ipaddresspools"}:           "IPAddressPoolList",
		{Group: "uds.dev", Version: "v1alpha1", Resource: "packages"}:                   "PackageList",
		{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}:        "NetworkPolicyList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, lists,
		obj("gateway.networking.k8s.io/v1", "GatewayClass", "", "envoy", map[string]any{"spec": map[string]any{"controllerName": EnvoyGatewayController},
			"status": map[string]any{"conditions": []any{map[string]any{"type": "Accepted", "status": "True"}}}}),
		obj("metallb.io/v1beta1", "IPAddressPool", "metallb-system", "default", map[string]any{"spec": map[string]any{"addresses": []any{"10.0.0.50-10.0.0.60"}}}),
		obj("uds.dev/v1alpha1", "Package", "gofi", "gofi", map[string]any{"spec": map[string]any{"network": map[string]any{"allow": []any{
			map[string]any{"direction": "Ingress", "selector": map[string]any{"gofi.io/listener": "telemetry"}, "remoteGenerated": "Anywhere", "port": int64(9000)},
		}}}}),
		obj("networking.k8s.io/v1", "NetworkPolicy", "gofi", "deny", map[string]any{"spec": map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"gofi.io/managed-by": "true"}}, "policyTypes": []any{"Ingress"}}}),
	)
	// Seeded objects get a guessed resource ("gatewaies"), so create the Gateway.
	gateway := obj("gateway.networking.k8s.io/v1", "Gateway", "envoy-gateway-system", "udp", map[string]any{
		"spec": map[string]any{"gatewayClassName": "envoy", "listeners": []any{
			map[string]any{"name": "udp-60084", "protocol": "UDP", "port": int64(60084), "allowedRoutes": map[string]any{"namespaces": map[string]any{"from": "All"}}},
			map[string]any{"name": "gofi-telemetry", "protocol": "TCP", "port": int64(9000)},
		}},
		"status": map[string]any{"addresses": []any{map[string]any{"value": "10.0.0.50"}}, "conditions": []any{map[string]any{"type": "Programmed", "status": "True"}},
			"listeners": []any{map[string]any{"name": "udp-60084", "attachedRoutes": int64(1)}}},
	})
	if _, err := dyn.Resource(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}).Namespace("envoy-gateway-system").Create(context.Background(), gateway, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return &Client{CS: cs, Dyn: dyn, Disc: disc, Opts: Options{Namespace: "gofi", ClusterWide: true, NodePortMin: 30000, NodePortMax: 32767}}
}

func TestDescribeFindsImplementationsGatewaysAndPools(t *testing.T) {
	out, err := testClient(t).Describe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	caps := map[string]bool{}
	for _, c := range out.Capabilities {
		caps[c.Id] = c.Present
	}
	for _, id := range []string{"gateway-api", "envoy-gateway", "metallb", "uds-core", "load-balancer"} {
		if !caps[id] {
			t.Errorf("capability %s not detected: %+v", id, out.Capabilities)
		}
	}
	if caps["istio"] {
		t.Error("istio reported without evidence")
	}
	if len(out.Gateways) != 1 || out.Gateways[0].Implementation != "envoy-gateway" || !out.Gateways[0].Programmed || len(out.Gateways[0].Listeners) != 2 {
		t.Fatalf("gateway: %+v warnings %v", out.Gateways, out.Warnings)
	}
	udp := out.Gateways[0].Listeners[0]
	if udp.Port != 60084 || udp.AllowedRoutes != "All" || udp.AttachedRoutes != 1 || udp.ManagedByGofi || !out.Gateways[0].Listeners[1].ManagedByGofi {
		t.Fatalf("listeners: %+v", out.Gateways[0].Listeners)
	}
	if !strings.Contains(strings.Join(out.Gateways[0].RouteKinds, ","), "UDPRoute") {
		t.Fatal("Envoy Gateway must advertise UDPRoute")
	}
	if len(out.AddressPools) != 1 || out.AddressPools[0].Addresses[0] != "10.0.0.50-10.0.0.60" || !out.AddressPools[0].AutoAssign {
		t.Fatalf("pools: %+v", out.AddressPools)
	}
	if len(out.LoadBalancerClasses) != 1 || out.LoadBalancerClasses[0] != "metallb" || out.LoadBalancers[0].Addresses[0] != "10.0.0.50" {
		t.Fatalf("load balancers: %+v %+v", out.LoadBalancerClasses, out.LoadBalancers)
	}
}

func TestNetworkingSummarizesAndAttributesFlows(t *testing.T) {
	flows := FlowLabels{"telemetry": {"gofi.io/managed-by": "true", "gofi.io/listener": "telemetry"}, "other": {"gofi.io/managed-by": "true", "gofi.io/listener": "other"}}
	out, err := testClient(t).Networking(context.Background(), "", flows)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]string{}
	affected := map[string][]string{}
	for _, o := range out.Objects {
		byKind[o.Kind] = o.Summary
		affected[o.Kind] = o.AffectedFlows
		if o.Yaml == "" {
			t.Errorf("%s/%s has no YAML", o.Kind, o.Name)
		}
	}
	if !strings.Contains(byKind["NetworkPolicy"], "denies all selected traffic") {
		t.Errorf("network policy summary: %q", byKind["NetworkPolicy"])
	}
	if len(affected["NetworkPolicy"]) != 2 {
		t.Errorf("deny-all policy should affect both flows: %v", affected["NetworkPolicy"])
	}
	if strings.Join(affected["Package"], ",") != "telemetry" {
		t.Errorf("package allow should affect telemetry only: %v", affected["Package"])
	}
	if !strings.Contains(byKind["Service"], "LoadBalancer 10.0.0.50") {
		t.Errorf("service summary: %q", byKind["Service"])
	}
	if strings.Join(out.Namespaces, ",") != "envoy-gateway-system,gofi,metallb-system" {
		t.Errorf("namespaces: %v", out.Namespaces)
	}
}

func TestImageTagStripsZarfSuffix(t *testing.T) {
	for image, want := range map[string]string{
		"127.0.0.1:31999/istio/pilot:1.31.1-distroless-zarf-4264239833": "1.31.1",
		"ghcr.io/defenseunicorns/pepr/controller:v2.0.3-zarf-804409620": "v2.0.3",
		"quay.io/metallb/controller:v0.16.1":                            "v0.16.1",
		"registry:5000/image@sha256:abc":                                "",
	} {
		if got := imageTag(image); got != want {
			t.Errorf("%s: got %q want %q", image, got, want)
		}
	}
}
