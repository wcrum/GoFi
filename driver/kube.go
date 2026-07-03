package driver

import (
	"context"
	"fmt"

	"github.com/cnak-us/gogo/listener"
	"github.com/cnak-us/gogo/spec"
)

// Kube creates a Deployment + headless Service per ListenerDefinition using
// client-go. The spec is injected via a ConfigMap mounted as LISTENER_SPEC.
//
// This driver is a stub — the interface is satisfied but Create/Delete are not
// yet implemented. Import "k8s.io/client-go" when ready.
type Kube struct {
	namespace string
	image     string
}

func NewKube(namespace, image string) *Kube {
	return &Kube{namespace: namespace, image: image}
}

func (k *Kube) Create(_ context.Context, def spec.ListenerDefinition) error {
	return fmt.Errorf("kube driver: not yet implemented (listener: %s)", def.Name)
}

func (k *Kube) Delete(_ context.Context, name string) error {
	return fmt.Errorf("kube driver: not yet implemented (listener: %s)", name)
}

func (k *Kube) List(_ context.Context) ([]spec.ListenerDefinition, error) {
	return nil, fmt.Errorf("kube driver: not yet implemented")
}

func (k *Kube) Tap(_ string) *listener.Listener { return nil }
