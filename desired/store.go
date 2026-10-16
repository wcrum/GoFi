// Package desired persists the desired configuration independently of runtime
// containers, allowing restarts and declarative updates to converge safely.
package desired

import (
	"context"

	"github.com/wcrum/gofi/spec"
)

const APIVersion = "gofi.wcrum.dev/v1alpha1"

type Configuration struct {
	Source     string `json:"source"`
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Path       string `json:"path,omitempty"`
}

// KindFlow is the only desired-state resource kind. The pre-rename Listener
// kind is rejected with an explicit error (docs/flows.md).
const KindFlow = "Flow"

// errListenerKind explains a pre-rename Listener resource.
const errListenerKind = "kind Listener is no longer supported; declare it as kind: Flow with source.listen (docs/flows.md)"

type Store interface {
	Load(context.Context) ([]spec.FlowDefinition, error)
	Save(context.Context, spec.FlowDefinition) error
	Delete(context.Context, string) error
	Configuration() Configuration
}
