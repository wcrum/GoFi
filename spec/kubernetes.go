package spec

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// KubernetesTarget routes TCP/UDP to the ready Pods matching labels inside the
// controller's namespace. Kubernetes maintains membership through a Service.
type KubernetesTarget struct {
	Namespace string            `json:"namespace,omitempty"`
	Selector  map[string]string `json:"selector"`
	Port      int               `json:"port"`
}

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
