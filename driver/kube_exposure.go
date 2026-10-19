package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/wcrum/gofi/clusterinfo"
	"github.com/wcrum/gofi/spec"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
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

	// routeDiscoveryTTL is how long a discovery answer (which Gateway API
	// version serves a route kind, or that none does) is reused. Discovery
	// used to run twice per flow per sync.
	routeDiscoveryTTL = 5 * time.Minute
)

// applyExposure sets the Service fields an exposure chooses. Gateway exposure
// is a ClusterIP Service behind a GoFi-managed TCPRoute or UDPRoute.
// lbDefaults are the chart's annotations for every LoadBalancer Service
// (GOFI_LB_ANNOTATIONS); the flow's own annotations win.
func applyExposure(svc *corev1.Service, cfg *spec.ServiceExposure, lbDefaults map[string]string) {
	if cfg.Type == "Gateway" {
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		svc.Spec.Ports[0].NodePort = 0
	}
	if cfg.ExternalTrafficPolicy != "" {
		svc.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicy(cfg.ExternalTrafficPolicy)
	}
	annotations := map[string]string{}
	if cfg.Type == "LoadBalancer" {
		for k, v := range lbDefaults {
			annotations[k] = v
		}
	}
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
		sort.Strings(keys) // a stable list, so an unchanged Service compares equal
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
		if len(current.Annotations) == 0 {
			current.Annotations = nil
		}
	}
	for k, v := range desired.Annotations {
		if current.Annotations == nil {
			current.Annotations = map[string]string{}
		}
		current.Annotations[k] = v
	}
}

// routeDiscovery is one cached discovery answer.
type routeDiscovery struct {
	gvr     schema.GroupVersionResource
	err     error
	expires time.Time
}

// routeGVR finds the served version of a Gateway API route kind (TCPRoute and
// UDPRoute moved from v1alpha2 to v1). Answers are cached for
// routeDiscoveryTTL; before, every flow ran discovery on every sync.
func (k *Kube) routeGVR(resource string) (schema.GroupVersionResource, error) {
	k.routeMu.Lock()
	defer k.routeMu.Unlock()
	if cached, ok := k.routeGVRs[resource]; ok && time.Now().Before(cached.expires) {
		return cached.gvr, cached.err
	}
	answer := routeDiscovery{expires: time.Now().Add(routeDiscoveryTTL)}
	answer.gvr, answer.err = k.discoverRoute(resource)
	if k.routeGVRs == nil {
		k.routeGVRs = map[string]routeDiscovery{}
	}
	k.routeGVRs[resource] = answer
	return answer.gvr, answer.err
}

func (k *Kube) discoverRoute(resource string) (schema.GroupVersionResource, error) {
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

// getRoute reads a route: from the dynamic informer cache when the driver
// runs with informers, otherwise from the API.
func (k *Kube) getRoute(ctx context.Context, gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error) {
	if k.informers != nil {
		lister, err := k.informers.Lister(ctx, gvr)
		if err == nil {
			obj, err := lister.Get(name)
			if err != nil {
				return nil, err
			}
			route, ok := obj.(*unstructured.Unstructured)
			if !ok {
				return nil, fmt.Errorf("unexpected %T in %s cache", obj, gvr)
			}
			return route, nil
		}
	}
	ctx, cancel := apiCall(ctx)
	defer cancel()
	return k.dyn.Resource(gvr).Namespace(k.namespace).Get(ctx, name, metav1.GetOptions{})
}

func routeKind(network string) (kind, resource string) {
	if transportOf(network) == corev1.ProtocolUDP { // udp and quic
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

func (k *Kube) reconcileGatewayRoute(ctx context.Context, def spec.FlowDefinition) error {
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
		if err := k.ensureGatewayListener(ctx, gw.Namespace, gw.Name, listener, string(transportOf(listen.Network)), kind, port); err != nil {
			return err
		}
		managed = gw.Namespace + "/" + gw.Name + "/" + listener
	}
	name := podName(def.Name)
	metadata := map[string]any{"name": name, "namespace": k.namespace,
		"labels": map[string]any{kubeLabelManaged: "true", kubeLabelListener: def.Name}}
	if owner := k.ownerOf(def.Name); len(owner) > 0 {
		metadata["ownerReferences"] = []any{map[string]any{"apiVersion": owner[0].APIVersion, "kind": owner[0].Kind, "name": owner[0].Name, "uid": string(owner[0].UID)}}
	}
	route := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvr.Group + "/" + gvr.Version, "kind": kind,
		"metadata": metadata,
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"group": gatewayGroup, "kind": "Gateway", "namespace": gw.Namespace, "name": gw.Name, "sectionName": listener}},
			"rules":      []any{map[string]any{"backendRefs": []any{map[string]any{"name": name, "port": int64(servicePort)}}}},
		},
	}}
	if managed != "" {
		route.SetAnnotations(map[string]string{annotationManagedListener: managed})
	}
	routes := k.dyn.Resource(gvr).Namespace(k.namespace)
	current, err := k.getRoute(ctx, gvr, name)
	if apierrors.IsNotFound(err) {
		createCtx, cancel := apiCall(ctx)
		defer cancel()
		_, err = routes.Create(createCtx, route, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if current.GetLabels()[kubeLabelManaged] != "true" {
		return fmt.Errorf("%s %q exists and is not managed by GoFi", kind, name)
	}
	if routeEqual(current, route) {
		return nil
	}
	route.SetResourceVersion(current.GetResourceVersion())
	updateCtx, cancel := apiCall(ctx)
	defer cancel()
	_, err = routes.Update(updateCtx, route, metav1.UpdateOptions{})
	return err
}

// routeEqual compares the parts of a route GoFi writes (the server adds
// status and defaulted fields GoFi must not fight over).
func routeEqual(current, desired *unstructured.Unstructured) bool {
	currentSpec, _, _ := unstructured.NestedMap(current.Object, "spec")
	desiredSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	if normalizeJSON([]any{currentSpec}) != normalizeJSON([]any{desiredSpec}) {
		return false
	}
	if !apiequality.Semantic.DeepEqual(current.GetLabels(), desired.GetLabels()) {
		return false
	}
	if current.GetAnnotations()[annotationManagedListener] != desired.GetAnnotations()[annotationManagedListener] {
		return false
	}
	return ownerNames(current.GetOwnerReferences()) == ownerNames(desired.GetOwnerReferences())
}

func ownerNames(refs []metav1.OwnerReference) string {
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, ref.Kind+"/"+ref.Name+"/"+string(ref.UID))
	}
	return strings.Join(parts, ",")
}

// ensureGatewayListener adds (or updates) a gofi-* listener on a Gateway.
// It never edits listeners GoFi did not add, and refuses a port another
// listener already uses for the same protocol.
func (k *Kube) ensureGatewayListener(ctx context.Context, namespace, name, listener, protocol, routeKind string, port int) error {
	gateways := k.dyn.Resource(gatewaysGVR).Namespace(namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		getCtx, cancel := apiCall(ctx)
		gw, err := gateways.Get(getCtx, name, metav1.GetOptions{})
		cancel()
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
		mine := 0
		for i, raw := range listeners {
			existing, _ := raw.(map[string]any)
			existingName := fmt.Sprint(existing["name"])
			if existingName == listener {
				index = i
				continue
			}
			if strings.HasPrefix(existingName, clusterinfo.ListenerPrefix) {
				mine++
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
			// Implementations cap a Gateway's listeners; refusing here reports a
			// Problem on this flow instead of breaking the Gateway for all.
			if k.gatewayMaxListeners > 0 && mine >= k.gatewayMaxListeners {
				return fmt.Errorf("Gateway %s/%s already carries %d GoFi listeners (chart gateway.maxListeners); expose this flow through another Gateway or a LoadBalancer Service", namespace, name, mine)
			}
			listeners = append(listeners, desired)
		}
		if err := unstructured.SetNestedSlice(gw.Object, listeners, "spec", "listeners"); err != nil {
			return err
		}
		updateCtx, cancel := apiCall(ctx)
		defer cancel()
		_, err = gateways.Update(updateCtx, gw, metav1.UpdateOptions{})
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
		getCtx, cancel := apiCall(ctx)
		gw, err := gateways.Get(getCtx, parts[1], metav1.GetOptions{})
		cancel()
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
		updateCtx, cancel := apiCall(ctx)
		defer cancel()
		_, err = gateways.Update(updateCtx, gw, metav1.UpdateOptions{})
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
		route, err := k.getRoute(ctx, gvr, podName(name))
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
		deleteCtx, cancel := apiCall(ctx)
		err = routes.Delete(deleteCtx, route.GetName(), metav1.DeleteOptions{})
		cancel()
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}
