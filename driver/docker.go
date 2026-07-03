package driver

import (
	"context"
	"fmt"

	"github.com/cnak-us/gogo/listener"
	"github.com/cnak-us/gogo/spec"
)

// Docker spawns each listener as a container of the same image, passing the
// spec via the LISTENER_SPEC environment variable. Requires the Docker socket
// to be mounted at /var/run/docker.sock (or DOCKER_HOST to be set).
//
// This driver is a stub — the interface is satisfied but Create/Delete are not
// yet implemented. Import "github.com/docker/docker/client" when ready.
type Docker struct {
	image string // e.g. "ghcr.io/cnak-us/cnak-ingress:latest"
}

func NewDocker(image string) *Docker {
	return &Docker{image: image}
}

func (d *Docker) Create(_ context.Context, def spec.ListenerDefinition) error {
	return fmt.Errorf("docker driver: not yet implemented (listener: %s)", def.Name)
}

func (d *Docker) Delete(_ context.Context, name string) error {
	return fmt.Errorf("docker driver: not yet implemented (listener: %s)", name)
}

func (d *Docker) List(_ context.Context) ([]spec.ListenerDefinition, error) {
	return nil, fmt.Errorf("docker driver: not yet implemented")
}

func (d *Docker) Tap(_ string) *listener.Listener { return nil }
