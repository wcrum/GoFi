package kubeexplain

import (
	"fmt"
	"net"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/yaml"
)

// UDSPackageResult says whether a UDS Package governs the namespace and
// whether one of its network.allow entries intends to cover a path. UDS
// enforces through the NetworkPolicies and AuthorizationPolicies its
// operator generates; those objects are evaluated separately. This layer
// explains intent and produces the Package-level fix.
type UDSPackageResult struct {
	Governed bool
	Package  string
	Covered  bool
	Findings []Finding
}

// UDSPath describes one path in the vocabulary of a UDS allow entry.
type UDSPath struct {
	Direction Direction
	// Namespace of the GoFi workload (the Package's namespace).
	Namespace string
	// Labels of the GoFi workload the entry selects.
	Selector map[string]string
	// Remote side: a workload, or "Anywhere" for traffic outside the cluster.
	RemoteNamespace string
	RemoteSelector  map[string]string
	RemoteAnywhere  bool
	// RemoteHost is an outside host name or IP (UDS remoteHost / remoteCidr).
	RemoteHost string
	Port       int32
	// Protocol is "TCP" (default) or "UDP"; UDS allows are per transport.
	Protocol    string
	Description string
}

// EvaluateUDS checks Packages in the GoFi namespace for an allow entry that
// intends to cover the path.
func EvaluateUDS(packages []unstructured.Unstructured, namespace string, path UDSPath) UDSPackageResult {
	result := UDSPackageResult{}
	for _, pkg := range packages {
		if pkg.GetNamespace() != namespace {
			continue
		}
		result.Governed = true
		result.Package = pkg.GetName()
		path.Namespace = namespace
		allows, _ := sliceField(pkg.Object, "spec", "network", "allow")
		for i, raw := range allows {
			entry, _ := raw.(map[string]any)
			if covers(entry, path) {
				result.Covered = true
				result.Findings = append(result.Findings, Finding{Engine: "uds", Kind: "Package", Name: pkg.GetName(), Namespace: namespace, Effect: "allows",
					Detail: fmt.Sprintf("network.allow[%d] (%s) intends to allow this path.", i, describeEntry(entry))})
				return result
			}
		}
		result.Findings = append(result.Findings, Finding{Engine: "uds", Kind: "Package", Name: pkg.GetName(), Namespace: namespace, Effect: "isolates",
			Detail: "This namespace is managed by a UDS Package with default-deny networking, and none of its network.allow entries covers this path."})
	}
	if !result.Governed {
		result.Findings = append(result.Findings, Finding{Engine: "uds", Effect: "not-applicable", Detail: "No UDS Package manages this namespace."})
	}
	return result
}

func covers(entry map[string]any, path UDSPath) bool {
	direction, _ := entry["direction"].(string)
	if !strings.EqualFold(direction, string(path.Direction)) {
		return false
	}
	if selector, ok := entry["selector"].(map[string]any); ok && len(selector) > 0 {
		if !labels.SelectorFromSet(toStringMap(selector)).Matches(labels.Set(path.Selector)) {
			return false
		}
	}
	if !portCovered(entry, path.Port) || !protocolCovered(entry, path.Protocol) {
		return false
	}
	generated, _ := entry["remoteGenerated"].(string)
	if path.RemoteHost != "" {
		if generated == "Anywhere" {
			return true
		}
		if host, _ := entry["remoteHost"].(string); host != "" {
			return strings.EqualFold(host, path.RemoteHost)
		}
		if cidr, _ := entry["remoteCidr"].(string); cidr != "" {
			_, network, err := net.ParseCIDR(cidr)
			ip := net.ParseIP(path.RemoteHost)
			return err == nil && ip != nil && network.Contains(ip)
		}
		return false
	}
	if path.RemoteAnywhere {
		return generated == "Anywhere"
	}
	if generated == "Anywhere" {
		return true
	}
	if generated == "IntraNamespace" {
		return path.RemoteNamespace == path.Namespace
	}
	if ns, ok := entry["remoteNamespace"].(string); ok && ns != "" && ns != "*" && ns != path.RemoteNamespace {
		return false
	}
	if remote, ok := entry["remoteSelector"].(map[string]any); ok && len(remote) > 0 {
		return labels.SelectorFromSet(toStringMap(remote)).Matches(labels.Set(path.RemoteSelector))
	}
	_, hasNamespace := entry["remoteNamespace"]
	return hasNamespace
}

// protocolCovered: an entry's remoteProtocol defaults to TCP, and HTTP/TLS
// (used with remoteHost) are TCP transports; UDP must be explicit.
func protocolCovered(entry map[string]any, protocol string) bool {
	want := strings.ToUpper(protocol)
	if want == "" {
		want = "TCP"
	}
	have, _ := entry["remoteProtocol"].(string)
	switch strings.ToUpper(have) {
	case "", "TCP", "HTTP", "TLS":
		return want == "TCP"
	}
	return strings.EqualFold(have, want)
}

func portCovered(entry map[string]any, port int32) bool {
	if p, ok := entry["port"]; ok {
		return fmt.Sprint(p) == fmt.Sprint(port)
	}
	if ports, ok := entry["ports"].([]any); ok && len(ports) > 0 {
		for _, p := range ports {
			if fmt.Sprint(p) == fmt.Sprint(port) {
				return true
			}
		}
		return false
	}
	return true
}

func describeEntry(entry map[string]any) string {
	description, _ := entry["description"].(string)
	if description != "" {
		return description
	}
	direction, _ := entry["direction"].(string)
	return direction
}

// UDSAllowFix renders the network.allow entry that would admit the path.
func UDSAllowFix(path UDSPath) string {
	entry := map[string]any{
		"direction":   string(path.Direction),
		"selector":    path.Selector,
		"port":        path.Port,
		"description": path.Description,
	}
	if strings.EqualFold(path.Protocol, "UDP") {
		entry["remoteProtocol"] = "UDP"
	}
	if path.RemoteHost != "" {
		if ip := net.ParseIP(path.RemoteHost); ip != nil {
			entry["remoteCidr"] = ip.String() + "/32"
		} else {
			entry["remoteHost"] = path.RemoteHost
			entry["remoteProtocol"] = "TLS"
		}
	} else if path.RemoteAnywhere {
		entry["remoteGenerated"] = "Anywhere"
	} else {
		entry["remoteNamespace"] = path.RemoteNamespace
		if len(path.RemoteSelector) > 0 {
			entry["remoteSelector"] = path.RemoteSelector
		}
	}
	out, _ := yaml.Marshal(map[string]any{"spec": map[string]any{"network": map[string]any{"allow": []any{entry}}}})
	return string(out)
}
