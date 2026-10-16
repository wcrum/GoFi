package kubeexplain

import (
	"net"
	"testing"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	tcp      = corev1.ProtocolTCP
	udp      = corev1.ProtocolUDP
	flowPod  = Workload{Namespace: "gofi", Labels: map[string]string{"gofi.io/listener": "telemetry", "gofi.io/managed-by": "true"}, NamespaceLabels: map[string]string{"kubernetes.io/metadata.name": "gofi"}, PortNames: map[string]int32{"data": 10001}}
	natsPod  = Workload{Namespace: "nats", Labels: map[string]string{"app": "nats"}, NamespaceLabels: map[string]string{"kubernetes.io/metadata.name": "nats"}}
	sameNS   = Workload{Namespace: "gofi", Labels: map[string]string{"app": "sender"}, NamespaceLabels: flowPod.NamespaceLabels}
	outside  = Peer{}
	dataPort = PortRef{Number: 10001, Name: "data", Protocol: tcp}
)

func policy(name, ns string, types []netv1.PolicyType, pod map[string]string) netv1.NetworkPolicy {
	return netv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: netv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: pod}, PolicyTypes: types}}
}
func port(n int) *intstr.IntOrString     { v := intstr.FromInt(n); return &v }
func named(n string) *intstr.IntOrString { v := intstr.FromString(n); return &v }

func TestNetworkPolicySemantics(t *testing.T) {
	denyAll := policy("default-deny", "gofi", []netv1.PolicyType{netv1.PolicyTypeIngress, netv1.PolicyTypeEgress}, nil)

	allowAnyIngress := policy("allow-data", "gofi", []netv1.PolicyType{netv1.PolicyTypeIngress}, map[string]string{"gofi.io/managed-by": "true"})
	allowAnyIngress.Spec.Ingress = []netv1.NetworkPolicyIngressRule{{From: []netv1.NetworkPolicyPeer{{IPBlock: &netv1.IPBlock{CIDR: "0.0.0.0/0"}}}, Ports: []netv1.NetworkPolicyPort{{Protocol: &tcp, Port: port(10001)}}}}

	partialCIDR := policy("office-only", "gofi", nil, nil)
	partialCIDR.Spec.Ingress = []netv1.NetworkPolicyIngressRule{{From: []netv1.NetworkPolicyPeer{{IPBlock: &netv1.IPBlock{CIDR: "10.0.0.0/8", Except: []string{"10.9.0.0/16"}}}}}}

	namedPort := policy("named", "gofi", nil, nil)
	namedPort.Spec.Ingress = []netv1.NetworkPolicyIngressRule{{From: []netv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}, Ports: []netv1.NetworkPolicyPort{{Port: named("data")}}}}

	egressToNATS := policy("egress-nats", "gofi", []netv1.PolicyType{netv1.PolicyTypeEgress}, nil)
	egressToNATS.Spec.Egress = []netv1.NetworkPolicyEgressRule{{To: []netv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "nats"}}, PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nats"}}}}, Ports: []netv1.NetworkPolicyPort{{Port: port(4222), EndPort: int32p(4223)}}}}

	otherNamespace := policy("elsewhere", "nats", nil, nil)

	tests := []struct {
		name      string
		policies  []netv1.NetworkPolicy
		direction Direction
		subject   Workload
		peer      Peer
		port      PortRef
		want      Verdict
		isolated  bool
	}{
		{"no policies: not isolated", nil, Ingress, flowPod, outside, dataPort, Allowed, false},
		{"policy in another namespace does not apply", []netv1.NetworkPolicy{otherNamespace}, Ingress, flowPod, outside, dataPort, Allowed, false},
		{"default deny blocks external ingress", []netv1.NetworkPolicy{denyAll}, Ingress, flowPod, outside, dataPort, Blocked, true},
		{"union: an allow rule beats default deny", []netv1.NetworkPolicy{denyAll, allowAnyIngress}, Ingress, flowPod, outside, dataPort, Allowed, true},
		{"allow rule is port specific", []netv1.NetworkPolicy{denyAll, allowAnyIngress}, Ingress, flowPod, outside, PortRef{Number: 9091, Protocol: tcp}, Blocked, true},
		{"protocol must match (UDP vs TCP rule)", []netv1.NetworkPolicy{denyAll, allowAnyIngress}, Ingress, flowPod, outside, PortRef{Number: 10001, Protocol: udp}, Blocked, true},
		{"partial CIDR with unknown sender address is unknown", []netv1.NetworkPolicy{partialCIDR}, Ingress, flowPod, outside, dataPort, Unknown, true},
		{"partial CIDR includes a known address", []netv1.NetworkPolicy{partialCIDR}, Ingress, flowPod, Peer{IP: net.ParseIP("10.1.2.3")}, dataPort, Allowed, true},
		{"ipBlock except excludes an address", []netv1.NetworkPolicy{partialCIDR}, Ingress, flowPod, Peer{IP: net.ParseIP("10.9.1.1")}, dataPort, Blocked, true},
		{"named port matches the container port name", []netv1.NetworkPolicy{namedPort}, Ingress, flowPod, Peer{Workload: &sameNS}, dataPort, Allowed, true},
		{"empty podSelector peer is same-namespace only", []netv1.NetworkPolicy{namedPort}, Ingress, flowPod, Peer{Workload: &natsPod}, dataPort, Blocked, true},
		{"pod selectors never match external senders", []netv1.NetworkPolicy{namedPort}, Ingress, flowPod, outside, dataPort, Blocked, true},
		{"egress default deny blocks the sink", []netv1.NetworkPolicy{denyAll}, Egress, flowPod, Peer{Workload: &natsPod}, PortRef{Number: 4222, Protocol: tcp}, Blocked, true},
		{"egress namespace AND pod selector with endPort", []netv1.NetworkPolicy{denyAll, egressToNATS}, Egress, flowPod, Peer{Workload: &natsPod}, PortRef{Number: 4223, Protocol: tcp}, Allowed, true},
		{"egress outside endPort range", []netv1.NetworkPolicy{denyAll, egressToNATS}, Egress, flowPod, Peer{Workload: &natsPod}, PortRef{Number: 8222, Protocol: tcp}, Blocked, true},
		{"policyTypes omitted: egress not isolated without egress rules", []netv1.NetworkPolicy{partialCIDR}, Egress, flowPod, Peer{Workload: &natsPod}, PortRef{Number: 4222, Protocol: tcp}, Allowed, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateNetworkPolicies(tc.policies, tc.direction, tc.subject, tc.peer, tc.port)
			if got.Verdict != tc.want || got.Isolated != tc.isolated {
				t.Fatalf("verdict %s isolated %v, want %s isolated %v; findings %+v", got.Verdict, got.Isolated, tc.want, tc.isolated, got.Findings)
			}
			if len(got.Findings) == 0 || got.Findings[len(got.Findings)-1].Detail == "" {
				t.Fatalf("every verdict needs an explanation: %+v", got.Findings)
			}
		})
	}
}

func int32p(v int32) *int32 { return &v }
