package kubeexplain

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func udsPackage(namespace string, allow ...map[string]any) unstructured.Unstructured {
	entries := make([]any, len(allow))
	for i, a := range allow {
		entries[i] = a
	}
	return unstructured.Unstructured{Object: map[string]any{"apiVersion": "uds.dev/v1alpha1", "kind": "Package", "metadata": map[string]any{"name": "gofi", "namespace": namespace}, "spec": map[string]any{"network": map[string]any{"allow": entries}}}}
}

func TestUDSIntent(t *testing.T) {
	flowLabels := map[string]string{"gofi.io/managed-by": "true", "gofi.io/listener": "telemetry"}
	ingressAnywhere := UDSPath{Direction: Ingress, Selector: flowLabels, RemoteAnywhere: true, Port: 10001, Description: "telemetry senders"}
	egressNATS := UDSPath{Direction: Egress, Selector: flowLabels, RemoteNamespace: "nats", RemoteSelector: map[string]string{"app": "nats"}, Port: 4222, Description: "telemetry to nats"}
	egressSameNS := UDSPath{Direction: Egress, Selector: flowLabels, RemoteNamespace: "gofi", Port: 8080}

	tests := []struct {
		name     string
		packages []unstructured.Unstructured
		path     UDSPath
		governed bool
		covered  bool
	}{
		{"no Package: not governed", nil, ingressAnywhere, false, false},
		{"Package in another namespace does not govern", []unstructured.Unstructured{udsPackage("other")}, ingressAnywhere, false, false},
		{"governed with no matching entry", []unstructured.Unstructured{udsPackage("gofi")}, ingressAnywhere, true, false},
		{"Anywhere ingress on the port", []unstructured.Unstructured{udsPackage("gofi", map[string]any{"direction": "Ingress", "selector": map[string]any{"gofi.io/managed-by": "true"}, "remoteGenerated": "Anywhere", "port": int64(10001)})}, ingressAnywhere, true, true},
		{"wrong port is not covered", []unstructured.Unstructured{udsPackage("gofi", map[string]any{"direction": "Ingress", "remoteGenerated": "Anywhere", "port": int64(9000)})}, ingressAnywhere, true, false},
		{"direction must match", []unstructured.Unstructured{udsPackage("gofi", map[string]any{"direction": "Egress", "remoteGenerated": "Anywhere"})}, ingressAnywhere, true, false},
		{"egress by remote namespace and selector", []unstructured.Unstructured{udsPackage("gofi", map[string]any{"direction": "Egress", "remoteNamespace": "nats", "remoteSelector": map[string]any{"app": "nats"}, "ports": []any{int64(4222), int64(8222)}})}, egressNATS, true, true},
		{"remote selector mismatch", []unstructured.Unstructured{udsPackage("gofi", map[string]any{"direction": "Egress", "remoteNamespace": "nats", "remoteSelector": map[string]any{"app": "other"}})}, egressNATS, true, false},
		{"IntraNamespace covers the same namespace only", []unstructured.Unstructured{udsPackage("gofi", map[string]any{"direction": "Egress", "remoteGenerated": "IntraNamespace"})}, egressSameNS, true, true},
		{"IntraNamespace does not cover another namespace", []unstructured.Unstructured{udsPackage("gofi", map[string]any{"direction": "Egress", "remoteGenerated": "IntraNamespace"})}, egressNATS, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateUDS(tc.packages, "gofi", tc.path)
			if got.Governed != tc.governed || got.Covered != tc.covered {
				t.Fatalf("governed %v covered %v, want %v %v; findings %+v", got.Governed, got.Covered, tc.governed, tc.covered, got.Findings)
			}
		})
	}
	fix := UDSAllowFix(egressNATS)
	for _, want := range []string{"direction: Egress", "remoteNamespace: nats", "app: nats", "port: 4222"} {
		if !strings.Contains(fix, want) {
			t.Fatalf("fix missing %q:\n%s", want, fix)
		}
	}
}

func TestUDSAllowsArePerTransport(t *testing.T) {
	pkg := unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "demeter", "namespace": "demeter"},
		"spec": map[string]any{"network": map[string]any{"allow": []any{
			map[string]any{"direction": "Ingress", "selector": map[string]any{"app": "demeter"}, "remoteNamespace": "gofi", "port": int64(60084)},
		}}}}}
	path := UDSPath{Direction: Ingress, Selector: map[string]string{"app": "demeter"}, RemoteNamespace: "gofi", Port: 60084, Protocol: "UDP"}
	if EvaluateUDS([]unstructured.Unstructured{pkg}, "demeter", path).Covered {
		t.Fatal("a TCP allow (the default) must not cover UDP")
	}
	path.Protocol = "TCP"
	if !EvaluateUDS([]unstructured.Unstructured{pkg}, "demeter", path).Covered {
		t.Fatal("default protocol is TCP")
	}
	path.Protocol = "UDP"
	if fix := UDSAllowFix(path); !strings.Contains(fix, "remoteProtocol: UDP") {
		t.Fatalf("UDP fix must say so: %s", fix)
	}
}
