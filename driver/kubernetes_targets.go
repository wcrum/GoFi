package driver

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/wcrum/gofi/kubeclient"
	"github.com/wcrum/gofi/spec"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const kubeLabelTarget = "gofi.io/target"
const MaxResolvedTargets = 100

// mergePatch is the patch type for small metadata patches.
const mergePatch = types.MergePatchType

func (k *Kube) validateTarget(target *spec.KubernetesTarget, network string) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if !isNetworkTarget(network) {
		return fmt.Errorf("target network must be tcp, udp, or quic")
	}
	return k.AllowsTargetNamespace(target.Namespace)
}

// AllowsTargetNamespace reports whether selector targets may use a namespace:
// GoFi's own always, others only with cluster-wide discovery (to read Pods).
func (k *Kube) AllowsTargetNamespace(namespace string) error {
	if namespace == "" || namespace == k.namespace {
		return nil
	}
	if k.cluster != nil && k.cluster.Opts.ClusterWide {
		return nil
	}
	return fmt.Errorf("Kubernetes targets outside namespace %q need cluster discovery (chart discovery.clusterWide)", k.namespace)
}

// targetNamespace is where a target's Pods live.
func (k *Kube) targetNamespace(target *spec.KubernetesTarget) string {
	if target.Namespace == "" {
		return k.namespace
	}
	return target.Namespace
}
func podReady(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning || net.ParseIP(pod.Status.PodIP) == nil {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
func (k *Kube) ResolveTargets(ctx context.Context, target spec.KubernetesTarget, network string) ([]Endpoint, error) {
	if err := k.validateTarget(&target, network); err != nil {
		return nil, err
	}
	ctx, cancel := apiCall(ctx)
	defer cancel()
	pods, err := k.cs.CoreV1().Pods(k.targetNamespace(&target)).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(target.Selector).String(), Limit: MaxResolvedTargets + 1})
	if err != nil {
		return nil, err
	}
	endpoints := []Endpoint{}
	for _, pod := range pods.Items {
		if !podReady(&pod) {
			continue
		}
		endpoints = append(endpoints, Endpoint{Name: pod.Name, Namespace: pod.Namespace, Kind: "kubernetes", Type: "Pod", Host: pod.Status.PodIP, Labels: pod.Labels, Ready: true, Ports: []EndpointPort{{Port: int32(target.Port), Protocol: string(transportOf(network)), URL: network + "://" + net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(target.Port))}}})
		if len(endpoints) > MaxResolvedTargets {
			return nil, fmt.Errorf("selector matches more than %d ready Pods; narrow the selector", MaxResolvedTargets)
		}
	}
	// A paginated list cannot safely be replayed as a silently partial target set.
	if pods.Continue != "" {
		return nil, fmt.Errorf("selector target inventory exceeds the %d Pod limit; narrow the selector", MaxResolvedTargets)
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].Name < endpoints[j].Name })
	return endpoints, nil
}

// targetSelector labels the target Services of one flow.
func targetSelector(flow string) map[string]string {
	return map[string]string{kubeLabelManaged: "true", kubeLabelListener: flow, kubeLabelTarget: "true"}
}

func (k *Kube) reconcileTargetServices(ctx context.Context, def spec.FlowDefinition) error {
	type destination struct {
		name string
		cfg  spec.SinkCfg
	}
	destinations := make([]destination, 0, len(def.Sinks)+1)
	for _, sink := range def.Sinks {
		if sink.Kubernetes != nil {
			destinations = append(destinations, destination{def.Name, sink})
		}
	}
	if def.DeadLetterSink != nil && def.DeadLetterSink.Kubernetes != nil {
		destinations = append(destinations, destination{def.Name + "-deadletter", *def.DeadLetterSink})
	}
	// A flow without Kubernetes destinations lists its target Services only
	// when that costs nothing (the cache); through the API it would be one
	// list per flow per sync for an empty result. Stale target Services of
	// such a flow are removed by the flow-set reconcile and by Delete.
	if len(destinations) == 0 && !k.read().cached() {
		return nil
	}
	wanted := map[string]bool{}
	services := k.cs.CoreV1().Services(k.namespace)
	for _, destination := range destinations {
		cfg := destination.cfg
		if err := k.validateTarget(cfg.Kubernetes, cfg.Type); err != nil {
			return err
		}
		name := spec.KubernetesServiceName(destination.name, cfg.Type, cfg.Kubernetes)
		wanted[name] = true
		desired := targetService(name, k.namespace, def.Name, k.ownerOf(def.Name), cfg)
		// Pods in another namespace cannot be selected by a Service here:
		// GoFi publishes their addresses in an EndpointSlice instead.
		remote := k.targetNamespace(cfg.Kubernetes) != k.namespace
		if remote {
			desired.Spec.Selector = nil
		}
		current, err := k.read().service(ctx, name)
		if err == nil && (current.Spec.ClusterIP == corev1.ClusterIPNone) != (desired.Spec.ClusterIP == corev1.ClusterIPNone) {
			// clusterIP is immutable: switching delivery mode recreates the Service.
			deleteCtx, cancel := apiCall(ctx)
			err = services.Delete(deleteCtx, name, metav1.DeleteOptions{})
			cancel()
			if err != nil && !apierrors.IsNotFound(err) {
				return err
			}
			err = apierrors.NewNotFound(corev1.Resource("services"), name)
		}
		var service *corev1.Service
		switch {
		case apierrors.IsNotFound(err):
			createCtx, cancel := apiCall(ctx)
			service, err = services.Create(createCtx, desired, metav1.CreateOptions{})
			cancel()
			if apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("kube driver: selector target service %q exists and is unmanaged", name)
			}
			if err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if current.Labels[kubeLabelManaged] != "true" || current.Labels[kubeLabelListener] != def.Name || current.Labels[kubeLabelTarget] != "true" {
				return fmt.Errorf("kube driver: selector target service %q exists and is unmanaged", name)
			}
			merged := current.DeepCopy()
			merged.Spec.Type = desired.Spec.Type
			merged.Spec.Selector = desired.Spec.Selector
			merged.Spec.Ports = desired.Spec.Ports
			merged.OwnerReferences = desired.OwnerReferences
			service = merged
			if !serviceEqual(current, merged) {
				updateCtx, cancel := apiCall(ctx)
				service, err = services.Update(updateCtx, merged, metav1.UpdateOptions{})
				cancel()
				if err != nil {
					return err
				}
			}
		}
		if remote {
			if err := k.reconcileTargetEndpoints(ctx, service, cfg); err != nil {
				return err
			}
		}
	}
	existing, err := k.read().services(ctx, targetSelector(def.Name))
	if err != nil {
		return err
	}
	for _, service := range existing {
		if !wanted[service.Name] {
			deleteCtx, cancel := apiCall(ctx)
			err := services.Delete(deleteCtx, service.Name, metav1.DeleteOptions{})
			cancel()
			if err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}
func (k *Kube) deleteTargetServices(ctx context.Context, name string) error {
	current, err := k.read().services(ctx, targetSelector(name))
	if err != nil {
		return err
	}
	for _, service := range current {
		deleteCtx, cancel := apiCall(ctx)
		err := k.cs.CoreV1().Services(k.namespace).Delete(deleteCtx, service.Name, metav1.DeleteOptions{})
		cancel()
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// podEndpoints lists the namespace's Pods as destinations, paging through
// the API and stopping at maxDiscoveryItems with a warning.
func (k *Kube) podEndpoints(ctx context.Context) ([]Endpoint, error) {
	pods, truncated, err := kubeclient.ListAll(ctx, maxDiscoveryItems, "", func(ctx context.Context, opts metav1.ListOptions) ([]corev1.Pod, string, error) {
		ctx, cancel := apiCall(ctx)
		defer cancel()
		list, err := k.cs.CoreV1().Pods(k.namespace).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return list.Items, list.Continue, nil
	})
	if err != nil {
		return nil, err
	}
	if truncated {
		slog.Warn("kube driver: " + kubeclient.TruncationWarning("Pod discovery", maxDiscoveryItems))
	}
	endpoints := make([]Endpoint, 0, len(pods))
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			continue
		}
		endpoint := Endpoint{Name: pod.Name, Namespace: pod.Namespace, Kind: "kubernetes", Type: "Pod", Host: pod.Status.PodIP, Labels: pod.Labels, Ready: podReady(&pod), Ports: []EndpointPort{}}
		if pod.Status.PodIP != "" {
			for _, container := range pod.Spec.Containers {
				for _, port := range container.Ports {
					protocol := port.Protocol
					if protocol == "" {
						protocol = corev1.ProtocolTCP
					}
					if protocol != corev1.ProtocolTCP && protocol != corev1.ProtocolUDP {
						continue
					}
					endpoint.Ports = append(endpoint.Ports, EndpointPort{Name: port.Name, Port: port.ContainerPort, Protocol: string(protocol), URL: strings.ToLower(string(protocol)) + "://" + net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(int(port.ContainerPort)))})
				}
			}
		}
		endpoints = append(endpoints, endpoint)
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].Name < endpoints[j].Name })
	return endpoints, nil
}

// targetService is the Service a selector destination sends to: headless for
// fan-out delivery (DNS returns every Pod), ClusterIP for load-balanced. It
// is owned by the Flow (not the runtime Pod) so it survives Pod replacement.
func targetService(name, namespace, flow string, owner []metav1.OwnerReference, cfg spec.SinkCfg) *corev1.Service {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace,
		Labels:          targetSelector(flow),
		OwnerReferences: owner},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: cfg.Kubernetes.Selector,
			Ports: []corev1.ServicePort{{Name: "data", Protocol: transportOf(cfg.Type), Port: int32(cfg.Kubernetes.Port), TargetPort: intstr.FromInt(cfg.Kubernetes.Port)}}}}
	if cfg.Kubernetes.FanOut() {
		svc.Spec.ClusterIP = corev1.ClusterIPNone
		// Unready Pods are skipped by DNS; publish only ready ones.
		svc.Spec.PublishNotReadyAddresses = false
	}
	return svc
}

// reconcileTargetEndpoints publishes the ready Pods of a target in another
// namespace as the EndpointSlice of its selector-less Service here.
func (k *Kube) reconcileTargetEndpoints(ctx context.Context, service *corev1.Service, cfg spec.SinkCfg) error {
	pods, err := k.ResolveTargets(ctx, *cfg.Kubernetes, cfg.Type)
	if err != nil {
		return err
	}
	protocol := transportOf(cfg.Type) // quic targets carry UDP ports
	port, portName := int32(cfg.Kubernetes.Port), "data"
	ready := true
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: service.Name, Namespace: k.namespace,
			Labels:          map[string]string{discoveryv1.LabelServiceName: service.Name, discoveryv1.LabelManagedBy: "gofi.wcrum.dev", kubeLabelManaged: "true", kubeLabelTarget: "true"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Service", Name: service.Name, UID: service.UID}}},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       []discoveryv1.EndpointPort{{Name: &portName, Port: &port, Protocol: &protocol}},
		Endpoints:   []discoveryv1.Endpoint{},
	}
	for _, pod := range pods {
		if net.ParseIP(pod.Host).To4() == nil {
			continue
		}
		name := pod.Name
		slice.Endpoints = append(slice.Endpoints, discoveryv1.Endpoint{Addresses: []string{pod.Host}, Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: pod.Namespace, Name: name}})
	}
	slices := k.cs.DiscoveryV1().EndpointSlices(k.namespace)
	getCtx, cancel := apiCall(ctx)
	current, err := slices.Get(getCtx, slice.Name, metav1.GetOptions{})
	cancel()
	if apierrors.IsNotFound(err) {
		createCtx, cancel := apiCall(ctx)
		defer cancel()
		_, err = slices.Create(createCtx, slice, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if current.Labels[discoveryv1.LabelManagedBy] != "gofi.wcrum.dev" {
		return fmt.Errorf("kube driver: EndpointSlice %q exists and is not managed by GoFi", slice.Name)
	}
	if apiequality.Semantic.DeepEqual(current.Endpoints, slice.Endpoints) && apiequality.Semantic.DeepEqual(current.Ports, slice.Ports) {
		return nil
	}
	current.Endpoints, current.Ports, current.Labels, current.OwnerReferences = slice.Endpoints, slice.Ports, slice.Labels, slice.OwnerReferences
	updateCtx, cancel := apiCall(ctx)
	defer cancel()
	_, err = slices.Update(updateCtx, current, metav1.UpdateOptions{})
	return err
}
