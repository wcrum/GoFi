// Package kubeexplain explains what a flow becomes in Kubernetes and which
// policies may block its traffic. It is read-only: it evaluates policy
// objects statically and never changes the cluster.
package kubeexplain

import (
	"fmt"
	"net"
	"strings"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// Verdict is the outcome of evaluating one traffic path.
type Verdict string

const (
	Allowed Verdict = "allowed"
	Blocked Verdict = "blocked"
	Unknown Verdict = "unknown"
)

// Finding records how one policy object affected a path.
type Finding struct {
	Engine    string // "networkpolicy" | "istio" | "uds"
	Kind      string
	Name      string
	Namespace string
	Effect    string // "allows" | "blocks" | "isolates" | "unknown" | "not-applicable"
	Detail    string
}

// Workload is a pod (or set of identically labelled pods) on one side of a path.
type Workload struct {
	Namespace       string
	Labels          map[string]string
	NamespaceLabels map[string]string
	// Container ports by name, for named NetworkPolicy ports.
	PortNames map[string]int32
}

// Peer is the other side of a path: a workload, or an address outside the
// pod network. An external peer with a nil IP is "anywhere outside the
// cluster" (for example NodePort senders whose address is unknown).
type Peer struct {
	Workload    *Workload
	IP          net.IP
	Description string
}

// External reports whether the peer is outside the pod network.
func (p Peer) External() bool { return p.Workload == nil }

// Direction is the policy type being evaluated on the subject workload.
type Direction string

const (
	Ingress Direction = "Ingress"
	Egress  Direction = "Egress"
)

// PortRef is the destination port of a path. Name is the destination
// container's port name when known (for named policy ports).
type PortRef struct {
	Number   int32
	Name     string
	Protocol corev1.Protocol
}

// NetworkPolicyResult is the NetworkPolicy verdict for one side of a path.
type NetworkPolicyResult struct {
	Verdict  Verdict
	Isolated bool
	Findings []Finding
}

// EvaluateNetworkPolicies applies Kubernetes NetworkPolicy semantics for one
// direction on `subject`. For Ingress, `peer` is the sender and `port` is on
// the subject. For Egress, `peer` is the destination and `port` is on the
// peer. Policies from every namespace may be passed; only those in the
// subject's namespace that select it for `direction` apply.
func EvaluateNetworkPolicies(policies []netv1.NetworkPolicy, direction Direction, subject Workload, peer Peer, port PortRef) NetworkPolicyResult {
	result := NetworkPolicyResult{Verdict: Allowed}
	anyUnknown := false
	for _, policy := range policies {
		if policy.Namespace != subject.Namespace || !appliesTo(policy, direction) {
			continue
		}
		selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
		if err != nil || !selector.Matches(labels.Set(subject.Labels)) {
			continue
		}
		result.Isolated = true
		match, detail := policyAllows(policy, direction, subject, peer, port)
		finding := Finding{Engine: "networkpolicy", Kind: "NetworkPolicy", Name: policy.Name, Namespace: policy.Namespace, Detail: detail}
		switch match {
		case Allowed:
			finding.Effect = "allows"
			result.Findings = append(result.Findings, finding)
			return finalize(result, Allowed)
		case Unknown:
			finding.Effect = "unknown"
			anyUnknown = true
		default:
			finding.Effect = "isolates"
		}
		result.Findings = append(result.Findings, finding)
	}
	if !result.Isolated {
		result.Findings = append(result.Findings, Finding{Engine: "networkpolicy", Effect: "not-applicable",
			Detail: fmt.Sprintf("No NetworkPolicy selects this workload for %s, so it is not isolated.", strings.ToLower(string(direction)))})
		return result
	}
	if anyUnknown {
		return finalize(result, Unknown)
	}
	return finalize(result, Blocked)
}

func finalize(result NetworkPolicyResult, verdict Verdict) NetworkPolicyResult {
	result.Verdict = verdict
	return result
}

// appliesTo implements policyTypes defaulting: Ingress always applies when
// policyTypes is empty; Egress only when egress rules are present.
func appliesTo(policy netv1.NetworkPolicy, direction Direction) bool {
	if len(policy.Spec.PolicyTypes) == 0 {
		return direction == Ingress || len(policy.Spec.Egress) > 0
	}
	for _, t := range policy.Spec.PolicyTypes {
		if string(t) == string(direction) {
			return true
		}
	}
	return false
}

func policyAllows(policy netv1.NetworkPolicy, direction Direction, subject Workload, peer Peer, port PortRef) (Verdict, string) {
	type rule struct {
		peers []netv1.NetworkPolicyPeer
		ports []netv1.NetworkPolicyPort
	}
	var rules []rule
	if direction == Ingress {
		for _, r := range policy.Spec.Ingress {
			rules = append(rules, rule{r.From, r.Ports})
		}
	} else {
		for _, r := range policy.Spec.Egress {
			rules = append(rules, rule{r.To, r.Ports})
		}
	}
	if len(rules) == 0 {
		return Blocked, fmt.Sprintf("Selects this workload for %s with no rules: all %s traffic is denied.", strings.ToLower(string(direction)), strings.ToLower(string(direction)))
	}
	sawUnknown := false
	var reasons []string
	for i, r := range rules {
		portMatch := portsMatch(r.ports, port)
		if portMatch == Blocked {
			reasons = append(reasons, fmt.Sprintf("rule %d does not include %s", i+1, describePort(port)))
			continue
		}
		peerMatch, why := peersMatch(r.peers, subject, peer)
		switch {
		case peerMatch == Allowed && portMatch == Allowed:
			return Allowed, fmt.Sprintf("Rule %d allows %s on %s.", i+1, peerLabel(peer), describePort(port))
		case peerMatch == Blocked:
			reasons = append(reasons, fmt.Sprintf("rule %d: %s", i+1, why))
		default:
			sawUnknown = true
			reasons = append(reasons, fmt.Sprintf("rule %d may apply: %s", i+1, why))
		}
	}
	if sawUnknown {
		return Unknown, "Isolates this workload; " + strings.Join(reasons, "; ") + "."
	}
	return Blocked, "Isolates this workload; no rule allows " + peerLabel(peer) + " on " + describePort(port) + " (" + strings.Join(reasons, "; ") + ")."
}

func portsMatch(ports []netv1.NetworkPolicyPort, port PortRef) Verdict {
	if len(ports) == 0 {
		return Allowed
	}
	for _, p := range ports {
		protocol := corev1.ProtocolTCP
		if p.Protocol != nil {
			protocol = *p.Protocol
		}
		if protocol != port.Protocol {
			continue
		}
		if p.Port == nil {
			return Allowed
		}
		if p.Port.Type == 1 { // named port
			if port.Name != "" && p.Port.StrVal == port.Name {
				return Allowed
			}
			continue
		}
		low := p.Port.IntVal
		high := low
		if p.EndPort != nil {
			high = *p.EndPort
		}
		if port.Number >= low && port.Number <= high {
			return Allowed
		}
	}
	return Blocked
}

// peersMatch evaluates a rule's peer list against the path peer. `subject`
// supplies the namespace for podSelector-only peers.
func peersMatch(peers []netv1.NetworkPolicyPeer, subject Workload, peer Peer) (Verdict, string) {
	if len(peers) == 0 {
		return Allowed, "all peers"
	}
	result := Blocked
	var why []string
	for _, p := range peers {
		v, reason := peerMatches(p, subject, peer)
		if v == Allowed {
			return Allowed, reason
		}
		if v == Unknown {
			result = Unknown
		}
		why = append(why, reason)
	}
	return result, strings.Join(why, ", ")
}

func peerMatches(p netv1.NetworkPolicyPeer, subject Workload, peer Peer) (Verdict, string) {
	if p.IPBlock != nil {
		return ipBlockMatches(*p.IPBlock, peer)
	}
	if peer.External() {
		return Blocked, "only selects pods, not " + peerLabel(peer)
	}
	w := peer.Workload
	if p.NamespaceSelector != nil {
		ns, err := metav1.LabelSelectorAsSelector(p.NamespaceSelector)
		if err != nil || !ns.Matches(labels.Set(w.NamespaceLabels)) {
			return Blocked, fmt.Sprintf("namespace %q does not match %s", w.Namespace, selectorString(p.NamespaceSelector))
		}
	} else if w.Namespace != subject.Namespace {
		return Blocked, fmt.Sprintf("podSelector only covers namespace %q, peer is in %q", subject.Namespace, w.Namespace)
	}
	if p.PodSelector != nil {
		pods, err := metav1.LabelSelectorAsSelector(p.PodSelector)
		if err != nil || !pods.Matches(labels.Set(w.Labels)) {
			return Blocked, "pod labels do not match " + selectorString(p.PodSelector)
		}
	}
	return Allowed, "selector matches " + peerLabel(peer)
}

func ipBlockMatches(block netv1.IPBlock, peer Peer) (Verdict, string) {
	_, cidr, err := net.ParseCIDR(block.CIDR)
	if err != nil {
		return Blocked, "invalid ipBlock " + block.CIDR
	}
	if peer.IP == nil {
		if ones, _ := cidr.Mask.Size(); ones == 0 && len(block.Except) == 0 {
			return Allowed, "ipBlock " + block.CIDR + " covers every address"
		}
		return Unknown, "ipBlock " + block.CIDR + " covers only some addresses and " + peerLabel(peer) + " has no known address"
	}
	if !cidr.Contains(peer.IP) {
		return Blocked, peer.IP.String() + " is outside ipBlock " + block.CIDR
	}
	for _, except := range block.Except {
		if _, ex, err := net.ParseCIDR(except); err == nil && ex.Contains(peer.IP) {
			return Blocked, peer.IP.String() + " is excluded by " + except
		}
	}
	return Allowed, "ipBlock " + block.CIDR + " includes " + peer.IP.String()
}

func peerLabel(peer Peer) string {
	if peer.Description != "" {
		return peer.Description
	}
	if peer.External() {
		if peer.IP != nil {
			return peer.IP.String()
		}
		return "senders outside the cluster"
	}
	return "pods " + labels.Set(peer.Workload.Labels).String() + " in " + peer.Workload.Namespace
}

func describePort(port PortRef) string {
	if port.Name != "" {
		return fmt.Sprintf("%s/%d (%s)", port.Protocol, port.Number, port.Name)
	}
	return fmt.Sprintf("%s/%d", port.Protocol, port.Number)
}

func selectorString(s *metav1.LabelSelector) string {
	sel, err := metav1.LabelSelectorAsSelector(s)
	if err != nil {
		return "(invalid selector)"
	}
	if sel.Empty() {
		return "{} (everything)"
	}
	return sel.String()
}
