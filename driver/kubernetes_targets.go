package driver

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/wcrum/gofi/spec"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const kubeLabelTarget = "gofi.io/target"
const MaxResolvedTargets = 100

func (k *Kube) Namespace() string { return k.namespace }
func (k *Kube) validateTarget(target *spec.KubernetesTarget, network string) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if network != "tcp" && network != "udp" {
		return fmt.Errorf("target network must be tcp or udp")
	}
	if target.Namespace != "" && target.Namespace != k.namespace {
		return fmt.Errorf("Kubernetes target namespace must be %q", k.namespace)
	}
	return nil
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
	pods, err := k.cs.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(target.Selector).String(), Limit: MaxResolvedTargets + 1})
	if err != nil {
		return nil, err
	}
	endpoints := []Endpoint{}
	for _, pod := range pods.Items {
		if !podReady(&pod) {
			continue
		}
		endpoints = append(endpoints, Endpoint{Name: pod.Name, Namespace: pod.Namespace, Kind: "kubernetes", Type: "Pod", Host: pod.Status.PodIP, Labels: pod.Labels, Ready: true, Ports: []EndpointPort{{Port: int32(target.Port), Protocol: strings.ToUpper(network), URL: network + "://" + net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(target.Port))}}})
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

func (k *Kube) reconcileTargetServices(ctx context.Context, def spec.ListenerDefinition, pod *corev1.Pod) error {
	type destination struct {
		name string
		cfg  spec.SinkCfg
	}
	destinations := make([]destination, 0, len(def.Sinks)+1)
	for _, sink := range def.Sinks {
		destinations = append(destinations, destination{def.Name, sink})
	}
	if def.DeadLetterSink != nil {
		destinations = append(destinations, destination{def.Name + "-deadletter", *def.DeadLetterSink})
	}
	wanted := map[string]bool{}
	services := k.cs.CoreV1().Services(k.namespace)
	for _, destination := range destinations {
		cfg := destination.cfg
		if cfg.Kubernetes == nil {
			continue
		}
		if err := k.validateTarget(cfg.Kubernetes, cfg.Type); err != nil {
			return err
		}
		name := spec.KubernetesServiceName(destination.name, cfg.Type, cfg.Kubernetes)
		wanted[name] = true
		desired := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.namespace, Labels: map[string]string{kubeLabelManaged: "true", kubeLabelListener: def.Name, kubeLabelTarget: "true"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: pod.Name, UID: pod.UID}}}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: cfg.Kubernetes.Selector, Ports: []corev1.ServicePort{{Name: "data", Protocol: corev1.Protocol(strings.ToUpper(cfg.Type)), Port: int32(cfg.Kubernetes.Port), TargetPort: intstr.FromInt(cfg.Kubernetes.Port)}}}}
		current, err := services.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if _, err := services.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if current.Labels[kubeLabelManaged] != "true" || current.Labels[kubeLabelListener] != def.Name || current.Labels[kubeLabelTarget] != "true" {
			return fmt.Errorf("kube driver: selector target service %q exists and is unmanaged", name)
		}
		current.Spec.Type = desired.Spec.Type
		current.Spec.Selector = desired.Spec.Selector
		current.Spec.Ports = desired.Spec.Ports
		current.OwnerReferences = desired.OwnerReferences
		if _, err := services.Update(ctx, current, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	existing, err := services.List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(map[string]string{kubeLabelManaged: "true", kubeLabelListener: def.Name, kubeLabelTarget: "true"}).String()})
	if err != nil {
		return err
	}
	for _, service := range existing.Items {
		if !wanted[service.Name] {
			if err := services.Delete(ctx, service.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}
func (k *Kube) deleteTargetServices(ctx context.Context, name string) error {
	services := k.cs.CoreV1().Services(k.namespace)
	current, err := services.List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(map[string]string{kubeLabelManaged: "true", kubeLabelListener: name, kubeLabelTarget: "true"}).String()})
	if err != nil {
		return err
	}
	for _, service := range current.Items {
		if err := services.Delete(ctx, service.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (k *Kube) podEndpoints(ctx context.Context) ([]Endpoint, error) {
	pods, err := k.cs.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		return nil, err
	}
	if pods.Continue != "" {
		return nil, fmt.Errorf("Pod discovery exceeds 500 entries in the controller namespace")
	}
	endpoints := make([]Endpoint, 0, len(pods.Items))
	for _, pod := range pods.Items {
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
