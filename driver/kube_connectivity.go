package driver

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/kubeexplain"
	"github.com/wcrum/gofi/spec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// connectivityPath is one direction of traffic a flow needs, described for
// the GoFi namespace's Package (local) and, for in-cluster peers, for the
// peer namespace's Package (remote, the mirror direction).
type connectivityPath struct {
	id, from, to, protocol string
	port                   int32
	local                  kubeexplain.UDSPath
	remote                 *kubeexplain.UDSPath
	remoteNamespace        string
}

// AnalyzeConnectivity verifies every path a flow needs against UDS Packages:
// GoFi's own Package as GoFi would apply it (checked by Pepr admission with a
// server-side dry run), and the Package of each in-cluster peer, which must
// admit the mirror direction. def may be unsaved; all are the stored flows.
func (k *Kube) AnalyzeConnectivity(ctx context.Context, def spec.FlowDefinition, all []spec.FlowDefinition) (*gofiv1.AnalyzeConnectivityResponse, error) {
	out := &gofiv1.AnalyzeConnectivityResponse{Namespace: k.namespace, Limits: []string{
		"This checks UDS Package intent. The UDS operator turns Packages into NetworkPolicies and Istio AuthorizationPolicies; Istio ambient enforces L4 policy for TCP only, and UDP is governed by NetworkPolicies.",
		"Firewalls, cloud security groups, and CNI-specific policies outside Kubernetes are not visible here.",
	}}
	if k.dyn == nil {
		return nil, fmt.Errorf("connectivity analysis needs the Kubernetes dynamic client")
	}
	flows := make([]spec.FlowDefinition, 0, len(all)+1)
	for _, other := range all {
		if other.Name != def.Name {
			flows = append(flows, other)
		}
	}
	flows = append(flows, def)

	// GoFi's own Package: the one GoFi would apply (verified by Pepr), or the
	// live one when GoFi does not manage allows.
	manage := k.cluster != nil && k.cluster.Opts.ManageUDSAllows
	var local []unstructured.Unstructured
	packages := k.dyn.Resource(udsPackages)
	if manage {
		desired, err := k.desiredUDSPackage(ctx, flows)
		if err != nil {
			out.Engines = append(out.Engines, &gofiv1.PolicyEngine{Name: "pepr", Present: true, Note: "Could not build GoFi's Package: " + err.Error()})
		} else {
			local = []unstructured.Unstructured{*desired}
			_, err := packages.Namespace(k.namespace).Apply(ctx, desired.GetName(), desired, metav1.ApplyOptions{FieldManager: udsFieldManager, Force: true, DryRun: []string{metav1.DryRunAll}})
			note := "Pepr admission accepts GoFi's Package with this flow."
			if err != nil {
				note = "Pepr admission rejects GoFi's Package with this flow: " + err.Error()
			}
			out.Engines = append(out.Engines, &gofiv1.PolicyEngine{Name: "pepr", Present: true, Note: note})
		}
	} else if list, err := packages.Namespace(k.namespace).List(ctx, metav1.ListOptions{}); err == nil {
		local = list.Items
	}

	// Every Package GoFi may read, by namespace, for the peer side.
	remote := map[string][]unstructured.Unstructured{}
	canReadRemote := k.cluster != nil && k.cluster.Opts.ClusterWide
	if canReadRemote {
		if list, err := packages.List(ctx, metav1.ListOptions{}); err == nil {
			for _, item := range list.Items {
				remote[item.GetNamespace()] = append(remote[item.GetNamespace()], item)
			}
		} else {
			canReadRemote = false
		}
	}
	udsPresent := len(local) > 0 || len(remote) > 0
	out.Engines = append(out.Engines, &gofiv1.PolicyEngine{Name: "uds", Present: udsPresent, Note: map[bool]string{true: "UDS Packages govern these namespaces with default-deny networking.", false: "No UDS Packages found."}[udsPresent]})
	if !udsPresent {
		return out, nil
	}
	for ns, items := range remote {
		for _, item := range items {
			if phase, _, _ := unstructured.NestedString(item.Object, "status", "phase"); phase != "" && phase != "Ready" && ns != k.namespace {
				out.Limits = append(out.Limits, fmt.Sprintf("Package %s/%s is %s, so its policies may not match its spec.", ns, item.GetName(), phase))
			}
		}
	}

	for _, path := range k.connectivityPaths(ctx, def) {
		out.Paths = append(out.Paths, k.verifyPath(path, local, remote, canReadRemote, manage))
	}
	return out, nil
}

func (k *Kube) verifyPath(path connectivityPath, local []unstructured.Unstructured, remote map[string][]unstructured.Unstructured, canReadRemote, manage bool) *gofiv1.ConnectivityPath {
	result := &gofiv1.ConnectivityPath{Id: path.id, Direction: strings.ToLower(string(path.local.Direction)), From: path.from, To: path.to, Port: path.port, Protocol: path.protocol, Verdict: "allowed"}
	addFindings := func(findings []kubeexplain.Finding) {
		for _, f := range findings {
			result.Findings = append(result.Findings, &gofiv1.PolicyFinding{Engine: f.Engine, Kind: f.Kind, Name: f.Name, Namespace: f.Namespace, Effect: f.Effect, Detail: f.Detail})
		}
	}
	mine := kubeexplain.EvaluateUDS(local, k.namespace, path.local)
	addFindings(mine.Findings)
	var problems []string
	if mine.Governed && !mine.Covered {
		result.Verdict = "blocked"
		problems = append(problems, "GoFi's Package does not allow it")
		title := "Add to GoFi's Package " + k.namespace + "/" + mine.Package
		if manage {
			title = "GoFi should generate this allow; report this as a bug: " + title
		}
		result.Fixes = append(result.Fixes, &gofiv1.SuggestedFix{Engine: "uds", Title: title, Yaml: kubeexplain.UDSAllowFix(path.local)})
	}
	if path.remote != nil {
		switch {
		case !canReadRemote:
			if result.Verdict == "allowed" {
				result.Verdict = "unknown"
			}
			problems = append(problems, fmt.Sprintf("namespace %s's Package could not be read (cluster discovery is off)", path.remoteNamespace))
		default:
			theirs := kubeexplain.EvaluateUDS(remote[path.remoteNamespace], path.remoteNamespace, *path.remote)
			addFindings(theirs.Findings)
			if theirs.Governed && !theirs.Covered {
				result.Verdict = "blocked"
				problems = append(problems, fmt.Sprintf("Package %s/%s does not allow it", path.remoteNamespace, theirs.Package))
				result.Fixes = append(result.Fixes, &gofiv1.SuggestedFix{Engine: "uds", Title: fmt.Sprintf("Add to Package %s/%s (owned by that application)", path.remoteNamespace, theirs.Package), Yaml: kubeexplain.UDSAllowFix(*path.remote)})
			}
		}
	}
	switch result.Verdict {
	case "allowed":
		result.Summary = "Allowed by both sides' UDS Packages."
		if path.remote == nil {
			result.Summary = "Allowed by GoFi's UDS Package."
		}
	default:
		result.Summary = strings.ToUpper(problems[0][:1]) + problems[0][1:] + "."
		if len(problems) > 1 {
			result.Summary = strings.ToUpper(problems[0][:1]) + problems[0][1:] + ", and " + strings.Join(problems[1:], ", ") + "."
		}
	}
	return result
}

// connectivityPaths lists the traffic a flow needs outside its namespace.
func (k *Kube) connectivityPaths(ctx context.Context, def spec.FlowDefinition) []connectivityPath {
	flowLabels := map[string]string{kubeLabelManaged: "true", kubeLabelListener: def.Name}
	var paths []connectivityPath
	if listen := def.Source.Listen; listen != nil && listen.Service != nil {
		protocol := strings.ToUpper(listenNetwork(listen))
		port := int32(listen.Port)
		base := kubeexplain.UDSPath{Direction: kubeexplain.Ingress, Selector: flowLabels, Port: port, Protocol: protocol, Description: fmt.Sprintf("GoFi flow %s ingress", def.Name)}
		to := "flow " + def.Name
		switch listen.Service.Type {
		case "NodePort", "LoadBalancer":
			p := base
			p.RemoteAnywhere = true
			paths = append(paths, connectivityPath{id: "ingress-external", from: "outside the cluster (" + listen.Service.Type + ")", to: to, port: port, protocol: protocol, local: p})
		case "Gateway":
			if gw := listen.Service.Gateway; gw != nil {
				p := base
				p.RemoteNamespace = gw.Namespace
				mirror := kubeexplain.UDSPath{Direction: kubeexplain.Egress, RemoteNamespace: k.namespace, RemoteSelector: flowLabels, Port: port, Protocol: protocol, Description: fmt.Sprintf("Gateway %s to GoFi flow %s", gw.Name, def.Name)}
				paths = append(paths, connectivityPath{id: "ingress-gateway", from: "Gateway " + gw.Namespace + "/" + gw.Name, to: to, port: port, protocol: protocol, local: p, remote: &mirror, remoteNamespace: gw.Namespace})
			}
		default:
			if len(listen.Service.AllowFrom) == 0 {
				p := base
				p.RemoteNamespace = "(any namespace)"
				paths = append(paths, connectivityPath{id: "ingress-cluster", from: "any namespace", to: to, port: port, protocol: protocol, local: p})
			}
			for i, peer := range listen.Service.AllowFrom {
				p := base
				p.RemoteNamespace, p.RemoteSelector = peer.Namespace, peer.Selector
				mirror := kubeexplain.UDSPath{Direction: kubeexplain.Egress, Selector: peer.Selector, RemoteNamespace: k.namespace, RemoteSelector: flowLabels, Port: port, Protocol: protocol,
					Description: fmt.Sprintf("%s to GoFi flow %s", peerName(peer), def.Name)}
				paths = append(paths, connectivityPath{id: fmt.Sprintf("ingress-peer-%d", i), from: "Package " + peerName(peer) + selectorText(peer.Selector), to: to, port: port, protocol: protocol, local: p, remote: &mirror, remoteNamespace: peer.Namespace})
			}
		}
	}
	add := func(id, label, rawURL, protocol string, target *spec.KubernetesTarget) {
		from := "flow " + def.Name
		if target != nil {
			if target.Namespace == "" || target.Namespace == k.namespace {
				return
			}
			port := int32(target.Port)
			p := kubeexplain.UDSPath{Direction: kubeexplain.Egress, Selector: flowLabels, RemoteNamespace: target.Namespace, RemoteSelector: target.Selector, Port: port, Protocol: protocol, Description: fmt.Sprintf("GoFi flow %s to %s pods", def.Name, target.Namespace)}
			mirror := kubeexplain.UDSPath{Direction: kubeexplain.Ingress, Selector: target.Selector, RemoteNamespace: k.namespace, RemoteSelector: flowLabels, Port: port, Protocol: protocol, Description: fmt.Sprintf("GoFi flow %s ingress", def.Name)}
			paths = append(paths, connectivityPath{id: id, from: from, to: label + " · pods " + target.Namespace + selectorText(target.Selector), port: port, protocol: protocol, local: p, remote: &mirror, remoteNamespace: target.Namespace})
			return
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Hostname() == "" {
			return
		}
		host := parsed.Hostname()
		portNumber, _ := strconv.Atoi(parsed.Port())
		if portNumber == 0 {
			portNumber = defaultPorts[parsed.Scheme]
		}
		port := int32(portNumber)
		if labels := strings.Split(host, "."); len(labels) >= 3 && labels[2] == "svc" {
			namespace := labels[1]
			if namespace == k.namespace {
				return
			}
			p := kubeexplain.UDSPath{Direction: kubeexplain.Egress, Selector: flowLabels, RemoteNamespace: namespace, Port: port, Protocol: protocol, Description: fmt.Sprintf("GoFi flow %s to %s", def.Name, host)}
			var mirror *kubeexplain.UDSPath
			// The peer Package allows Pod ports: resolve the Service's
			// selector and target port.
			if svc, err := k.cs.CoreV1().Services(namespace).Get(ctx, labels[0], metav1.GetOptions{}); err == nil {
				p.RemoteSelector = svc.Spec.Selector
				podPort := port
				for _, sp := range svc.Spec.Ports {
					if sp.Port == port && sp.TargetPort.IntValue() > 0 {
						podPort = int32(sp.TargetPort.IntValue())
					}
				}
				mirror = &kubeexplain.UDSPath{Direction: kubeexplain.Ingress, Selector: svc.Spec.Selector, RemoteNamespace: k.namespace, RemoteSelector: flowLabels, Port: podPort, Protocol: protocol, Description: fmt.Sprintf("GoFi flow %s ingress", def.Name)}
			}
			paths = append(paths, connectivityPath{id: id, from: from, to: label + " · " + host, port: port, protocol: protocol, local: p, remote: mirror, remoteNamespace: namespace})
			return
		}
		if !strings.Contains(host, ".") && net.ParseIP(host) == nil {
			return // a bare name resolves inside the GoFi namespace
		}
		p := kubeexplain.UDSPath{Direction: kubeexplain.Egress, Selector: flowLabels, RemoteHost: host, Port: port, Protocol: protocol, Description: fmt.Sprintf("GoFi flow %s to %s", def.Name, host)}
		if protocol == "UDP" && net.ParseIP(host) == nil {
			p.RemoteHost, p.RemoteAnywhere = "", true // UDS cannot match UDP by host name
		}
		paths = append(paths, connectivityPath{id: id, from: from, to: label + " · " + host, port: port, protocol: protocol, local: p})
	}
	sinks := append([]spec.SinkCfg{}, def.Sinks...)
	for i, sink := range sinks {
		protocol := "TCP"
		if sink.Type == "udp" {
			protocol = "UDP"
		}
		add(fmt.Sprintf("egress-sink-%d", i), fmt.Sprintf("destination %d (%s)", i+1, sink.Type), sink.URL, protocol, sink.Kubernetes)
	}
	if dl := def.DeadLetterSink; dl != nil {
		protocol := "TCP"
		if dl.Type == "udp" {
			protocol = "UDP"
		}
		add("egress-deadletter", "dead letter ("+dl.Type+")", dl.URL, protocol, dl.Kubernetes)
	}
	if source := sourceURL(def.Source); source != "" {
		// A pulled source connects out; the data then flows in on that connection.
		add("egress-source", "source", source, "TCP", nil)
	}
	sort.SliceStable(paths, func(i, j int) bool { return paths[i].id < paths[j].id })
	return paths
}

func sourceURL(source spec.FlowSource) string {
	switch {
	case source.Fetch != nil && source.Fetch.HTTP != nil:
		return source.Fetch.HTTP.URL
	case source.Subscribe != nil && source.Subscribe.NATS != nil:
		return source.Subscribe.NATS.URL
	case source.Subscribe != nil && source.Subscribe.MQTT != nil:
		return source.Subscribe.MQTT.URL
	case source.Subscribe != nil && source.Subscribe.WebSocket != nil:
		return source.Subscribe.WebSocket.URL
	case source.Subscribe != nil && source.Subscribe.SSE != nil:
		return source.Subscribe.SSE.URL
	}
	return ""
}

func selectorText(selector map[string]string) string {
	if len(selector) == 0 {
		return ""
	}
	parts := make([]string, 0, len(selector))
	for k, v := range selector {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return " (" + strings.Join(parts, ",") + ")"
}
