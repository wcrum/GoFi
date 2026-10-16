package spec

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// KubernetesTarget routes TCP/UDP to the ready Pods matching labels, in the
// controller's namespace or (with cluster discovery) another namespace.
// GoFi keeps a Service in its own namespace for the target: a selector
// Service for local Pods, or one backed by a GoFi-managed EndpointSlice for
// Pods elsewhere.
type KubernetesTarget struct {
	Namespace string            `json:"namespace,omitempty"`
	Selector  map[string]string `json:"selector"`
	Port      int               `json:"port"`
	// Delivery is "all" (default: every ready Pod receives each message,
	// through a headless Service) or "one" (load-balanced ClusterIP Service).
	Delivery string `json:"delivery,omitempty"`
}

// FanOut reports whether each message goes to every matching Pod.
func (t *KubernetesTarget) FanOut() bool { return t.Delivery != "one" }

func (t *KubernetesTarget) Validate() error {
	if t == nil || len(t.Selector) == 0 {
		return fmt.Errorf("kubernetes.selector requires at least one label")
	}
	if len(t.Selector) > 64 {
		return fmt.Errorf("kubernetes.selector supports at most 64 labels")
	}
	if t.Port < 1 || t.Port > 65535 {
		return fmt.Errorf("kubernetes.port must be between 1 and 65535")
	}
	if t.Delivery != "" && t.Delivery != "all" && t.Delivery != "one" {
		return fmt.Errorf("kubernetes.delivery must be all or one")
	}
	if t.Namespace != "" && len(validation.IsDNS1123Label(t.Namespace)) != 0 {
		return fmt.Errorf("kubernetes.namespace must be a valid namespace")
	}
	for key, value := range t.Selector {
		if len(validation.IsQualifiedName(key)) != 0 || len(validation.IsValidLabelValue(value)) != 0 {
			return fmt.Errorf("kubernetes.selector contains an invalid label")
		}
	}
	return nil
}

// KubernetesServiceName is shared by the controller's declarative service and
// the isolated listener. It never depends on transient Pod IP addresses.
func KubernetesServiceName(listenerName, network string, target *KubernetesTarget) string {
	data, _ := json.Marshal([]any{listenerName, strings.ToLower(network), target})
	sum := sha256.Sum256(data)
	return fmt.Sprintf("gofi-target-%x", sum[:10])
}
