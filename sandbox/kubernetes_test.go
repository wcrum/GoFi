package sandbox

import (
	"context"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"testing"
)

func TestKubernetesWorkerIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := fake.NewClientset()
	c.PrependReactor("create", "pods", func(ktesting.Action) (bool, runtime.Object, error) { cancel(); return false, nil, nil })
	k := &Kubernetes{Client: c, Namespace: "gofi", Image: DefaultImage}
	_, e := k.Start(ctx, Definition{Name: "library", SourceCode: "import gofi", TimeoutSeconds: 5, MemoryMiB: 128}, "secret")
	if e == nil {
		t.Fatal("cancelled readiness must fail")
	}
	pod, e := c.CoreV1().Pods("gofi").Get(context.Background(), "gofi-python-library", metav1.GetOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("worker must not receive Kubernetes credentials")
	}
	worker := pod.Spec.Containers[0]
	if !*worker.SecurityContext.ReadOnlyRootFilesystem || *worker.SecurityContext.AllowPrivilegeEscalation || !*pod.Spec.SecurityContext.RunAsNonRoot {
		t.Fatal("worker security context incomplete")
	}
	if worker.Resources.Limits.Memory().Value() != 128*1024*1024 {
		t.Fatal("worker memory cap missing")
	}
	if len(worker.SecurityContext.Capabilities.Drop) != 1 || worker.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatal("worker capabilities must all be dropped")
	}
	policy, e := c.NetworkingV1().NetworkPolicies("gofi").Get(context.Background(), "gofi-python-library", metav1.GetOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if len(policy.Spec.Egress) != 0 || len(policy.Spec.PolicyTypes) != 2 {
		t.Fatal("worker egress must be denied")
	}
	if len(policy.Spec.Ingress[0].From) != 2 {
		t.Fatal("controller and listener ingress peers required")
	}
	secret, e := c.CoreV1().Secrets("gofi").Get(context.Background(), "gofi-python-library", metav1.GetOptions{})
	if e != nil || secret.StringData["token"] != "secret" {
		t.Fatal("worker credential must use a Secret")
	}
}
func TestKubernetesCannotDeleteUnmanagedCollision(t *testing.T) {
	c := fake.NewClientset(&core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "gofi-python-library", Namespace: "gofi"}})
	k := &Kubernetes{Client: c, Namespace: "gofi"}
	if e := k.Stop(context.Background(), "library"); e == nil {
		t.Fatal("unmanaged resource must not be deleted")
	}
	if _, e := c.CoreV1().Pods("gofi").Get(context.Background(), "gofi-python-library", metav1.GetOptions{}); e != nil {
		t.Fatal(e)
	}
}
