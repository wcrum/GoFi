package driver

import (
	"context"
	"strings"
	"testing"

	"github.com/wcrum/gofi/clusterinfo"
	"github.com/wcrum/gofi/spec"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/fake"

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

// A flow may name a Gateway that does not exist yet and give a GatewayClass:
// GoFi then creates it, labels it its own, gives it the flow's listener, and
// lets later flows share it. It must refuse when the chart withholds the
// permission, and must never delete a Gateway it did not create.
func TestGatewayCreatedWhenAllowedSharedByFlowsAndRemovedWithTheLastOne(t *testing.T) {
	gateways := schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "gateways"}
	newDriver := func(manage bool) (*Kube, dynamic.Interface) {
		dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
			map[schema.GroupVersionResource]string{gateways: "GatewayList"})
		return &Kube{namespace: "gofi", dyn: dyn, gatewayMaxListeners: 64,
			cluster: clusterinfo.New(fake.NewSimpleClientset(), dyn, clusterinfo.Options{Namespace: "gofi", ManageGateways: manage, ManageGatewayListeners: true})}, dyn
	}
	listen := &spec.ListenSource{Port: 30001, Network: "udp"}
	gw := &spec.GatewayExposure{Namespace: "udp-gateway-system", Name: "gofi-udp", ClassName: "udp-gateway-class"}
	ctx := context.Background()

	// Refused without the permission.
	refusing, _ := newDriver(false)
	if err := refusing.ensureGateway(ctx, "radar", gw, listen, "UDPRoute"); err == nil || !strings.Contains(err.Error(), "manageGateways") {
		t.Fatalf("without gateway.manageGateways GoFi must refuse and say so: %v", err)
	}

	k, dyn := newDriver(true)
	if err := k.ensureGateway(ctx, "radar", gw, listen, "UDPRoute"); err != nil {
		t.Fatal(err)
	}
	created, err := dyn.Resource(gateways).Namespace(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if class, _, _ := unstructured.NestedString(created.Object, "spec", "gatewayClassName"); class != "udp-gateway-class" {
		t.Errorf("gatewayClassName = %q", class)
	}
	if created.GetLabels()[kubeLabelManaged] != "true" {
		t.Errorf("a Gateway GoFi created must be labelled its own: %v", created.GetLabels())
	}
	listeners, _, _ := unstructured.NestedSlice(created.Object, "spec", "listeners")
	if len(listeners) != 1 {
		t.Fatalf("want the flow's listener, got %v", listeners)
	}
	first, _ := listeners[0].(map[string]any)
	if first["name"] != GatewayListenerName("gofi", "radar") || first["protocol"] != "UDP" || first["port"] != int64(30001) {
		t.Errorf("listener = %v", first)
	}

	// A second flow shares it: same object, one more listener, still one Gateway.
	if err := k.ensureGateway(ctx, "weather", gw, listen, "UDPRoute"); err != nil {
		t.Fatal(err)
	}
	if err := k.ensureGatewayListener(ctx, gw.Namespace, gw.Name, GatewayListenerName("gofi", "weather"), "UDP", "UDPRoute", 30002); err != nil {
		t.Fatal(err)
	}
	list, err := dyn.Resource(gateways).Namespace(gw.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("flows share one Gateway, got %d", len(list.Items))
	}
	shared, _, _ := unstructured.NestedSlice(list.Items[0].Object, "spec", "listeners")
	if len(shared) != 2 {
		t.Fatalf("want a listener per flow, got %v", shared)
	}

	// Removing one flow keeps the Gateway; removing the last takes it away.
	if err := k.removeGatewayListener(ctx, gw.Namespace+"/"+gw.Name+"/"+GatewayListenerName("gofi", "weather")); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(gateways).Namespace(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("the Gateway still carries another flow: %v", err)
	}
	if err := k.removeGatewayListener(ctx, gw.Namespace+"/"+gw.Name+"/"+GatewayListenerName("gofi", "radar")); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(gateways).Namespace(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("a GoFi-created Gateway goes away with its last flow: %v", err)
	}
}
