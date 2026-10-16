package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/wcrum/gofi/clusterinfo"
	"github.com/wcrum/gofi/spec"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
)

const (
	gatewayGroup = "gateway.networking.k8s.io"
	// annotationManagedListener records "<namespace>/<gateway>/<listener>" on
	// a route whose Gateway listener GoFi added, so deletion can remove it.
	annotationManagedListener = "gofi.wcrum.dev/gateway-listener"
	// annotationOwnedAnnotations lists the Service annotations GoFi set, so
	// annotations other controllers add (e.g. MetalLB status) are kept.
	annotationOwnedAnnotations = "gofi.wcrum.dev/annotations"
	metallbPoolAnnotation      = "metallb.io/address-pool"
	metallbIPsAnnotation       = "metallb.io/loadBalancerIPs"
)

// applyExposure sets the Service fields an exposure chooses. Gateway exposure
// is a ClusterIP Service behind a GoFi-managed TCPRoute or UDPRoute.
func applyExposure(svc *corev1.Service, cfg *spec.ServiceExposure) {
	if cfg.Type == "Gateway" {
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		svc.Spec.Ports[0].NodePort = 0
	}
	if cfg.ExternalTrafficPolicy != "" {
		svc.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicy(cfg.ExternalTrafficPolicy)
	}
	annotations := map[string]string{}
	for k, v := range cfg.Annotations {
		annotations[k] = v
	}
	if lb := cfg.LoadBalancer; lb != nil && cfg.Type == "LoadBalancer" {
		if lb.Class != "" {
			class := lb.Class
			svc.Spec.LoadBalancerClass = &class
		}
		if lb.AddressPool != "" {
			annotations[metallbPoolAnnotation] = lb.AddressPool
		}
		if lb.IP != "" {
			annotations[metallbIPsAnnotation] = lb.IP
		}
	}
	if len(annotations) > 0 {
		keys := make([]string, 0, len(annotations))
		for k := range annotations {
			keys = append(keys, k)
		}
		annotations[annotationOwnedAnnotations] = strings.Join(keys, ",")
		svc.Annotations = annotations
	}
}

// updateExposure carries the exposure fields onto an existing Service,
// replacing only the annotations GoFi set before.
func updateExposure(current, desired *corev1.Service) {
	current.Spec.ExternalTrafficPolicy = desired.Spec.ExternalTrafficPolicy
	if current.Spec.Type != corev1.ServiceTypeNodePort && current.Spec.Type != corev1.ServiceTypeLoadBalancer {
		current.Spec.ExternalTrafficPolicy = ""
	}
	// loadBalancerClass is immutable once set; a change recreates the flow.
	if current.Spec.LoadBalancerClass == nil {
		current.Spec.LoadBalancerClass = desired.Spec.LoadBalancerClass
	}
	if current.Annotations != nil {
		for _, key := range strings.Split(current.Annotations[annotationOwnedAnnotations], ",") {
			delete(current.Annotations, key)
		}
		delete(current.Annotations, annotationOwnedAnnotations)
	}
	for k, v := range desired.Annotations {
		if current.Annotations == nil {
			current.Annotations = map[string]string{}
		}
		current.Annotations[k] = v
	}
}

// routeGVR finds the served version of a Gateway API route kind (TCPRoute and
// UDPRoute moved from v1alpha2 to v1).
func (k *Kube) routeGVR(resource string) (schema.GroupVersionResource, error) {
	for _, version := range []string{"v1", "v1alpha2"} {
		list, err := k.cs.Discovery().ServerResourcesForGroupVersion(gatewayGroup + "/" + version)
		if err != nil {
			continue
		}
		for _, r := range list.APIResources {
			if r.Name == resource {
				return schema.GroupVersionResource{Group: gatewayGroup, Version: version, Resource: resource}, nil
			}
		}
	}
	return schema.GroupVersionResource{}, fmt.Errorf("this cluster does not serve %s (install the Gateway API experimental channel, or an implementation such as Envoy Gateway)", resource)
}

func routeKind(network string) (kind, resource string) {
	if network == "udp" {
		return "UDPRoute", "udproutes"
	}
	return "TCPRoute", "tcproutes"
}

// GatewayListenerName is the listener GoFi adds to a Gateway for a flow.
// Listener names are DNS labels (63 characters).
func GatewayListenerName(namespace, flow string) string {
	name := clusterinfo.ListenerPrefix + flow
	if len(name) <= 63 {
		return name
	}
	sum := sha256.Sum256([]byte(namespace + "/" + flow))
	return name[:54] + "-" + hex.EncodeToString(sum[:])[:8]
}

var gatewaysGVR = schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "gateways"}

func (k *Kube) reconcileGatewayRoute(ctx context.Context, def spec.FlowDefinition, pod *corev1.Pod) error {
	listen := def.Source.Listen
	if listen == nil || listen.Service == nil || listen.Service.Type != "Gateway" || listen.Service.Gateway == nil {
		return k.deleteGatewayRoute(ctx, def.Name)
	}
	if k.dyn == nil {
		return fmt.Errorf("Gateway exposure needs the Kubernetes dynamic client")
	}
	gw := listen.Service.Gateway
	kind, resource := routeKind(listen.Network)
	gvr, err := k.routeGVR(resource)
	if err != nil {
		return err
	}
	servicePort := listen.Service.Port
	if servicePort == 0 {
		servicePort = listen.Port
	}
	listener := gw.Listener
	managed := ""
	if listener == "" {
		if k.cluster == nil || !k.cluster.Opts.ManageGatewayListeners {
			return fmt.Errorf("choose a listener on Gateway %s/%s: GoFi is not allowed to add listeners (chart gateway.manageListeners)", gw.Namespace, gw.Name)
		}
		port := gw.Port
		if port == 0 {
			port = listen.Port
		}
		listener = GatewayListenerName(k.namespace, def.Name)
		if err := k.ensureGatewayListener(ctx, gw.Namespace, gw.Name, listener, strings.ToUpper(listenNetwork(listen)), kind, port); err != nil {
			return err
		}
		managed = gw.Namespace + "/" + gw.Name + "/" + listener
	}
	route := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvr.Group + "/" + gvr.Version, "kind": kind,
		"metadata": map[string]any{"name": pod.Name, "namespace": k.namespace,
			"labels":          map[string]any{kubeLabelManaged: "true", kubeLabelListener: def.Name},
			"ownerReferences": []any{map[string]any{"apiVersion": "v1", "kind": "Pod", "name": pod.Name, "uid": string(pod.UID)}}},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"group": gatewayGroup, "kind": "Gateway", "namespace": gw.Namespace, "name": gw.Name, "sectionName": listener}},
			"rules":      []any{map[string]any{"backendRefs": []any{map[string]any{"name": pod.Name, "port": int64(servicePort)}}}},
		},
	}}
	if managed != "" {
		route.SetAnnotations(map[string]string{annotationManagedListener: managed})
	}
	routes := k.dyn.Resource(gvr).Namespace(k.namespace)
	current, err := routes.Get(ctx, pod.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = routes.Create(ctx, route, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if current.GetLabels()[kubeLabelManaged] != "true" {
		return fmt.Errorf("%s %q exists and is not managed by GoFi", kind, pod.Name)
	}
	route.SetResourceVersion(current.GetResourceVersion())
	_, err = routes.Update(ctx, route, metav1.UpdateOptions{})
	return err
}

func listenNetwork(listen *spec.ListenSource) string {
	if listen.Network == "" {
		return "tcp"
	}
	return listen.Network
}

// ensureGatewayListener adds (or updates) a gofi-* listener on a Gateway.
// It never edits listeners GoFi did not add, and refuses a port another
// listener already uses for the same protocol.
func (k *Kube) ensureGatewayListener(ctx context.Context, namespace, name, listener, protocol, routeKind string, port int) error {
	gateways := k.dyn.Resource(gatewaysGVR).Namespace(namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw, err := gateways.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("Gateway %s/%s: %w", namespace, name, err)
		}
		listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		desired := map[string]any{"name": listener, "protocol": protocol, "port": int64(port),
			"allowedRoutes": map[string]any{
				"namespaces": map[string]any{"from": "Selector", "selector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": k.namespace}}},
				"kinds":      []any{map[string]any{"group": gatewayGroup, "kind": routeKind}},
			}}
		index := -1
		for i, raw := range listeners {
			existing, _ := raw.(map[string]any)
			existingName := fmt.Sprint(existing["name"])
			if existingName == listener {
				index = i
				continue
			}
			existingPort, _ := existing["port"].(int64)
			if existingPort == int64(port) && strings.EqualFold(fmt.Sprint(existing["protocol"]), protocol) {
				return fmt.Errorf("Gateway %s/%s already has listener %q on %s %d; select that listener instead", namespace, name, existingName, protocol, port)
			}
		}
		if index >= 0 {
			if fmt.Sprint(listeners[index]) == fmt.Sprint(desired) {
				return nil
			}
			listeners[index] = desired
		} else {
			listeners = append(listeners, desired)
		}
		if err := unstructured.SetNestedSlice(gw.Object, listeners, "spec", "listeners"); err != nil {
			return err
		}
		_, err = gateways.Update(ctx, gw, metav1.UpdateOptions{})
		return err
	})
}

// removeGatewayListener drops a listener GoFi added; others are untouched.
func (k *Kube) removeGatewayListener(ctx context.Context, managed string) error {
	parts := strings.SplitN(managed, "/", 3)
	if len(parts) != 3 || !strings.HasPrefix(parts[2], clusterinfo.ListenerPrefix) {
		return nil
	}
	gateways := k.dyn.Resource(gatewaysGVR).Namespace(parts[0])
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw, err := gateways.Get(ctx, parts[1], metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		kept := listeners[:0]
		for _, raw := range listeners {
			if existing, _ := raw.(map[string]any); fmt.Sprint(existing["name"]) != parts[2] {
				kept = append(kept, raw)
			}
		}
		if len(kept) == len(listeners) {
			return nil
		}
		if err := unstructured.SetNestedSlice(gw.Object, kept, "spec", "listeners"); err != nil {
			return err
		}
		_, err = gateways.Update(ctx, gw, metav1.UpdateOptions{})
		return err
	})
}

// deleteGatewayRoute removes a flow's route and any listener GoFi added for it.
func (k *Kube) deleteGatewayRoute(ctx context.Context, name string) error {
	if k.dyn == nil {
		return nil
	}
	for _, resource := range []string{"tcproutes", "udproutes"} {
		gvr, err := k.routeGVR(resource)
		if err != nil {
			continue
		}
		routes := k.dyn.Resource(gvr).Namespace(k.namespace)
		route, err := routes.Get(ctx, podName(name), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if route.GetLabels()[kubeLabelManaged] != "true" {
			continue
		}
		if managed := route.GetAnnotations()[annotationManagedListener]; managed != "" {
			if err := k.removeGatewayListener(ctx, managed); err != nil {
				return fmt.Errorf("remove Gateway listener %s: %w", managed, err)
			}
		}
		if err := routes.Delete(ctx, route.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}
