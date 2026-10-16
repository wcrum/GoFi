package driver

import (
	"context"
	"fmt"
	"testing"

	"github.com/wcrum/gofi/clusterinfo"
	"github.com/wcrum/gofi/spec"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func receiverPod(name, ip string, ready bool) *corev1.Pod {
	condition := corev1.ConditionFalse
	if ready {
		condition = corev1.ConditionTrue
	}
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "apps", Labels: map[string]string{"app": "receiver", "tier": "backend"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: ip, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: condition}}}}
}
func TestKubernetesSelectorResolutionIsScopedReadyAndBounded(t *testing.T) {
	ctx := context.Background()
	k, cs := newTestKube("apps")
	for _, pod := range []*corev1.Pod{receiverPod("first", "10.0.0.1", true), receiverPod("second", "10.0.0.2", true), receiverPod("not-ready", "10.0.0.3", false)} {
		if _, err := cs.CoreV1().Pods("apps").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	target := spec.KubernetesTarget{Selector: map[string]string{"app": "receiver"}, Port: 7777}
	endpoints, err := k.ResolveTargets(ctx, target, "udp")
	if err != nil || len(endpoints) != 2 {
		t.Fatalf("targets: %+v %v", endpoints, err)
	}
	if endpoints[0].Name != "first" || endpoints[0].Ports[0].URL != "udp://10.0.0.1:7777" || endpoints[0].Labels["tier"] != "backend" || !endpoints[0].Ready {
		t.Fatalf("target contract: %+v", endpoints[0])
	}
	target.Namespace = "outside"
	if _, err := k.ResolveTargets(ctx, target, "tcp"); err == nil {
		t.Fatal("cross-namespace selector accepted")
	}
	target.Namespace = ""
	target.Selector = nil
	if _, err := k.ResolveTargets(ctx, target, "tcp"); err == nil {
		t.Fatal("empty selector accepted")
	}
	target.Selector = map[string]string{"app": "receiver"}
	for i := 0; i < 100; i++ {
		if _, err := cs.CoreV1().Pods("apps").Create(ctx, receiverPod(fmt.Sprintf("extra-%d", i), "10.0.1.1", true), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := k.ResolveTargets(ctx, target, "tcp"); err == nil {
		t.Fatal("unbounded selector accepted")
	}
}
func TestKubernetesSelectorServicesPreserveDesiredDefinitionAndRepairDrift(t *testing.T) {
	ctx := context.Background()
	k, cs := newTestKube("apps")
	def := testDef("selected", 9000)
	target := &spec.KubernetesTarget{Selector: map[string]string{"app": "receiver"}, Port: 7777}
	def.Sinks = []spec.SinkCfg{{Type: "tcp", Kubernetes: target}}
	if err := k.Create(ctx, def); err != nil {
		t.Fatal(err)
	}
	name := spec.KubernetesServiceName(def.Name, "tcp", target)
	service, err := cs.CoreV1().Services("apps").Get(ctx, name, metav1.GetOptions{})
	if err != nil || service.Spec.Selector["app"] != "receiver" || service.Spec.Ports[0].TargetPort.IntValue() != 7777 || service.Labels[kubeLabelTarget] != "true" {
		t.Fatalf("selector Service: %+v %v", service, err)
	}
	pod, err := cs.CoreV1().Pods("apps").Get(ctx, podName(def.Name), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	original, ok := decodePodListenerSpec(pod)
	if !ok || original.Sinks[0].URL != "" || original.Sinks[0].Kubernetes.Selector["app"] != "receiver" {
		t.Fatalf("desired definition rewritten: %+v", original)
	}
	foundNamespace := false
	for _, env := range pod.Spec.Containers[0].Env {
		if env.Name == "GOFI_K8S_NAMESPACE" && env.Value == "apps" {
			foundNamespace = true
		}
	}
	if !foundNamespace {
		t.Fatal("listener missing target namespace binding")
	}
	service.Spec.Selector = map[string]string{"wrong": "destination"}
	if _, err := cs.CoreV1().Services("apps").Update(ctx, service, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	pod.Status.Phase = corev1.PodRunning
	if _, err := cs.CoreV1().Pods("apps").UpdateStatus(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := k.List(ctx); err != nil {
		t.Fatal(err)
	}
	service, err = cs.CoreV1().Services("apps").Get(ctx, name, metav1.GetOptions{})
	if err != nil || service.Spec.Selector["app"] != "receiver" {
		t.Fatalf("selector drift not repaired: %+v %v", service, err)
	}
	if err := k.Delete(ctx, def.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().Services("apps").Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("target Service leaked: %v", err)
	}
}
func TestKubernetesRejectsCrossNamespaceTargetBeforePodCreation(t *testing.T) {
	ctx := context.Background()
	k, cs := newTestKube("apps")
	def := testDef("rejected", 9000)
	def.Sinks = []spec.SinkCfg{{Type: "tcp", Kubernetes: &spec.KubernetesTarget{Namespace: "outside", Selector: map[string]string{"app": "receiver"}, Port: 7777}}}
	if err := k.Create(ctx, def); err == nil {
		t.Fatal("cross namespace target accepted")
	}
	pods, err := cs.CoreV1().Pods("apps").List(ctx, metav1.ListOptions{})
	if err != nil || len(pods.Items) != 0 {
		t.Fatalf("invalid definition created a Pod: %+v %v", pods, err)
	}
}

func TestCrossNamespaceTargetPublishesEndpointSliceWithClusterDiscovery(t *testing.T) {
	ctx := context.Background()
	k, cs := newTestKube("gofi")
	target := &spec.KubernetesTarget{Namespace: "apps", Selector: map[string]string{"app": "receiver"}, Port: 7777}
	if err := k.AllowsTargetNamespace("apps"); err == nil {
		t.Fatal("another namespace allowed without cluster discovery")
	}
	k.cluster = clusterinfo.New(cs, nil, clusterinfo.Options{Namespace: "gofi", ClusterWide: true})
	for _, pod := range []*corev1.Pod{receiverPod("first", "10.0.0.1", true), receiverPod("second", "10.0.0.2", true), receiverPod("not-ready", "10.0.0.3", false)} {
		if _, err := cs.CoreV1().Pods("apps").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	def := testDef("fanout", 9000)
	def.Sinks = []spec.SinkCfg{{Type: "udp", Kubernetes: target}}
	if err := k.Create(ctx, def); err != nil {
		t.Fatal(err)
	}
	name := spec.KubernetesServiceName(def.Name, "udp", target)
	service, err := cs.CoreV1().Services("gofi").Get(ctx, name, metav1.GetOptions{})
	if err != nil || service.Spec.Selector != nil || service.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Fatalf("want a selector-less headless Service: %+v %v", service, err)
	}
	slice, err := cs.DiscoveryV1().EndpointSlices("gofi").Get(ctx, name, metav1.GetOptions{})
	if err != nil || len(slice.Endpoints) != 2 || slice.Endpoints[0].Addresses[0] != "10.0.0.1" || *slice.Ports[0].Port != 7777 || slice.Labels["kubernetes.io/service-name"] != name {
		t.Fatalf("EndpointSlice: %+v %v", slice, err)
	}
	// A Pod going away is reflected on the next reconcile of the running flow.
	if err := cs.CoreV1().Pods("apps").Delete(ctx, "second", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	runtime, _ := cs.CoreV1().Pods("gofi").Get(ctx, podName(def.Name), metav1.GetOptions{})
	runtime.Status.Phase = corev1.PodRunning
	if _, err := cs.CoreV1().Pods("gofi").UpdateStatus(ctx, runtime, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := k.List(ctx); err != nil {
		t.Fatal(err)
	}
	slice, _ = cs.DiscoveryV1().EndpointSlices("gofi").Get(ctx, name, metav1.GetOptions{})
	if len(slice.Endpoints) != 1 {
		t.Fatalf("EndpointSlice not refreshed: %+v", slice.Endpoints)
	}
}
