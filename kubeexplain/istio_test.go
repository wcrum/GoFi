package kubeexplain

import (
	"net"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func istioObject(kind, name, namespace string, spec map[string]any) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{"apiVersion": "security.istio.io/v1", "kind": kind, "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": spec}}
}

func TestIstioSemantics(t *testing.T) {
	subject := MeshSubject{Workload: flowPod, InMesh: true, ServiceNames: []string{"gofi-listener-telemetry"}}
	external := MeshPeer{}
	natsSender := MeshPeer{InMesh: true, Principal: "cluster.local/ns/nats/sa/nats", Namespace: "nats"}
	data := PortRef{Number: 10001, Protocol: corev1.ProtocolTCP}

	strictMesh := istioObject("PeerAuthentication", "default", "istio-system", map[string]any{"mtls": map[string]any{"mode": "STRICT"}})
	permissivePort := istioObject("PeerAuthentication", "gofi-data", "gofi", map[string]any{
		"selector":      map[string]any{"matchLabels": map[string]any{"gofi.io/managed-by": "true"}},
		"mtls":          map[string]any{"mode": "STRICT"},
		"portLevelMtls": map[string]any{"10001": map[string]any{"mode": "PERMISSIVE"}},
	})
	allowNothing := istioObject("AuthorizationPolicy", "deny-all", "gofi", map[string]any{})
	allowAll := istioObject("AuthorizationPolicy", "allow-all", "gofi", map[string]any{"rules": []any{map[string]any{}}})
	allowNATSNamespace := istioObject("AuthorizationPolicy", "allow-nats", "gofi", map[string]any{"action": "ALLOW", "rules": []any{map[string]any{"from": []any{map[string]any{"source": map[string]any{"namespaces": []any{"nats"}}}}}}})
	allowHTTPOnly := istioObject("AuthorizationPolicy", "allow-get", "gofi", map[string]any{"rules": []any{map[string]any{"to": []any{map[string]any{"operation": map[string]any{"methods": []any{"GET"}}}}}}})
	denyPort := istioObject("AuthorizationPolicy", "deny-data", "gofi", map[string]any{"action": "DENY", "rules": []any{map[string]any{"to": []any{map[string]any{"operation": map[string]any{"ports": []any{"10001"}, "paths": []any{"/admin"}}}}}}})
	jwtAllow := istioObject("AuthorizationPolicy", "jwt", "gofi", map[string]any{"rules": []any{map[string]any{"from": []any{map[string]any{"source": map[string]any{"requestPrincipals": []any{"*"}}}}}}})
	otherWorkload := istioObject("AuthorizationPolicy", "other", "gofi", map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "other"}}})
	custom := istioObject("AuthorizationPolicy", "ext-authz", "istio-system", map[string]any{"action": "CUSTOM", "provider": map[string]any{"name": "oauth"}, "rules": []any{map[string]any{}}})
	ipAllow := istioObject("AuthorizationPolicy", "office", "gofi", map[string]any{"rules": []any{map[string]any{"from": []any{map[string]any{"source": map[string]any{"ipBlocks": []any{"203.0.113.0/24"}}}}}}})

	tests := []struct {
		name    string
		authz   []unstructured.Unstructured
		pa      []unstructured.Unstructured
		subject MeshSubject
		peer    MeshPeer
		port    PortRef
		want    Verdict
	}{
		{"not in mesh: Istio does not apply", []unstructured.Unstructured{allowNothing}, []unstructured.Unstructured{strictMesh}, MeshSubject{Workload: flowPod}, external, data, Allowed},
		{"UDP is not proxied", []unstructured.Unstructured{allowNothing}, nil, subject, external, PortRef{Number: 10001, Protocol: corev1.ProtocolUDP}, Allowed},
		{"mesh-wide STRICT blocks plain-text senders", nil, []unstructured.Unstructured{strictMesh}, subject, external, data, Blocked},
		{"STRICT accepts mesh senders", nil, []unstructured.Unstructured{strictMesh}, subject, natsSender, data, Allowed},
		{"port-level PERMISSIVE overrides workload STRICT", nil, []unstructured.Unstructured{strictMesh, permissivePort}, subject, external, data, Allowed},
		{"ALLOW with no rules denies everything", []unstructured.Unstructured{allowNothing}, nil, subject, natsSender, data, Blocked},
		{"rules [{}] allows everything", []unstructured.Unstructured{allowAll}, nil, subject, external, data, Allowed},
		{"namespace source needs mTLS identity", []unstructured.Unstructured{allowNATSNamespace}, nil, subject, external, data, Blocked},
		{"namespace source matches mesh sender", []unstructured.Unstructured{allowNATSNamespace}, nil, subject, natsSender, data, Allowed},
		{"HTTP-only ALLOW never matches TCP", []unstructured.Unstructured{allowHTTPOnly}, nil, subject, natsSender, data, Blocked},
		{"DENY ignores HTTP fields on TCP and wins over ALLOW", []unstructured.Unstructured{allowAll, denyPort}, nil, subject, natsSender, data, Blocked},
		{"JWT conditions are unknown", []unstructured.Unstructured{jwtAllow}, nil, subject, natsSender, data, Unknown},
		{"selector for another workload does not apply", []unstructured.Unstructured{otherWorkload}, nil, subject, external, data, Allowed},
		{"CUSTOM is decided externally", []unstructured.Unstructured{custom}, nil, subject, natsSender, data, Unknown},
		{"ipBlocks with unknown sender address", []unstructured.Unstructured{ipAllow}, nil, subject, external, data, Unknown},
		{"ipBlocks include known sender", []unstructured.Unstructured{ipAllow}, nil, subject, MeshPeer{IP: net.ParseIP("203.0.113.9")}, data, Allowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateIstio(tc.authz, tc.pa, "istio-system", tc.subject, tc.peer, tc.port)
			if got.Verdict != tc.want {
				t.Fatalf("verdict %s, want %s; findings %+v", got.Verdict, tc.want, got.Findings)
			}
			if len(got.Findings) == 0 {
				t.Fatal("every verdict needs an explanation")
			}
		})
	}
	// Evaluation must never mutate the policies it reads.
	if _, ok, _ := unstructured.NestedSlice(denyPort.Object, "spec", "rules"); !ok {
		t.Fatal("policy was modified")
	}
	rules, _, _ := unstructured.NestedSlice(denyPort.Object, "spec", "rules")
	op := rules[0].(map[string]any)["to"].([]any)[0].(map[string]any)["operation"].(map[string]any)
	if _, ok := op["paths"]; !ok {
		t.Fatal("DENY evaluation deleted the paths field from the policy object")
	}
}
