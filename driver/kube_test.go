package driver

import (
	"context"
	"encoding/json"
	"github.com/wcrum/gofi/spec"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubeNodePortServiceLifecycleAndDrift(t *testing.T) {
	k, cs := newTestKube("default")
	ctx := context.Background()
	def := testDef("udp-route", 9000)
	def.Source.Listen.Network = "udp"
	def.Source.Listen.Service = &spec.ServiceExposure{Type: "NodePort", NodePort: 31000, Port: 8000}
	if err := k.Create(ctx, def); err != nil {
		t.Fatal(err)
	}
	svc, err := cs.CoreV1().Services("default").Get(ctx, podName(def.Name), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Spec.Type != corev1.ServiceTypeNodePort || svc.Spec.Ports[0].NodePort != 31000 || svc.Spec.Ports[0].TargetPort.IntValue() != 9000 || svc.Spec.Ports[0].Protocol != corev1.ProtocolUDP {
		t.Fatalf("unexpected service: %+v", svc.Spec)
	}
	svc.Spec.Ports[0].Port = 1234
	if _, err := cs.CoreV1().Services("default").Update(ctx, svc, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	pod, _ := cs.CoreV1().Pods("default").Get(ctx, podName(def.Name), metav1.GetOptions{})
	pod.Status.Phase = corev1.PodRunning
	if _, err := cs.CoreV1().Pods("default").UpdateStatus(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := k.List(ctx); err != nil {
		t.Fatal(err)
	}
	svc, _ = cs.CoreV1().Services("default").Get(ctx, podName(def.Name), metav1.GetOptions{})
	if svc.Spec.Ports[0].Port != 8000 {
		t.Fatal("service drift not repaired")
	}
	if err := k.Delete(ctx, def.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().Services("default").Get(ctx, podName(def.Name), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("service not deleted: %v", err)
	}
}

func TestKubeCreateRollsBackPodOnServiceConflict(t *testing.T) {
	k, cs := newTestKube("default")
	ctx := context.Background()
	def := testDef("conflict", 9000)
	def.Source.Listen.Service = &spec.ServiceExposure{Type: "ClusterIP"}
	_, err := cs.CoreV1().Services("default").Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: podName(def.Name)}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Create(ctx, def); err == nil {
		t.Fatal("expected unmanaged service conflict")
	}
	if _, err := cs.CoreV1().Pods("default").Get(ctx, podName(def.Name), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("partially created pod not rolled back")
	}
}

func TestKubeDiscoversLoadBalancerEndpoints(t *testing.T) {
	k, cs := newTestKube("apps")
	ctx := context.Background()
	_, err := cs.CoreV1().Services("apps").Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "receiver", Namespace: "apps"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, ClusterIP: "10.0.0.1", Ports: []corev1.ServicePort{{Port: 8080, Protocol: corev1.ProtocolTCP, NodePort: 31080}}}, Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "192.0.2.10"}}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := k.Endpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0].Host != "receiver.apps.svc.cluster.local" || endpoints[0].Ports[0].URL != "tcp://receiver.apps.svc.cluster.local:8080" || endpoints[0].ExternalAddresses[0] != "192.0.2.10" {
		t.Fatalf("unexpected discovery: %+v", endpoints)
	}
}

func newTestKube(namespace string) (*Kube, *fake.Clientset) {
	cs := fake.NewSimpleClientset()
	return &Kube{cs: cs, namespace: namespace, image: "gofi:test"}, cs
}

func TestKubeCreateSetsLabelsAndEnv(t *testing.T) {
	k, cs := newTestKube("default")
	def := testDef("cot-ingest", 9000)

	if err := k.Create(context.Background(), def); err != nil {
		t.Fatalf("Create: %v", err)
	}

	pod, err := cs.CoreV1().Pods("default").Get(context.Background(), "gofi-listener-cot-ingest", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get pod: %v", err)
	}
	if pod.Labels[kubeLabelListener] != "cot-ingest" {
		t.Errorf("label %s = %q, want cot-ingest", kubeLabelListener, pod.Labels[kubeLabelListener])
	}
	if pod.Labels[kubeLabelManaged] != "true" {
		t.Errorf("label %s = %q, want true", kubeLabelManaged, pod.Labels[kubeLabelManaged])
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %q, want Never", pod.Spec.RestartPolicy)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(pod.Spec.Containers))
	}
	if pod.Spec.Containers[0].Image != "gofi:test" {
		t.Errorf("image = %q, want gofi:test", pod.Spec.Containers[0].Image)
	}
	var gotEnv string
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Name == "FLOW_SPEC" {
			gotEnv = e.Value
		}
	}
	if gotEnv == "" {
		t.Fatal("FLOW_SPEC env var not set")
	}
	if pod.Spec.SecurityContext.RunAsUser == nil || *pod.Spec.SecurityContext.RunAsUser != 65532 || pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("listener pod must run unprivileged without Kubernetes credentials")
	}
}

func TestKubeListenerPodTemplate(t *testing.T) {
	template, err := parseListenerPodTemplate(`{"containers":[{"name":"aster-agent","image":"example.test/aster-agent:1.2.3","volumeMounts":[{"name":"state","mountPath":"/state"}]}],"volumes":[{"name":"state","emptyDir":{}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	k, cs := newTestKube("default")
	k.podTemplate = template
	ctx := context.Background()
	if err := k.Create(ctx, testDef("with-agent", 9000)); err != nil {
		t.Fatal(err)
	}
	pod, err := cs.CoreV1().Pods("default").Get(ctx, podName("with-agent"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pod.Spec.Containers) != 2 || pod.Spec.Containers[0].Name != "gofi-listener" || pod.Spec.Containers[1].Name != "aster-agent" || len(pod.Spec.Volumes) != 1 {
		t.Fatalf("template not injected: %+v", pod.Spec)
	}
	for _, raw := range []string{`{"containers":[{"name":"gofi-listener","image":"bad"}]}`, `{"containers":[{"name":"agent"}]}`, `{"volumes":[{"name":"state"},{"name":"state"}]}`, `invalid`} {
		if _, err := parseListenerPodTemplate(raw); err == nil {
			t.Errorf("accepted invalid template %s", raw)
		}
	}
}

func TestKubeListFiltersByLabelAndPhase(t *testing.T) {
	k, cs := newTestKube("default")
	ctx := context.Background()

	def := testDef("cot-ingest", 9000)
	if err := k.Create(ctx, def); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// fake clientset doesn't set a Phase by default; set it to Running so
	// List's phase filter includes it.
	pod, err := cs.CoreV1().Pods("default").Get(ctx, "gofi-listener-cot-ingest", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get pod: %v", err)
	}
	pod.Status.Phase = corev1.PodRunning
	pod.Status.PodIP = "10.0.0.5"
	if _, err := cs.CoreV1().Pods("default").UpdateStatus(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	// An unrelated, unmanaged pod should be excluded.
	unmanaged := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "x", Image: "x"}},
		},
	}
	if _, err := cs.CoreV1().Pods("default").Create(ctx, unmanaged, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create unmanaged pod: %v", err)
	}

	// A managed pod that's still Pending should also be included.
	pendingDef := testDef("pending-one", 9001)
	if err := k.Create(ctx, pendingDef); err != nil {
		t.Fatalf("Create pending: %v", err)
	}
	pendingPod, err := cs.CoreV1().Pods("default").Get(ctx, podName("pending-one"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get pending pod: %v", err)
	}
	pendingPod.Status.Phase = corev1.PodPending
	if _, err := cs.CoreV1().Pods("default").UpdateStatus(ctx, pendingPod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("UpdateStatus pending pod: %v", err)
	}

	// A managed pod that's Failed should be excluded.
	failedDef := testDef("failed-one", 9002)
	if err := k.Create(ctx, failedDef); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	failedPod, err := cs.CoreV1().Pods("default").Get(ctx, podName("failed-one"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get failed pod: %v", err)
	}
	failedPod.Status.Phase = corev1.PodFailed
	if _, err := cs.CoreV1().Pods("default").UpdateStatus(ctx, failedPod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("UpdateStatus failed pod: %v", err)
	}

	got, err := k.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	names := map[string]bool{}
	for _, d := range got {
		names[d.Name] = true
	}
	if !names["cot-ingest"] {
		t.Error("expected cot-ingest (Running) to be listed")
	}
	if !names["pending-one"] {
		t.Error("expected pending-one (Pending) to be listed")
	}
	if names["failed-one"] {
		t.Error("did not expect failed-one (Failed) to be listed")
	}
	if len(got) != 2 {
		t.Errorf("expected exactly 2 listed defs, got %d: %+v", len(got), got)
	}
}

func TestKubeDeleteWaitsForRemoval(t *testing.T) {
	k, _ := newTestKube("default")
	ctx := context.Background()

	def := testDef("cot-ingest", 9000)
	if err := k.Create(ctx, def); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := k.Delete(ctx, "cot-ingest"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := k.cs.CoreV1().Pods("default").Get(ctx, podName("cot-ingest"), metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected pod to be gone after Delete, got err=%v", err)
	}
}

func TestKubeDeleteNotFoundIsSuccess(t *testing.T) {
	k, _ := newTestKube("default")
	if err := k.Delete(context.Background(), "nonexistent"); err != nil {
		t.Errorf("Delete on nonexistent pod should succeed, got %v", err)
	}
}

func TestKubeTapReturnsPodIPWhenRunning(t *testing.T) {
	k, cs := newTestKube("default")
	ctx := context.Background()

	def := testDef("cot-ingest", 9000)
	if err := k.Create(ctx, def); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Not running yet: Tap should report not-ok.
	_, ok, err := k.Tap(ctx, "cot-ingest")
	if err != nil {
		t.Fatalf("Tap: %v", err)
	}
	if ok {
		t.Error("expected Tap to report not-ok before pod is Running")
	}

	pod, err := cs.CoreV1().Pods("default").Get(ctx, podName("cot-ingest"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get pod: %v", err)
	}
	pod.Status.Phase = corev1.PodRunning
	pod.Status.PodIP = "10.1.2.3"
	if _, err := cs.CoreV1().Pods("default").UpdateStatus(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	target, ok, err := k.Tap(ctx, "cot-ingest")
	if err != nil || !ok {
		t.Fatalf("Tap: ok=%v err=%v", ok, err)
	}
	if target.Addr != "10.1.2.3:9091" {
		t.Errorf("Tap addr = %q, want 10.1.2.3:9091", target.Addr)
	}
}

func TestKubeTapNotFound(t *testing.T) {
	k, _ := newTestKube("default")
	_, ok, err := k.Tap(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Tap: %v", err)
	}
	if ok {
		t.Error("expected Tap to report not-ok for nonexistent pod")
	}
}

func TestKubeRuntimeStatusReportsPhaseAndNode(t *testing.T) {
	k, cs := newTestKube("default")
	ctx := context.Background()

	def := testDef("cot-ingest", 9000)
	if err := k.Create(ctx, def); err != nil {
		t.Fatalf("Create: %v", err)
	}

	pod, err := cs.CoreV1().Pods("default").Get(ctx, podName("cot-ingest"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get pod: %v", err)
	}
	startTime := metav1.NewTime(time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	pod.Status.Phase = corev1.PodRunning
	pod.Status.PodIP = "10.0.0.7"
	pod.Status.StartTime = &startTime
	pod.Spec.NodeName = "node-a"
	if _, err := cs.CoreV1().Pods("default").UpdateStatus(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	// NodeName is part of Spec, not Status; UpdateStatus won't persist it, so
	// set it via a full Update instead.
	pod2, err := cs.CoreV1().Pods("default").Get(ctx, podName("cot-ingest"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get pod: %v", err)
	}
	pod2.Spec.NodeName = "node-a"
	if _, err := cs.CoreV1().Pods("default").Update(ctx, pod2, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	info, ok, err := k.RuntimeStatus(ctx, "cot-ingest")
	if err != nil {
		t.Fatalf("RuntimeStatus: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for existing pod")
	}
	if info.Kind != "kubernetes" {
		t.Errorf("Kind = %q, want kubernetes", info.Kind)
	}
	if info.Status != string(corev1.PodRunning) {
		t.Errorf("Status = %q, want %q", info.Status, corev1.PodRunning)
	}
	if info.ID != podName("cot-ingest") {
		t.Errorf("ID = %q, want %q", info.ID, podName("cot-ingest"))
	}
	if info.Node != "node-a" {
		t.Errorf("Node = %q, want node-a", info.Node)
	}
	if info.IP != "10.0.0.7" {
		t.Errorf("IP = %q, want 10.0.0.7", info.IP)
	}
	if !info.StartedAt.Equal(startTime.Time) {
		t.Errorf("StartedAt = %v, want %v", info.StartedAt, startTime.Time)
	}
}

func TestKubeRuntimeStatusNotFound(t *testing.T) {
	k, _ := newTestKube("default")
	_, ok, err := k.RuntimeStatus(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("RuntimeStatus: %v", err)
	}
	if ok {
		t.Error("expected ok=false for nonexistent pod")
	}
}

func TestKubeKind(t *testing.T) {
	k, _ := newTestKube("default")
	if k.Kind() != "kubernetes" {
		t.Errorf("Kind() = %q, want kubernetes", k.Kind())
	}
}

func TestNewKubeRequiresImageAndNamespace(t *testing.T) {
	if _, err := NewKube("ns", ""); err == nil {
		t.Error("expected error when image is empty")
	}
	if _, err := NewKube("", "img"); err == nil {
		t.Error("expected error when namespace is empty")
	}
}

func TestDecodePodListenerSpec(t *testing.T) {
	def := testDef("x", 1)
	raw, _ := json.Marshal(def)
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Env: []corev1.EnvVar{{Name: "FLOW_SPEC", Value: string(raw)}}},
			},
		},
	}
	got, ok := decodePodListenerSpec(pod)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.Name != "x" {
		t.Errorf("Name = %q, want x", got.Name)
	}
}
