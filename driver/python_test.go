package driver

import (
	"context"
	"testing"

	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/spec"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRemotePythonBindingsTrackOnlyReferencedLibraries(t *testing.T) {
	ctx := context.Background()
	bindings := map[string]functionrpc.Binding{"used": {Endpoint: "http://worker:8080", Token: "old", TimeoutSeconds: 5}}
	functionrpc.SetResolver(func(context.Context) (map[string]functionrpc.Binding, error) { return bindings, nil })
	t.Cleanup(func() { functionrpc.SetResolver(nil) })
	def := spec.FlowDefinition{Name: "python", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 1234}}, Parser: spec.ParserCfg{Format: "python", Python: &spec.PythonRef{Library: "used", Function: "parse"}}}
	api := newFakeDockerAPI()
	d := &Docker{cli: api, network: "test", image: "gofi:test"}
	if err := d.Create(ctx, def); err != nil {
		t.Fatal(err)
	}
	env, err := functionrpc.Environment(ctx, def)
	if err != nil {
		t.Fatal(err)
	}
	k := &Kube{namespace: "test", cs: fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName(def.Name), Namespace: "test", Labels: map[string]string{kubeLabelManaged: "true", kubeLabelListener: def.Name}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "gofi-listener", Env: []corev1.EnvVar{{Name: "GOFI_PYTHON_BINDINGS", Value: env}}}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	})}
	for name, checker := range map[string]PythonBindingsChecker{"docker": d, "kubernetes": k} {
		t.Run(name, func(t *testing.T) {
			bindings["used"] = functionrpc.Binding{Endpoint: "http://worker:8080", Token: "old", TimeoutSeconds: 5}
			bindings["unrelated"] = functionrpc.Binding{Endpoint: "http://new:8080", Token: "new"}
			if current, err := checker.PythonBindingsCurrent(ctx, def); err != nil || !current {
				t.Fatalf("unrelated addition marked listener stale: %v", err)
			}
			bindings["used"] = functionrpc.Binding{Endpoint: "http://worker:8080", Token: "rotated", TimeoutSeconds: 5}
			if current, err := checker.PythonBindingsCurrent(ctx, def); err != nil || current {
				t.Fatalf("rotation not detected: %v", err)
			}
			delete(bindings, "used")
			if _, err := checker.PythonBindingsCurrent(ctx, def); err == nil {
				t.Fatal("unready library must retain an unknown state")
			}
		})
	}
}
