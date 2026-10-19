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
	"github.com/wcrum/gofi/observability"
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

// ConnectivityRuntime is what the running flow contributes to the analysis.
type ConnectivityRuntime struct {
	// Observed is the runtime's route health; joined by path ID.
	Observed []*gofiv1.RouteHealth
	// Saved is true when the analyzed definition routes like the saved one,
	// so GoFi's Package for the saved flows already includes it.
	Saved bool
}

// AnalyzeConnectivity verifies every path a flow needs against UDS Packages:
// GoFi's own Package as GoFi would apply it (checked by Pepr admission with a
// server-side dry run) and as it is live, and the Package of each in-cluster
// peer, which must admit the mirror direction. def may be unsaved; all are
// the stored flows. Observed failures are joined to the verdict per path.
func (k *Kube) AnalyzeConnectivity(ctx context.Context, def spec.FlowDefinition, all []spec.FlowDefinition, runtime ConnectivityRuntime) (*gofiv1.AnalyzeConnectivityResponse, error) {
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
	// live one when GoFi does not manage allows. A managed Package is also
	// checked live, since a path GoFi would allow is still blocked until the
	// Package is applied.
	manage := k.cluster != nil && k.cluster.Opts.ManageUDSAllows
	var local, live []unstructured.Unstructured
	packages := k.dyn.Resource(udsPackages)
	if list, err := packages.Namespace(k.namespace).List(ctx, metav1.ListOptions{}); err == nil {
		live = list.Items
	}
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
	} else {
		local = live
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

	observed := map[string]*gofiv1.RouteHealth{}
	for _, route := range runtime.Observed {
		observed[route.Route] = route
	}
	sides := packageSides{local: local, live: live, remote: remote, canReadRemote: canReadRemote, manage: manage, saved: runtime.Saved}
	for _, path := range k.connectivityPaths(ctx, def) {
		out.Paths = append(out.Paths, k.verifyPath(path, sides, observed[path.id]))
	}
	return out, nil
}

// packageSides are the Packages one analysis checks paths against.
type packageSides struct {
	// local is GoFi's Package as it would be applied; live as it is.
	local, live   []unstructured.Unstructured
	remote        map[string][]unstructured.Unstructured
	canReadRemote bool
	manage, saved bool
}

// Notes on who makes a Package change.
const (
	noteManagedLag = "GoFi generates this allow from the saved flows, but the live Package does not have it yet (the last reconcile failed or has not run). Review the change and apply GoFi's Package."
	noteUnsaved    = "GoFi adds this allow to its Package when the flow is saved."
	noteUnmanaged  = "GoFi does not manage its Package (uds.manageAllows is off). Add this entry to additionalNetworkAllow in the uds-gofi-config chart values and redeploy, or enable uds.manageAllows with gofiManagesPackage so GoFi keeps it in step."
	notePeer       = "This Package belongs to another application and is deployed with it, so GoFi only reads it. Append this entry to network.allow in that application's Package source (its chart or bundle values); a merge patch on the live object would replace the whole allow list and be reverted on its next deploy."
)

func (k *Kube) verifyPath(path connectivityPath, sides packageSides, observed *gofiv1.RouteHealth) *gofiv1.ConnectivityPath {
	result := &gofiv1.ConnectivityPath{Id: path.id, Direction: strings.ToLower(string(path.local.Direction)), From: path.from, To: path.to, Port: path.port, Protocol: path.protocol, Verdict: "allowed", Observed: observed}
	addFindings := func(findings []kubeexplain.Finding) {
		for _, f := range findings {
			result.Findings = append(result.Findings, &gofiv1.PolicyFinding{Engine: f.Engine, Kind: f.Kind, Name: f.Name, Namespace: f.Namespace, Effect: f.Effect, Detail: f.Detail})
		}
	}
	mine := kubeexplain.EvaluateUDS(sides.local, k.namespace, path.local)
	addFindings(mine.Findings)
	var problems []string
	switch {
	case mine.Governed && !mine.Covered:
		result.Verdict = "blocked"
		problems = append(problems, "GoFi's Package does not allow it")
		fix := &gofiv1.SuggestedFix{Engine: "uds", Title: "Add to GoFi's Package " + k.namespace + "/" + mine.Package, Yaml: kubeexplain.UDSAllowFix(path.local), Note: noteUnmanaged}
		if sides.manage {
			fix.Title, fix.Note = "GoFi should generate this allow; report this as a bug: "+fix.Title, ""
		}
		result.Fixes = append(result.Fixes, fix)
	case mine.Covered && sides.manage:
		current := kubeexplain.EvaluateUDS(sides.live, k.namespace, path.local)
		if current.Governed && !current.Covered {
			result.Verdict = "blocked"
			fix := &gofiv1.SuggestedFix{Engine: "uds", Title: "Apply GoFi's Package " + k.namespace + "/" + current.Package, Yaml: kubeexplain.UDSAllowFix(path.local), Note: noteUnsaved}
			if sides.saved {
				problems = append(problems, "GoFi's live Package does not allow it yet")
				fix.Action, fix.Note = "apply-gofi-package", noteManagedLag
				for _, f := range current.Findings {
					f.Detail = "Live: " + f.Detail
					addFindings([]kubeexplain.Finding{f})
				}
			} else {
				problems = append(problems, "GoFi's Package allows it once the flow is saved")
			}
			result.Fixes = append(result.Fixes, fix)
		}
	}
	if path.remote != nil {
		switch {
		case !sides.canReadRemote:
			if result.Verdict == "allowed" {
				result.Verdict = "unknown"
			}
			problems = append(problems, fmt.Sprintf("namespace %s's Package could not be read (cluster discovery is off)", path.remoteNamespace))
		default:
			theirs := kubeexplain.EvaluateUDS(sides.remote[path.remoteNamespace], path.remoteNamespace, *path.remote)
			addFindings(theirs.Findings)
			if theirs.Governed && !theirs.Covered {
				result.Verdict = "blocked"
				problems = append(problems, fmt.Sprintf("Package %s/%s does not allow it", path.remoteNamespace, theirs.Package))
				result.Fixes = append(result.Fixes, &gofiv1.SuggestedFix{Engine: "uds", Title: fmt.Sprintf("Add to Package %s/%s (owned by that application)", path.remoteNamespace, theirs.Package), Yaml: kubeexplain.UDSAllowFix(*path.remote), Note: notePeer})
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
	diagnose(result)
	return result
}

// failureText describes an observed error class for a summary.
var failureText = map[string]string{
	"denied":      "Istio denied the request (RBAC: access denied)",
	"forbidden":   "the server answered 403 Forbidden",
	"reset":       "connections are closed or reset, which is how Istio ambient denies TCP",
	"timeout":     "connections time out, which is how a NetworkPolicy drop looks",
	"refused":     "connections are refused (nothing listening, or the port is closed)",
	"dns":         "the name does not resolve",
	"unreachable": "the network is unreachable",
	"tls":         "the TLS handshake fails",
	"http":        "the server answers with an HTTP error",
}

// diagnose joins the static verdict with what the running flow observed, so
// a path that is failing says whether a Package is the reason.
func diagnose(result *gofiv1.ConnectivityPath) {
	observed := result.Observed
	if observed == nil {
		return
	}
	how := failureText[observed.ErrorClass]
	if how == "" {
		how = "sends fail"
	}
	switch {
	case observed.Failing && result.Verdict == "blocked":
		result.Diagnosis = "not-allowed"
		result.Summary = "Not allowed: " + strings.ToLower(result.Summary[:1]) + result.Summary[1:] + " Sends fail: " + how + "."
	case observed.Failing && result.Verdict == "unknown":
		result.Diagnosis = "unverified"
		result.Summary += " Sends fail (" + how + "), and the unread Package may be why."
	case observed.Failing:
		result.Diagnosis = "unreachable"
		result.Summary = "Unreachable, but not because of UDS Packages: " + how + "."
	case result.Verdict == "blocked" && observed.Successes > 0:
		result.Diagnosis = "mismatch"
		result.Summary += " Yet sends succeed, so enforcement differs from the Package (or the Package is not Ready)."
	}
}

// connectivityPaths lists the traffic a flow needs outside its namespace.
func (k *Kube) connectivityPaths(ctx context.Context, def spec.FlowDefinition) []connectivityPath {
	flowLabels := map[string]string{kubeLabelManaged: "true", kubeLabelListener: def.Name}
	var paths []connectivityPath
	if listen := def.Source.Listen; listen != nil && listen.Service != nil {
		protocol := string(transportOf(listen.Network)) // udp and quic: UDP
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
		add(observability.RouteSink(i), fmt.Sprintf("destination %d (%s)", i+1, sink.Type), sink.URL, protocol, sink.Kubernetes)
	}
	if dl := def.DeadLetterSink; dl != nil {
		protocol := "TCP"
		if dl.Type == "udp" {
			protocol = "UDP"
		}
		add(observability.RouteDeadLetter, "dead letter ("+dl.Type+")", dl.URL, protocol, dl.Kubernetes)
	}
	if source := sourceURL(def.Source); source != "" {
		// A pulled source connects out; the data then flows in on that connection.
		add(observability.RouteSource, "source", source, "TCP", nil)
	}
	sort.SliceStable(paths, func(i, j int) bool { return paths[i].id < paths[j].id })
	return paths
}

func sourceURL(source spec.FlowSource) string {
	switch {
	case source.Fetch != nil && source.Fetch.HTTP != nil:
		return source.Fetch.HTTP.URL
	case source.Fetch != nil && source.Fetch.S3 != nil:
		return source.Fetch.S3.EndpointURL()
	case source.Fetch != nil && source.Fetch.SQL != nil:
		return source.Fetch.SQL.EndpointURL()
	case source.Subscribe != nil && source.Subscribe.NATS != nil:
		return source.Subscribe.NATS.URL
	case source.Subscribe != nil && source.Subscribe.MQTT != nil:
		return source.Subscribe.MQTT.URL
	case source.Subscribe != nil && source.Subscribe.WebSocket != nil:
		return source.Subscribe.WebSocket.URL
	case source.Subscribe != nil && source.Subscribe.SSE != nil:
		return source.Subscribe.SSE.URL
	case source.Subscribe != nil && source.Subscribe.Zenoh != nil:
		return source.Subscribe.Zenoh.URL
	case len(source.Subscribe.Endpoints()) > 0: // kafka, amqp, jetstream, redis: first broker
		return source.Subscribe.Endpoints()[0]
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
