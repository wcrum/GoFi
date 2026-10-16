package kubeexplain

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

// MeshPeer describes the sender as Istio sees it. Senders outside the mesh
// (plain-text, e.g. NodePort clients) have no principal or namespace.
type MeshPeer struct {
	InMesh    bool
	Principal string // "cluster.local/ns/<ns>/sa/<sa>" when InMesh
	Namespace string
	IP        net.IP
	Label     string
}

// MeshSubject is the destination workload of a path.
type MeshSubject struct {
	Workload Workload
	InMesh   bool
	// Services that select the workload, for targetRefs scoping.
	ServiceNames []string
}

// IstioResult is the Istio verdict for traffic into one workload.
type IstioResult struct {
	Verdict  Verdict
	Findings []Finding
}

// EvaluateIstio applies PeerAuthentication and AuthorizationPolicy to traffic
// from peer into subject on port. rootNamespace is the mesh config root
// (usually istio-system). Policies are unstructured security.istio.io objects.
func EvaluateIstio(authz, peerAuth []unstructured.Unstructured, rootNamespace string, subject MeshSubject, peer MeshPeer, port PortRef) IstioResult {
	result := IstioResult{Verdict: Allowed}
	if !subject.InMesh {
		result.Findings = append(result.Findings, Finding{Engine: "istio", Effect: "not-applicable", Detail: "The workload is not in the mesh (no sidecar injection or ambient enrollment), so Istio policies do not apply."})
		return result
	}
	if port.Protocol == corev1.ProtocolUDP {
		result.Findings = append(result.Findings, Finding{Engine: "istio", Effect: "not-applicable", Detail: "Istio does not proxy UDP; AuthorizationPolicy and mTLS do not apply to this port."})
		return result
	}

	// PeerAuthentication: STRICT rejects plain-text senders.
	if mode, source := effectiveMTLS(peerAuth, rootNamespace, subject.Workload, port.Number); mode == "STRICT" && !peer.InMesh {
		result.Findings = append(result.Findings, Finding{Engine: "istio", Kind: "PeerAuthentication", Name: source.GetName(), Namespace: source.GetNamespace(), Effect: "blocks",
			Detail: fmt.Sprintf("mTLS mode STRICT on port %d rejects plain-text traffic from %s.", port.Number, meshPeerLabel(peer))})
		result.Verdict = Blocked
		return result
	} else if source != nil {
		result.Findings = append(result.Findings, Finding{Engine: "istio", Kind: "PeerAuthentication", Name: source.GetName(), Namespace: source.GetNamespace(), Effect: "allows", Detail: "mTLS mode " + mode + " accepts this sender."})
	}

	applicable := map[string][]unstructured.Unstructured{}
	for _, policy := range authz {
		if scope := authzScope(policy, rootNamespace, subject); scope != "" {
			action := strings.ToUpper(stringField(policy.Object, "spec", "action"))
			if action == "" {
				action = "ALLOW"
			}
			applicable[action] = append(applicable[action], policy)
		}
	}
	sawUnknown := false
	for _, policy := range applicable["CUSTOM"] {
		result.Findings = append(result.Findings, Finding{Engine: "istio", Kind: "AuthorizationPolicy", Name: policy.GetName(), Namespace: policy.GetNamespace(), Effect: "unknown", Detail: "CUSTOM action delegates the decision to an external authorizer."})
		sawUnknown = true
	}
	for _, policy := range applicable["DENY"] {
		match, why := policyMatches(policy, peer, port, true)
		switch match {
		case Allowed:
			result.Findings = append(result.Findings, Finding{Engine: "istio", Kind: "AuthorizationPolicy", Name: policy.GetName(), Namespace: policy.GetNamespace(), Effect: "blocks", Detail: "DENY rule matches: " + why})
			result.Verdict = Blocked
			return result
		case Unknown:
			sawUnknown = true
			result.Findings = append(result.Findings, Finding{Engine: "istio", Kind: "AuthorizationPolicy", Name: policy.GetName(), Namespace: policy.GetNamespace(), Effect: "unknown", Detail: "DENY rule may match: " + why})
		}
	}
	if allows := applicable["ALLOW"]; len(allows) > 0 {
		allowed := false
		for _, policy := range allows {
			match, why := policyMatches(policy, peer, port, false)
			finding := Finding{Engine: "istio", Kind: "AuthorizationPolicy", Name: policy.GetName(), Namespace: policy.GetNamespace(), Detail: why}
			switch match {
			case Allowed:
				finding.Effect = "allows"
				allowed = true
			case Unknown:
				finding.Effect = "unknown"
				sawUnknown = true
			default:
				finding.Effect = "isolates"
			}
			result.Findings = append(result.Findings, finding)
		}
		if !allowed {
			if sawUnknown {
				result.Verdict = Unknown
			} else {
				result.Verdict = Blocked
			}
			return result
		}
	}
	if sawUnknown && result.Verdict == Allowed {
		result.Verdict = Unknown
	}
	if len(applicable) == 0 {
		result.Findings = append(result.Findings, Finding{Engine: "istio", Effect: "not-applicable", Detail: "No AuthorizationPolicy applies to this workload."})
	}
	return result
}

// effectiveMTLS resolves PeerAuthentication precedence: workload selector >
// namespace-wide > mesh-wide, with portLevelMtls overriding the mode.
func effectiveMTLS(peerAuth []unstructured.Unstructured, rootNamespace string, subject Workload, port int32) (string, *unstructured.Unstructured) {
	var workload, namespace, mesh *unstructured.Unstructured
	for i := range peerAuth {
		pa := &peerAuth[i]
		selector, hasSelector := mapField(pa.Object, "spec", "selector", "matchLabels")
		switch {
		case pa.GetNamespace() == subject.Namespace && hasSelector:
			if labels.SelectorFromSet(toStringMap(selector)).Matches(labels.Set(subject.Labels)) {
				workload = pa
			}
		case pa.GetNamespace() == subject.Namespace:
			namespace = pa
		case pa.GetNamespace() == rootNamespace && !hasSelector:
			mesh = pa
		}
	}
	for _, pa := range []*unstructured.Unstructured{workload, namespace, mesh} {
		if pa == nil {
			continue
		}
		if ports, ok := mapField(pa.Object, "spec", "portLevelMtls"); ok {
			if entry, ok := ports[strconv.Itoa(int(port))].(map[string]any); ok {
				if mode, _ := entry["mode"].(string); mode != "" && mode != "UNSET" {
					return mode, pa
				}
			}
		}
		if mode := stringField(pa.Object, "spec", "mtls", "mode"); mode != "" && mode != "UNSET" {
			return mode, pa
		}
	}
	return "PERMISSIVE", nil
}

// authzScope reports why a policy applies to the subject ("" when it does not).
func authzScope(policy unstructured.Unstructured, rootNamespace string, subject MeshSubject) string {
	selector, hasSelector := mapField(policy.Object, "spec", "selector", "matchLabels")
	refs, hasRefs := sliceField(policy.Object, "spec", "targetRefs")
	if ref, ok := mapField(policy.Object, "spec", "targetRef"); ok {
		refs, hasRefs = append(refs, ref), true
	}
	switch {
	case hasRefs:
		if policy.GetNamespace() != subject.Workload.Namespace {
			return ""
		}
		for _, raw := range refs {
			ref, _ := raw.(map[string]any)
			kind, _ := ref["kind"].(string)
			name, _ := ref["name"].(string)
			if kind == "Service" {
				for _, svc := range subject.ServiceNames {
					if svc == name {
						return "targetRef Service/" + name
					}
				}
			}
		}
		return ""
	case hasSelector:
		if policy.GetNamespace() == subject.Workload.Namespace && labels.SelectorFromSet(toStringMap(selector)).Matches(labels.Set(subject.Workload.Labels)) {
			return "workload selector"
		}
		return ""
	case policy.GetNamespace() == rootNamespace:
		return "mesh-wide"
	case policy.GetNamespace() == subject.Workload.Namespace:
		return "namespace-wide"
	}
	return ""
}

// policyMatches evaluates an AuthorizationPolicy's rules. Allowed means a
// rule definitely matches, Blocked means no rule matches. For TCP traffic,
// HTTP-only fields make an ALLOW rule unmatched and are ignored in DENY rules.
func policyMatches(policy unstructured.Unstructured, peer MeshPeer, port PortRef, deny bool) (Verdict, string) {
	rules, ok := sliceField(policy.Object, "spec", "rules")
	if !ok || len(rules) == 0 {
		if deny {
			return Blocked, "has no rules, so it never denies"
		}
		return Blocked, "ALLOW with no rules denies all requests to this workload"
	}
	result := Blocked
	var reasons []string
	for i, raw := range rules {
		rule, _ := raw.(map[string]any)
		v, why := ruleMatches(rule, peer, port, deny)
		reason := fmt.Sprintf("rule %d: %s", i+1, why)
		if v == Allowed {
			return Allowed, reason
		}
		if v == Unknown {
			result = Unknown
		}
		reasons = append(reasons, reason)
	}
	return result, strings.Join(reasons, "; ")
}

var httpOnlyOperationFields = []string{"hosts", "notHosts", "methods", "notMethods", "paths", "notPaths"}

func ruleMatches(rule map[string]any, peer MeshPeer, port PortRef, deny bool) (Verdict, string) {
	result := Allowed
	var notes []string
	combine := func(v Verdict, note string) {
		if note != "" {
			notes = append(notes, note)
		}
		if v == Blocked {
			result = Blocked
		} else if v == Unknown && result != Blocked {
			result = Unknown
		}
	}
	if from, ok := rule["from"].([]any); ok && len(from) > 0 {
		combine(anyOf(from, "source", func(source map[string]any) (Verdict, string) { return sourceMatches(source, peer) }))
	}
	if to, ok := rule["to"].([]any); ok && len(to) > 0 {
		combine(anyOf(to, "operation", func(op map[string]any) (Verdict, string) {
			// TCP traffic: HTTP-only fields make ALLOW rules unmatched and are
			// ignored by DENY rules. The policy object itself is never modified.
			if !deny {
				for _, field := range httpOnlyOperationFields {
					if _, present := op[field]; present {
						return Blocked, "uses HTTP-only field " + field + ", which never matches TCP traffic"
					}
				}
			}
			if ports, ok := op["ports"].([]any); ok && !containsPort(ports, port.Number) {
				return Blocked, fmt.Sprintf("ports %v exclude %d", ports, port.Number)
			}
			if ports, ok := op["notPorts"].([]any); ok && containsPort(ports, port.Number) {
				return Blocked, fmt.Sprintf("notPorts excludes %d", port.Number)
			}
			return Allowed, ""
		}))
	}
	if when, ok := rule["when"].([]any); ok {
		for _, raw := range when {
			condition, _ := raw.(map[string]any)
			combine(conditionMatches(condition, peer, port))
		}
	}
	if len(notes) == 0 {
		notes = append(notes, "matches "+meshPeerLabel(peer))
	}
	return result, strings.Join(notes, ", ")
}

func anyOf(items []any, key string, match func(map[string]any) (Verdict, string)) (Verdict, string) {
	result := Blocked
	var notes []string
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		inner, _ := item[key].(map[string]any)
		v, note := match(inner)
		if v == Allowed {
			return Allowed, note
		}
		if v == Unknown {
			result = Unknown
		}
		notes = append(notes, note)
	}
	return result, strings.Join(notes, " / ")
}

func sourceMatches(source map[string]any, peer MeshPeer) (Verdict, string) {
	for _, field := range []string{"requestPrincipals", "notRequestPrincipals"} {
		if _, ok := source[field]; ok {
			return Unknown, field + " depends on request JWTs"
		}
	}
	if values, ok := stringList(source["principals"]); ok {
		if !peer.InMesh || !matchesAny(values, peer.Principal) {
			return Blocked, "principals " + strings.Join(values, ",") + " do not include " + meshPeerLabel(peer)
		}
	}
	if values, ok := stringList(source["notPrincipals"]); ok && peer.InMesh && matchesAny(values, peer.Principal) {
		return Blocked, "notPrincipals excludes " + peer.Principal
	}
	if values, ok := stringList(source["namespaces"]); ok {
		if !peer.InMesh || !matchesAny(values, peer.Namespace) {
			return Blocked, "namespaces " + strings.Join(values, ",") + " do not include " + meshPeerLabel(peer)
		}
	}
	if values, ok := stringList(source["notNamespaces"]); ok && peer.InMesh && matchesAny(values, peer.Namespace) {
		return Blocked, "notNamespaces excludes " + peer.Namespace
	}
	for _, field := range []string{"ipBlocks", "remoteIpBlocks"} {
		if values, ok := stringList(source[field]); ok {
			v, note := cidrsMatch(values, peer.IP)
			if v != Allowed {
				return v, field + " " + note
			}
		}
	}
	for _, field := range []string{"notIpBlocks", "notRemoteIpBlocks"} {
		if values, ok := stringList(source[field]); ok {
			v, note := cidrsMatch(values, peer.IP)
			if v == Allowed {
				return Blocked, field + " excludes the sender"
			}
			if v == Unknown {
				return Unknown, field + " " + note
			}
		}
	}
	return Allowed, ""
}

func conditionMatches(condition map[string]any, peer MeshPeer, port PortRef) (Verdict, string) {
	key, _ := condition["key"].(string)
	values, _ := stringList(condition["values"])
	notValues, _ := stringList(condition["notValues"])
	var actual string
	switch key {
	case "source.namespace":
		if !peer.InMesh {
			return Blocked, "condition source.namespace needs an mTLS sender"
		}
		actual = peer.Namespace
	case "source.principal":
		if !peer.InMesh {
			return Blocked, "condition source.principal needs an mTLS sender"
		}
		actual = peer.Principal
	case "destination.port":
		actual = strconv.Itoa(int(port.Number))
	case "source.ip", "remote.ip":
		if peer.IP == nil {
			return Unknown, "condition " + key + " depends on the sender address"
		}
		v, note := cidrsMatch(values, peer.IP)
		return v, "condition " + key + " " + note
	default:
		return Unknown, "condition " + key + " depends on request attributes"
	}
	if len(values) > 0 && !matchesAny(values, actual) {
		return Blocked, fmt.Sprintf("condition %s=%s not in %v", key, actual, values)
	}
	if matchesAny(notValues, actual) {
		return Blocked, fmt.Sprintf("condition %s excludes %s", key, actual)
	}
	return Allowed, ""
}

func cidrsMatch(values []string, ip net.IP) (Verdict, string) {
	if ip == nil {
		for _, v := range values {
			if v == "0.0.0.0/0" || v == "::/0" {
				return Allowed, "covers every address"
			}
		}
		return Unknown, "depends on the sender address"
	}
	for _, v := range values {
		if !strings.Contains(v, "/") {
			if net.ParseIP(v).Equal(ip) {
				return Allowed, "includes " + ip.String()
			}
			continue
		}
		if _, cidr, err := net.ParseCIDR(v); err == nil && cidr.Contains(ip) {
			return Allowed, "includes " + ip.String()
		}
	}
	return Blocked, "does not include " + ip.String()
}

// matchesAny implements Istio string matching: exact, "*", prefix "x*", suffix "*x".
func matchesAny(patterns []string, value string) bool {
	for _, p := range patterns {
		switch {
		case p == "*":
			if value != "" {
				return true
			}
		case strings.HasSuffix(p, "*") && strings.HasPrefix(value, strings.TrimSuffix(p, "*")):
			return true
		case strings.HasPrefix(p, "*") && strings.HasSuffix(value, strings.TrimPrefix(p, "*")):
			return true
		case p == value:
			return true
		}
	}
	return false
}

func containsPort(ports []any, port int32) bool {
	for _, p := range ports {
		if s, ok := p.(string); ok && s == strconv.Itoa(int(port)) {
			return true
		}
	}
	return false
}

func meshPeerLabel(peer MeshPeer) string {
	if peer.Label != "" {
		return peer.Label
	}
	if peer.InMesh {
		return peer.Principal
	}
	return "a sender outside the mesh"
}

func stringField(obj map[string]any, path ...string) string {
	v, _, _ := unstructured.NestedString(obj, path...)
	return v
}

func mapField(obj map[string]any, path ...string) (map[string]any, bool) {
	v, ok, _ := unstructured.NestedMap(obj, path...)
	return v, ok && v != nil
}

func sliceField(obj map[string]any, path ...string) ([]any, bool) {
	v, ok, _ := unstructured.NestedSlice(obj, path...)
	return v, ok
}

func stringList(raw any) ([]string, bool) {
	items, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out, true
}

func toStringMap(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = fmt.Sprint(v)
	}
	return out
}
