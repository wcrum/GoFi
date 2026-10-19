package driver

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/wcrum/gofi/kubeclient"
	"github.com/wcrum/gofi/spec"
)

// GOFI_LISTENER_ENV (chart listenerPods.extraEnv and GOFI_PROFILE) reaches
// the listener container; variables the driver sets itself are not replaced.
func TestListenerPodCarriesExtraEnv(t *testing.T) {
	t.Setenv("GOFI_LISTENER_ENV", `[{"name":"GOFI_PROFILE","value":"edge"},{"name":"GOFI_K8S_NAMESPACE","value":"elsewhere"},{"name":"GOFI_SINK_RETRIES","value":"7"}]`)
	defaults, err := kubeclient.LoadPodDefaults("LISTENER")
	if err != nil {
		t.Fatal(err)
	}
	k, _ := newTestKube("gofi")
	k.listenerDefaults = defaults
	def := spec.FlowDefinition{Name: "edge-flow", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 7000}}}
	pod := k.listenerPod(def, []byte(`{}`), "", "hash")
	env := map[string]corev1.EnvVar{}
	for _, e := range pod.Spec.Containers[0].Env {
		if _, dup := env[e.Name]; dup {
			t.Fatalf("%s set twice: %+v", e.Name, pod.Spec.Containers[0].Env)
		}
		env[e.Name] = e
	}
	if env["GOFI_PROFILE"].Value != "edge" || env["GOFI_SINK_RETRIES"].Value != "7" {
		t.Fatalf("extra env missing: %+v", pod.Spec.Containers[0].Env)
	}
	if env["GOFI_K8S_NAMESPACE"].Value != "gofi" {
		t.Fatalf("driver-owned namespace overridden: %+v", env["GOFI_K8S_NAMESPACE"])
	}
	if env[spec.FlowSpecEnv].Value != `{}` {
		t.Fatalf("FLOW_SPEC lost: %+v", env[spec.FlowSpecEnv])
	}
}
