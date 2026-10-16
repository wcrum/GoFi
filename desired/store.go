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

type Store interface {
	Load(context.Context) ([]spec.ListenerDefinition, error)
	Save(context.Context, spec.ListenerDefinition) error
	Delete(context.Context, string) error
	Configuration() Configuration
}
