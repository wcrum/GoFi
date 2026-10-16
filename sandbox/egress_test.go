package sandbox

import (
	"strings"
	"testing"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/wcrum/gofi/spec"
)

func TestWorkerNetworkPolicyEgressRules(t *testing.T) {
	labels := map[string]string{"gofi.sandbox": "library"}
	denied := WorkerNetworkPolicy(metav1.ObjectMeta{Name: "p"}, labels, nil)
	if len(denied.Spec.Egress) != 0 || len(denied.Spec.PolicyTypes) != 2 {
		t.Fatal("a library without rules must have no egress, not even DNS")
	}
	d, err := Normalize(Definition{Name: "library", SourceCode: "import gofi", Egress: []EgressRule{
		{CIDR: "10.0.0.7", Port: 4222},
		{Namespace: "messaging", PodSelector: map[string]string{"app": "nats"}, Port: 4222, Protocol: "TCP"},
		{CIDR: "192.168.0.0/16", Protocol: "udp"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	policy := WorkerNetworkPolicy(metav1.ObjectMeta{Name: "p"}, labels, d.Egress)
	if len(policy.Spec.Egress) != 4 {
		t.Fatalf("expected 3 rules plus DNS, got %d", len(policy.Spec.Egress))
	}
	first := policy.Spec.Egress[0]
	if first.To[0].IPBlock.CIDR != "10.0.0.7/32" || first.Ports[0].Port.IntValue() != 4222 || *first.Ports[0].Protocol != core.ProtocolTCP {
		t.Fatalf("cidr rule: %+v", first)
	}
	selector := policy.Spec.Egress[1].To[0]
	if selector.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "messaging" || selector.PodSelector.MatchLabels["app"] != "nats" || selector.IPBlock != nil {
		t.Fatalf("namespace rule: %+v", selector)
	}
	anyPort := policy.Spec.Egress[2]
	if anyPort.Ports[0].Port != nil || *anyPort.Ports[0].Protocol != core.ProtocolUDP {
		t.Fatalf("port 0 must allow any port on the protocol: %+v", anyPort)
	}
	dns := policy.Spec.Egress[3]
	if dns.To[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "kube-system" || len(dns.Ports) != 2 {
		t.Fatalf("DNS rule: %+v", dns)
	}
}

func TestEgressAndManagedValidation(t *testing.T) {
	for _, rule := range []EgressRule{{}, {CIDR: "not-a-cidr"}, {CIDR: "10.0.0.0/8", Port: 70000}, {CIDR: "10.0.0.0/8", Protocol: "icmp"}, {Namespace: "Bad_NS"}} {
		if _, err := Normalize(Definition{Name: "library", SourceCode: "import gofi", Egress: []EgressRule{rule}}); err == nil {
			t.Fatalf("invalid egress accepted: %+v", rule)
		}
	}
	if _, err := NormalizeUser(Definition{Name: "flow-mine", SourceCode: "x"}); err == nil {
		t.Fatal("reserved managed prefix accepted from a user")
	}
	if _, err := NormalizeUser(Definition{Name: "mine", SourceCode: "x", ManagedBy: "flow:x"}); err == nil {
		t.Fatal("users must not mark libraries managed")
	}
	if _, err := Normalize(Definition{Name: "lib", SourceCode: "x", Tests: []FunctionTest{{Function: "f", Kind: "bogus"}}}); err == nil {
		t.Fatal("invalid test kind accepted")
	}
}

func TestInlineLibraryIsDeterministicAndIsolated(t *testing.T) {
	def := spec.FlowDefinition{Name: "Telemetry.Demo", Stages: []spec.StageSpec{
		{ID: "enrich", Kind: "python", Python: &spec.PythonRef{InlineSource: "def transform(fields):\n    return fields\n"}},
		{ID: "keep", Kind: "filter", Type: "cel", Expression: "true"},
		{ID: "tag", Kind: "python", Python: &spec.PythonRef{InlineSource: "x = \"\"\"a\n  b\"\"\"\ndef transform(fields, meta, label='t'):\n    return {**fields, 'tag': label}\n"}},
	}}
	lib, ok := InlineLibrary(def)
	if !ok || lib.Name != spec.ManagedLibraryName(def.Name) || lib.ManagedBy != "flow:Telemetry.Demo" {
		t.Fatalf("managed library: %+v", lib)
	}
	if _, err := Normalize(lib); err != nil {
		t.Fatalf("managed library must be a valid definition: %v", err)
	}
	if strings.Count(lib.SourceCode, "_gofi._inline(") != 2 || !strings.Contains(lib.SourceCode, `_gofi._inline("tag", `) {
		t.Fatalf("each inline stage registers as its stage ID:\n%s", lib.SourceCode)
	}
	// Multi-line string literals are embedded verbatim, never re-indented.
	if !strings.Contains(lib.SourceCode, `x = \"\"\"a\n  b\"\"\"`) {
		t.Fatalf("snippet text must be preserved exactly:\n%s", lib.SourceCode)
	}
	again, _ := InlineLibrary(def)
	if again.SourceCode != lib.SourceCode {
		t.Fatal("managed source must be deterministic")
	}
	if _, ok := InlineLibrary(spec.FlowDefinition{Name: "plain"}); ok {
		t.Fatal("listeners without inline stages have no managed library")
	}
	for _, name := range []string{"a", strings.Repeat("very-long-listener-name.", 6), "x.y.z"} {
		if got := spec.ManagedLibraryName(name); !namePattern.MatchString(got) {
			t.Fatalf("managed name %q for %q violates the library name rule", got, name)
		}
	}
}
