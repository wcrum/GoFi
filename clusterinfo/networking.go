package clusterinfo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// source is one kind of networking object GoFi lists.
type source struct {
	group, resource, kind, engine string
	summarize                     func(obj map[string]any) (string, []string)
}

var sources = []source{
	{"networking.k8s.io", "networkpolicies", "NetworkPolicy", "kubernetes", summarizeNetworkPolicy},
	{"security.istio.io", "authorizationpolicies", "AuthorizationPolicy", "istio", summarizeAuthorizationPolicy},
	{"security.istio.io", "peerauthentications", "PeerAuthentication", "istio", summarizePeerAuthentication},
	{"networking.istio.io", "gateways", "Gateway", "istio", summarizeIstioGateway},
	{"networking.istio.io", "virtualservices", "VirtualService", "istio", summarizeVirtualService},
	{"networking.istio.io", "serviceentries", "ServiceEntry", "istio", summarizeServiceEntry},
	{"gateway.networking.k8s.io", "gateways", "Gateway", "gateway-api", summarizeGateway},
	{"gateway.networking.k8s.io", "httproutes", "HTTPRoute", "gateway-api", summarizeRoute},
	{"gateway.networking.k8s.io", "grpcroutes", "GRPCRoute", "gateway-api", summarizeRoute},
	{"gateway.networking.k8s.io", "tlsroutes", "TLSRoute", "gateway-api", summarizeRoute},
	{"gateway.networking.k8s.io", "tcproutes", "TCPRoute", "gateway-api", summarizeRoute},
	{"gateway.networking.k8s.io", "udproutes", "UDPRoute", "gateway-api", summarizeRoute},
	{"uds.dev", "packages", "Package", "uds", summarizePackage},
	{"metallb.io", "ipaddresspools", "IPAddressPool", "metallb", summarizePool},
	{"metallb.io", "l2advertisements", "L2Advertisement", "metallb", summarizeAdvertisement},
}

// FlowLabels are the labels of each flow's runtime Pod, used to report which
// flows a policy or route affects.
type FlowLabels map[string]map[string]string

// Networking lists traffic-shaping objects in one namespace or all readable ones.
func (c *Client) Networking(ctx context.Context, namespace string, flows FlowLabels) (*gofiv1.ListClusterNetworkingResponse, error) {
	versions, err := c.groupVersions()
	if err != nil {
		return nil, fmt.Errorf("API discovery: %w", err)
	}
	out := &gofiv1.ListClusterNetworkingResponse{}
	namespaces := map[string]bool{}
	for _, src := range sources {
		version := c.versionFor(src.group, src.resource)
		items, err := c.list(ctx, versions, src.group, src.resource, namespace, &out.Warnings)
		if err != nil {
			out.Warnings = append(out.Warnings, warning(src.kind+" ("+src.group+")", err))
			continue
		}
		for _, item := range items {
			summary, details := src.summarize(item.Object)
			namespaces[item.GetNamespace()] = true
			out.Objects = append(out.Objects, &gofiv1.NetworkingObject{Kind: src.kind, ApiVersion: src.group + "/" + version, Namespace: item.GetNamespace(), Name: item.GetName(),
				Engine: src.engine, Summary: summary, Details: details, Yaml: toYAML(item), AffectedFlows: c.affected(src.kind, item, flows)})
		}
	}
	// Services that expose traffic from outside the cluster.
	serviceNamespace := namespace
	if serviceNamespace == "" && !c.Opts.ClusterWide {
		serviceNamespace = c.Opts.Namespace
	}
	services, err := c.listServices(ctx, serviceNamespace, &out.Warnings)
	if err != nil {
		out.Warnings = append(out.Warnings, warning("Services", err))
	} else {
		for _, svc := range services {
			if svc.Spec.Type != corev1.ServiceTypeLoadBalancer && svc.Spec.Type != corev1.ServiceTypeNodePort {
				continue
			}
			raw, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&svc)
			obj := unstructured.Unstructured{Object: raw}
			obj.SetAPIVersion("v1")
			obj.SetKind("Service")
			namespaces[svc.Namespace] = true
			summary, details := summarizeService(&svc)
			out.Objects = append(out.Objects, &gofiv1.NetworkingObject{Kind: "Service", ApiVersion: "v1", Namespace: svc.Namespace, Name: svc.Name, Engine: "kubernetes",
				Summary: summary, Details: details, Yaml: toYAML(obj), AffectedFlows: c.selectorFlows(svc.Namespace, svc.Spec.Selector, flows)})
		}
	}
	sort.Slice(out.Objects, func(i, j int) bool {
		a, b := out.Objects[i], out.Objects[j]
		return a.Namespace+"/"+a.Kind+"/"+a.Name < b.Namespace+"/"+b.Kind+"/"+b.Name
	})
	for ns := range namespaces {
		if ns != "" {
			out.Namespaces = append(out.Namespaces, ns)
		}
	}
	sort.Strings(out.Namespaces)
	return out, nil
}

// affected names the flows whose runtime Pods an object selects.
func (c *Client) affected(kind string, item unstructured.Unstructured, flows FlowLabels) []string {
	if item.GetNamespace() != c.Opts.Namespace && !(kind == "AuthorizationPolicy" || kind == "PeerAuthentication") {
		return nil
	}
	switch kind {
	case "NetworkPolicy":
		selector, _, _ := unstructured.NestedMap(item.Object, "spec", "podSelector", "matchLabels")
		return c.selectorFlows(item.GetNamespace(), stringMap(selector), flows)
	case "AuthorizationPolicy", "PeerAuthentication":
		if item.GetNamespace() != c.Opts.Namespace && item.GetNamespace() != "istio-system" {
			return nil
		}
		selector, _, _ := unstructured.NestedMap(item.Object, "spec", "selector", "matchLabels")
		return c.selectorFlows(c.Opts.Namespace, stringMap(selector), flows)
	case "Package":
		var names []string
		for _, list := range [][]any{slice(item.Object, "spec", "network", "allow"), slice(item.Object, "spec", "network", "expose")} {
			for _, raw := range list {
				entry, _ := raw.(map[string]any)
				selector, _ := entry["selector"].(map[string]any)
				names = append(names, c.selectorFlows(item.GetNamespace(), stringMap(selector), flows)...)
			}
		}
		return unique(names)
	case "TCPRoute", "UDPRoute":
		var names []string
		for _, raw := range slice(item.Object, "spec", "rules") {
			rule, _ := raw.(map[string]any)
			for _, ref := range slice(rule, "backendRefs") {
				backend, _ := ref.(map[string]any)
				for flow := range flows {
					if strings.HasPrefix(fmt.Sprint(backend["name"]), "gofi-listener-"+flow) {
						names = append(names, flow)
					}
				}
			}
		}
		return unique(names)
	}
	return nil
}

func (c *Client) selectorFlows(namespace string, selector map[string]string, flows FlowLabels) []string {
	if namespace != c.Opts.Namespace || selector == nil {
		return nil
	}
	sel := labels.SelectorFromSet(selector)
	var names []string
	for flow, podLabels := range flows {
		if sel.Matches(labels.Set(podLabels)) {
			names = append(names, flow)
		}
	}
	sort.Strings(names)
	return names
}

func toYAML(obj unstructured.Unstructured) string {
	obj = *obj.DeepCopy()
	obj.SetManagedFields(nil)
	annotations := obj.GetAnnotations()
	delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
	if len(annotations) == 0 {
		annotations = nil
	}
	obj.SetAnnotations(annotations)
	data, err := yaml.Marshal(obj.Object)
	if err != nil {
		return ""
	}
	return string(data)
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func peerText(peer map[string]any) string {
	var parts []string
	if pods, ok := peer["podSelector"].(map[string]any); ok {
		parts = append(parts, "pods "+labelString(stringMap(pods["matchLabels"])))
	}
	if ns, ok := peer["namespaceSelector"].(map[string]any); ok {
		parts = append(parts, "namespaces "+labelString(stringMap(ns["matchLabels"])))
	}
	if block, ok := peer["ipBlock"].(map[string]any); ok {
		parts = append(parts, "CIDR "+fmt.Sprint(block["cidr"]))
	}
	if len(parts) == 0 {
		return "anything"
	}
	return strings.Join(parts, " in ")
}

func portsText(ports []any) string {
	if len(ports) == 0 {
		return "all ports"
	}
	var parts []string
	for _, raw := range ports {
		port, _ := raw.(map[string]any)
		text := fmt.Sprint(port["port"])
		if port["port"] == nil {
			text = "any"
		}
		if protocol, ok := port["protocol"].(string); ok {
			text += "/" + protocol
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, ", ")
}

func summarizeNetworkPolicy(obj map[string]any) (string, []string) {
	selector, _, _ := unstructured.NestedMap(obj, "spec", "podSelector", "matchLabels")
	var types []string
	for _, t := range slice(obj, "spec", "policyTypes") {
		types = append(types, fmt.Sprint(t))
	}
	var details []string
	for _, direction := range []struct{ key, peers, verb string }{{"ingress", "from", "from"}, {"egress", "to", "to"}} {
		for _, raw := range slice(obj, "spec", direction.key) {
			rule, _ := raw.(map[string]any)
			peers := slice(rule, direction.peers)
			var who []string
			for _, p := range peers {
				peer, _ := p.(map[string]any)
				who = append(who, peerText(peer))
			}
			if len(who) == 0 {
				who = []string{"anything"}
			}
			details = append(details, fmt.Sprintf("allow %s %s %s on %s", direction.key, direction.verb, strings.Join(who, " or "), portsText(slice(rule, "ports"))))
		}
	}
	summary := fmt.Sprintf("Selects pods %s · %s", labelString(stringMap(selector)), strings.Join(types, "+"))
	if len(details) == 0 {
		summary += " · denies all selected traffic"
	}
	return summary, details
}

func summarizeAuthorizationPolicy(obj map[string]any) (string, []string) {
	action := str(obj, "spec", "action")
	if action == "" {
		action = "ALLOW"
	}
	selector, _, _ := unstructured.NestedMap(obj, "spec", "selector", "matchLabels")
	target := "all workloads in namespace"
	if len(selector) > 0 {
		target = "workloads " + labelString(stringMap(selector))
	}
	var details []string
	for _, raw := range slice(obj, "spec", "rules") {
		rule, _ := raw.(map[string]any)
		var parts []string
		for _, f := range slice(rule, "from") {
			from, _ := f.(map[string]any)
			source, _ := from["source"].(map[string]any)
			for key, value := range source {
				parts = append(parts, fmt.Sprintf("%s=%v", key, value))
			}
		}
		for _, t := range slice(rule, "to") {
			to, _ := t.(map[string]any)
			operation, _ := to["operation"].(map[string]any)
			if ports, ok := operation["ports"]; ok {
				parts = append(parts, fmt.Sprintf("ports=%v", ports))
			}
		}
		if len(parts) == 0 {
			parts = []string{"any source"}
		}
		sort.Strings(parts)
		details = append(details, strings.Join(parts, " · "))
	}
	if len(slice(obj, "spec", "rules")) == 0 && action == "ALLOW" {
		return fmt.Sprintf("%s for %s with no rules: denies all", action, target), details
	}
	return fmt.Sprintf("%s for %s · %d rule(s)", action, target, len(details)), details
}

func summarizePeerAuthentication(obj map[string]any) (string, []string) {
	mode := str(obj, "spec", "mtls", "mode")
	if mode == "" {
		mode = "inherit"
	}
	selector, _, _ := unstructured.NestedMap(obj, "spec", "selector", "matchLabels")
	var details []string
	ports, _, _ := unstructured.NestedMap(obj, "spec", "portLevelMtls")
	for port, raw := range ports {
		entry, _ := raw.(map[string]any)
		details = append(details, fmt.Sprintf("port %s: %v", port, entry["mode"]))
	}
	sort.Strings(details)
	return fmt.Sprintf("mTLS %s for %s", mode, labelString(stringMap(selector))), details
}

func summarizeIstioGateway(obj map[string]any) (string, []string) {
	selector, _, _ := unstructured.NestedMap(obj, "spec", "selector")
	var details []string
	for _, raw := range slice(obj, "spec", "servers") {
		server, _ := raw.(map[string]any)
		port, _ := server["port"].(map[string]any)
		details = append(details, fmt.Sprintf("%v %v hosts %v", port["protocol"], port["number"], server["hosts"]))
	}
	return fmt.Sprintf("Istio ingress gateway %s · %d server(s)", labelString(stringMap(selector)), len(details)), details
}

func summarizeVirtualService(obj map[string]any) (string, []string) {
	var hosts, gateways []string
	for _, h := range slice(obj, "spec", "hosts") {
		hosts = append(hosts, fmt.Sprint(h))
	}
	for _, g := range slice(obj, "spec", "gateways") {
		gateways = append(gateways, fmt.Sprint(g))
	}
	var details []string
	for _, kind := range []string{"http", "tcp", "tls"} {
		for _, raw := range slice(obj, "spec", kind) {
			route, _ := raw.(map[string]any)
			for _, d := range slice(route, "route") {
				dest, _ := d.(map[string]any)
				target, _ := dest["destination"].(map[string]any)
				details = append(details, fmt.Sprintf("%s → %v:%v", kind, target["host"], nestedOr(target, "port", "number")))
			}
		}
	}
	return fmt.Sprintf("Hosts %s via %s", strings.Join(hosts, ", "), strings.Join(gateways, ", ")), details
}

func nestedOr(obj map[string]any, path ...string) any {
	value, found, _ := unstructured.NestedFieldNoCopy(obj, path...)
	if !found {
		return ""
	}
	return value
}

func summarizeServiceEntry(obj map[string]any) (string, []string) {
	var hosts []string
	for _, h := range slice(obj, "spec", "hosts") {
		hosts = append(hosts, fmt.Sprint(h))
	}
	return fmt.Sprintf("%s %s", firstNonEmpty(str(obj, "spec", "location"), "MESH_EXTERNAL"), strings.Join(hosts, ", ")), nil
}

func summarizeGateway(obj map[string]any) (string, []string) {
	var details []string
	for _, raw := range slice(obj, "spec", "listeners") {
		listener, _ := raw.(map[string]any)
		details = append(details, fmt.Sprintf("%v %v/%v routes from %s", listener["name"], listener["protocol"], listener["port"], allowedRoutes(listener)))
	}
	state := "not programmed"
	if conditionTrue(obj, "Programmed") {
		state = "programmed"
	}
	return fmt.Sprintf("Class %s · %d listener(s) · %s", str(obj, "spec", "gatewayClassName"), len(details), state), details
}

func summarizeRoute(obj map[string]any) (string, []string) {
	var parents, details []string
	for _, raw := range slice(obj, "spec", "parentRefs") {
		ref, _ := raw.(map[string]any)
		parent := fmt.Sprint(ref["name"])
		if ns, ok := ref["namespace"]; ok {
			parent = fmt.Sprint(ns) + "/" + parent
		}
		if section, ok := ref["sectionName"]; ok {
			parent += ":" + fmt.Sprint(section)
		}
		parents = append(parents, parent)
	}
	for _, raw := range slice(obj, "spec", "rules") {
		rule, _ := raw.(map[string]any)
		for _, b := range slice(rule, "backendRefs") {
			backend, _ := b.(map[string]any)
			details = append(details, fmt.Sprintf("→ %v:%v", backend["name"], backend["port"]))
		}
	}
	accepted := "accepted"
	for _, raw := range slice(obj, "status", "parents") {
		status, _ := raw.(map[string]any)
		for _, c := range slice(status, "conditions") {
			condition, _ := c.(map[string]any)
			if condition["type"] == "Accepted" && condition["status"] != "True" {
				accepted = fmt.Sprintf("not accepted: %v", condition["message"])
			}
		}
	}
	return fmt.Sprintf("Attached to %s · %s", strings.Join(parents, ", "), accepted), details
}

func summarizePackage(obj map[string]any) (string, []string) {
	var details []string
	for _, raw := range slice(obj, "spec", "network", "expose") {
		entry, _ := raw.(map[string]any)
		details = append(details, fmt.Sprintf("expose %v.%v → %v:%v", entry["host"], firstNonEmpty(fmt.Sprint(entry["gateway"]), "tenant"), entry["service"], entry["port"]))
	}
	for _, raw := range slice(obj, "spec", "network", "allow") {
		entry, _ := raw.(map[string]any)
		peer := firstNonEmpty(str(entry, "remoteGenerated"), str(entry, "remoteNamespace"), str(entry, "remoteHost"), "any")
		if remote, ok := entry["remoteSelector"]; ok {
			peer += " " + labelString(stringMap(remote))
		}
		port := "all ports"
		if p, ok := entry["port"]; ok {
			port = fmt.Sprint(p)
		}
		details = append(details, fmt.Sprintf("allow %v %s on %s %s", entry["direction"], peer, port, str(entry, "remoteProtocol")))
	}
	phase := str(obj, "status", "phase")
	return fmt.Sprintf("UDS Package · %d expose, %d allow · %s", len(slice(obj, "spec", "network", "expose")), len(slice(obj, "spec", "network", "allow")), firstNonEmpty(phase, "no status")), details
}

func summarizePool(obj map[string]any) (string, []string) {
	var addresses []string
	for _, a := range slice(obj, "spec", "addresses") {
		addresses = append(addresses, fmt.Sprint(a))
	}
	return "Addresses " + strings.Join(addresses, ", "), nil
}

func summarizeAdvertisement(obj map[string]any) (string, []string) {
	var pools []string
	for _, p := range slice(obj, "spec", "ipAddressPools") {
		pools = append(pools, fmt.Sprint(p))
	}
	if len(pools) == 0 {
		pools = []string{"all pools"}
	}
	return "Layer 2 announcement for " + strings.Join(pools, ", "), nil
}

func summarizeService(svc *corev1.Service) (string, []string) {
	var details []string
	for _, port := range svc.Spec.Ports {
		text := fmt.Sprintf("%d/%s → %s", port.Port, port.Protocol, port.TargetPort.String())
		if port.NodePort != 0 {
			text += fmt.Sprintf(" (node %d)", port.NodePort)
		}
		details = append(details, text)
	}
	summary := string(svc.Spec.Type)
	if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
		var addresses []string
		for _, ingress := range svc.Status.LoadBalancer.Ingress {
			addresses = append(addresses, firstNonEmpty(ingress.IP, ingress.Hostname))
		}
		if len(addresses) == 0 {
			addresses = []string{"pending address"}
		}
		summary += " " + strings.Join(addresses, ", ")
		if svc.Spec.LoadBalancerClass != nil {
			summary += " · class " + *svc.Spec.LoadBalancerClass
		}
	}
	if svc.Spec.ExternalTrafficPolicy != "" {
		summary += " · externalTrafficPolicy " + string(svc.Spec.ExternalTrafficPolicy)
	}
	return summary, details
}
