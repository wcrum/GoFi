// Package driver: Docker driver.
//
// Docker spawns each listener as a sibling container running the same
// image as the controller, in "listener mode" (LISTENER_SPEC env var — see
// main.go's runListenerMode). It talks to the container runtime over the
// Docker Engine API (github.com/docker/docker/client), which any
// Docker-Engine-API-compatible runtime can serve — this has been written to
// work against both real Docker and Podman's Docker-API compatibility layer
// (e.g. `podman system service`, pointed to via DOCKER_HOST). containerd is
// explicitly out of scope: it does not speak the Docker Engine API at all
// and would need its own driver built on github.com/containerd/containerd's
// client SDK.
//
// Each listener's data port (def.Source.Port/Network) is published 1:1 to
// the host (same port number on both sides) so it's reachable from outside
// Docker entirely, not just from other containers on the same network. This
// means two listeners configured with the same port WILL conflict at the
// Docker host-port-binding level — ports must be unique across listeners on
// a given host, the same constraint InProcess mode already has (it also
// binds one real OS port per listener).
package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/wcrum/gofi/spec"
)

// Labels applied to every container the Docker driver creates, used to
// distinguish gofi-managed listener containers from anything else running
// on the same daemon.
const (
	labelListener = "gofi.listener"
	labelManaged  = "gofi.managed-by"

	// tapRelayPort is the port the tap relay server (see taprelay package)
	// listens on inside each listener container. Must match TAP_RELAY_ADDR's
	// default in main.go.
	tapRelayPort = "9091"
)

// dockerAPI is the narrow slice of the Docker Engine API client that the
// Docker driver actually uses. Defined as an interface (rather than using
// *client.Client directly) so unit tests can back it with a hand-written
// fake instead of a real daemon.
type dockerAPI interface {
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
}

// DockerConfig configures the Docker driver. Image and Network are both
// optional: if left empty, NewDocker self-inspects the controller's own
// container to derive them (see selfInspect), so operators don't have to
// hand-type values that would otherwise drift from the actual deployed
// image/network.
type DockerConfig struct {
	Image   string
	Network string
}

// Docker spawns each listener as a container of the same image, passing the
// spec via the LISTENER_SPEC environment variable. Requires access to a
// Docker-Engine-API-compatible socket (Docker or Podman), reachable the same
// way the standard Docker CLI/SDK resolve it: via DOCKER_HOST (or the
// per-platform default if unset) — see client.FromEnv. No socket path is
// ever hardcoded here, which is what makes Podman's differently-located
// socket (e.g. /run/user/<uid>/podman/podman.sock) work without any special
// casing, as long as DOCKER_HOST points at it.
type Docker struct {
	cli     dockerAPI
	image   string
	network string
}

// NewDocker builds a Docker driver. It connects via client.FromEnv (so
// DOCKER_HOST — not any hardcoded socket path — controls which daemon we
// talk to) and negotiates the API version so it works against whatever
// Engine-API-compatible version the remote (Docker or Podman) speaks.
func NewDocker(cfg DockerConfig) (*Docker, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker driver: client: %w", err)
	}
	return newDockerWithClient(cli, cfg)
}

// newDockerWithClient builds a Docker driver around an already-constructed
// dockerAPI, used directly by NewDocker and by tests (with a fake).
func newDockerWithClient(cli dockerAPI, cfg DockerConfig) (*Docker, error) {
	image, network := cfg.Image, cfg.Network
	if image == "" || network == "" {
		selfImage, selfNetwork, serr := selfInspect(context.Background(), cli)
		if image == "" {
			if serr != nil {
				return nil, fmt.Errorf("docker driver: GOFI_IMAGE not set and self-inspect failed: %w", serr)
			}
			image = selfImage
		}
		if network == "" {
			if serr != nil {
				return nil, fmt.Errorf("docker driver: GOFI_DOCKER_NETWORK not set and self-inspect failed: %w", serr)
			}
			network = selfNetwork
		}
	}
	return &Docker{cli: cli, image: image, network: network}, nil
}

// selfInspect reads the running container's own hostname (which Docker/Podman
// set to the container's own short ID by default, exposed as $HOSTNAME) and
// inspects itself to discover its own image reference and the name of the
// first network it's attached to. The image string is treated as fully
// opaque — it's passed straight through to the next ContainerCreate call, so
// it doesn't matter whether it's a Docker Hub reference, a fully-qualified
// registry reference, or any other format a Docker-API-compatible runtime
// (including Podman) chooses to report.
func selfInspect(ctx context.Context, cli dockerAPI) (image, netName string, err error) {
	hostname := os.Getenv("HOSTNAME")
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	if hostname == "" {
		return "", "", fmt.Errorf("self-inspect: could not determine own hostname")
	}

	info, err := cli.ContainerInspect(ctx, hostname)
	if err != nil {
		return "", "", fmt.Errorf("self-inspect: inspect %q: %w", hostname, err)
	}
	if info.Config != nil {
		image = info.Config.Image
	}
	if info.NetworkSettings != nil && len(info.NetworkSettings.Networks) > 0 {
		if len(info.NetworkSettings.Networks) > 1 {
			slog.Warn("docker driver: self-inspect found multiple attached networks; set GOFI_DOCKER_NETWORK explicitly to disambiguate", "count", len(info.NetworkSettings.Networks))
		}
		for name := range info.NetworkSettings.Networks {
			netName = name
			break
		}
	}
	if image == "" || netName == "" {
		return image, netName, fmt.Errorf("self-inspect: incomplete result (image=%q network=%q)", image, netName)
	}
	return image, netName, nil
}

// containerName derives a stable, valid container name for a listener.
func containerName(name string) string {
	return "gofi-listener-" + sanitizeName(name)
}

// Create starts a new sibling container running the given listener
// definition. Name conflicts (a stale container left over from an unnoticed
// crash) are handled by force-removing the old container and retrying once.
//
// The listener's data port (def.Source.Port/Network) is published to the
// host on the same port number (0.0.0.0:<port> -> container:<port>), so it's
// reachable from the host machine and anything outside Docker — not just
// from other containers on the same Docker network (which would already be
// reachable unpublished, via Docker's bridge networking). Because the
// host-side port is fixed to match the container's, two listeners sharing
// the same port WILL conflict at ContainerCreate/ContainerStart time; ports
// must be unique across listeners on a given host, same as InProcess mode.
func (d *Docker) Create(ctx context.Context, def spec.ListenerDefinition) error {
	raw, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("docker driver: marshal spec for %q: %w", def.Name, err)
	}

	proto := "tcp"
	if strings.EqualFold(def.Source.Network, "udp") {
		proto = "udp"
	}
	port, err := nat.NewPort(proto, strconv.Itoa(def.Source.Port))
	if err != nil {
		return fmt.Errorf("docker driver: invalid port %d/%s for %q: %w", def.Source.Port, proto, def.Name, err)
	}

	name := containerName(def.Name)
	cfg := &container.Config{
		Image:        d.image,
		Env:          []string{"LISTENER_SPEC=" + string(raw)},
		ExposedPorts: nat.PortSet{port: struct{}{}},
		Labels: map[string]string{
			labelListener: def.Name,
			labelManaged:  "true",
		},
	}
	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode(d.network),
		PortBindings: nat.PortMap{
			port: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: strconv.Itoa(def.Source.Port)}},
		},
		RestartPolicy: container.RestartPolicy{
			// GoFi's own periodic resync (controller.Controller.Run), not
			// Docker, is the recreate path on crash — this keeps crashes
			// visible instead of silently hidden by container-runtime restarts.
			Name: container.RestartPolicyDisabled,
		},
	}

	_, err = d.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
	if err != nil && cerrdefs.IsConflict(err) {
		slog.Warn("docker driver: stale container with conflicting name, removing and retrying", "name", name)
		if rerr := d.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); rerr != nil && !cerrdefs.IsNotFound(rerr) {
			return fmt.Errorf("docker driver: remove stale container %q: %w", name, rerr)
		}
		_, err = d.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
	}
	if err != nil {
		return fmt.Errorf("docker driver: create %q: %w", def.Name, err)
	}

	if err := d.cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		return fmt.Errorf("docker driver: start %q: %w", def.Name, err)
	}
	return nil
}

// Delete blocks until the listener's container is stopped and removed.
// It must block (unlike InProcess.Delete's fire-and-forget cancel): the
// controller's update path calls Delete synchronously right before Create'ing
// the replacement, and a non-blocking Delete would race that Create against
// a container name conflict.
func (d *Docker) Delete(ctx context.Context, name string) error {
	cn := containerName(name)

	timeoutSec := 5
	if err := d.cli.ContainerStop(ctx, cn, container.StopOptions{Timeout: &timeoutSec}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker driver: stop %q: %w", name, err)
	}

	if err := d.cli.ContainerRemove(ctx, cn, container.RemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker driver: remove %q: %w", name, err)
	}
	return nil
}

// List returns the definitions of all running gofi-managed containers,
// decoded back from each container's LISTENER_SPEC env var.
func (d *Docker) List(ctx context.Context) ([]spec.ListenerDefinition, error) {
	f := filters.NewArgs()
	f.Add("label", labelManaged+"=true")

	summaries, err := d.cli.ContainerList(ctx, container.ListOptions{All: false, Filters: f})
	if err != nil {
		return nil, fmt.Errorf("docker driver: list: %w", err)
	}

	out := make([]spec.ListenerDefinition, 0, len(summaries))
	for _, s := range summaries {
		info, err := d.cli.ContainerInspect(ctx, s.ID)
		if err != nil {
			slog.Warn("docker driver: inspect during list failed", "container", s.ID, "error", err)
			continue
		}
		def, ok := decodeListenerSpec(info)
		if !ok {
			slog.Warn("docker driver: managed container missing/invalid LISTENER_SPEC, skipping", "container", s.ID)
			continue
		}
		out = append(out, def)
	}
	return out, nil
}

// decodeListenerSpec extracts and decodes the LISTENER_SPEC env var from a
// container's inspect result.
func decodeListenerSpec(info container.InspectResponse) (spec.ListenerDefinition, bool) {
	if info.Config == nil {
		return spec.ListenerDefinition{}, false
	}
	for _, e := range info.Config.Env {
		v, ok := strings.CutPrefix(e, "LISTENER_SPEC=")
		if !ok {
			continue
		}
		var def spec.ListenerDefinition
		if err := json.Unmarshal([]byte(v), &def); err != nil {
			return spec.ListenerDefinition{}, false
		}
		return def, true
	}
	return spec.ListenerDefinition{}, false
}

// Tap returns the address of the listener's tap relay server. This is pure
// string formatting (no I/O): Compose's per-project user-defined network
// already gives embedded DNS-by-container-name, so the container's own name
// is a routable hostname for any other container on the same network.
func (d *Docker) Tap(_ context.Context, name string) (TapTarget, bool, error) {
	return TapTarget{Addr: containerName(name) + ":" + tapRelayPort}, true, nil
}

// RuntimeStatus inspects the listener's container and reports its Docker
// state ("running", "exited", "created", etc. — Docker's own status string,
// passed straight through), full container ID, and start time. ok is false
// if the container doesn't exist (not yet created, or already removed).
func (d *Docker) RuntimeStatus(ctx context.Context, name string) (RuntimeInfo, bool, error) {
	info, err := d.cli.ContainerInspect(ctx, containerName(name))
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return RuntimeInfo{}, false, nil
		}
		return RuntimeInfo{}, false, fmt.Errorf("docker driver: inspect %q: %w", name, err)
	}

	out := RuntimeInfo{Kind: "docker"}
	if info.ContainerJSONBase != nil {
		out.ID = info.ID
		if info.State != nil {
			out.Status = info.State.Status
			if info.State.StartedAt != "" {
				if t, perr := time.Parse(time.RFC3339Nano, info.State.StartedAt); perr == nil {
					out.StartedAt = t
				}
			}
		}
	}
	return out, true, nil
}

// Kind identifies this driver implementation.
func (d *Docker) Kind() string { return "docker" }
