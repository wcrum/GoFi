package driver

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// A UDPRoute against an Istio Gateway is accepted by the API server and then
// ignored by Istio, with no status to diagnose: GoFi must refuse it instead of
// reporting a healthy flow that carries nothing. An Envoy Gateway serves it,
// and an implementation GoFi has no entry for is left alone.
func TestGatewayRouteKindRefusedOnlyWhenProvenUnsupported(t *testing.T) {
	gateways := schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "gateways"}
	classes := schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "gatewayclasses"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gateways: "GatewayList", classes: "GatewayClassList"})
	ctx := context.Background()
	gateway := func(namespace, name, class string) {
		object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": gatewayGroup + "/v1", "kind": "Gateway",
			"metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"gatewayClassName": class}}}
		if _, err := dyn.Resource(gateways).Namespace(namespace).Create(ctx, object, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	class := func(name, controller string) {
		object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": gatewayGroup + "/v1", "kind": "GatewayClass",
			"metadata": map[string]any{"name": name}, "spec": map[string]any{"controllerName": controller}}}
		if _, err := dyn.Resource(classes).Create(ctx, object, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	class("istio", "istio.io/gateway-controller")
	class("envoy", "gateway.envoyproxy.io/gatewayclass-controller")
	class("other", "example.com/gateway-controller")
	gateway("istio-system", "edge", "istio")
	gateway("envoy-gateway-system", "edge", "envoy")
	gateway("vendor", "edge", "other")

	k := &Kube{namespace: "gofi", dyn: dyn}
	err := k.checkRouteSupport(ctx, "istio-system", "edge", "UDPRoute")
	if err == nil || !strings.Contains(err.Error(), "serves no UDPRoute") {
		t.Fatalf("Istio serves no UDPRoute and must be refused: %v", err)
	}
	if err := k.checkRouteSupport(ctx, "istio-system", "edge", "TCPRoute"); err != nil {
		t.Fatalf("Istio does serve TCPRoute: %v", err)
	}
	if err := k.checkRouteSupport(ctx, "envoy-gateway-system", "edge", "UDPRoute"); err != nil {
		t.Fatalf("Envoy Gateway serves UDPRoute: %v", err)
	}
	if err := k.checkRouteSupport(ctx, "vendor", "edge", "UDPRoute"); err != nil {
		t.Fatalf("an unknown implementation may serve it, so it must not be refused: %v", err)
	}
	if err := k.checkRouteSupport(ctx, "istio-system", "missing", "UDPRoute"); err != nil {
		t.Fatalf("a Gateway that cannot be read leaves the decision alone: %v", err)
	}
}
