// Package driver: Docker driver.
//
// Docker spawns each listener as a sibling container running the same
// image as the controller, in "listener mode" (FLOW_SPEC env var — see
// main.go's runListenerMode). It talks to the container runtime over the
// Docker Engine API (github.com/docker/docker/client), which any
// Docker-Engine-API-compatible runtime can serve — this has been written to
// work against both real Docker and Podman's Docker-API compatibility layer
// (e.g. `podman system service`, pointed to via DOCKER_HOST). containerd is
// explicitly out of scope: it does not speak the Docker Engine API at all
// and would need its own driver built on github.com/containerd/containerd's
// client SDK.
//
// Each listener's data port (def.Source.Listening().Port/Network) is published 1:1 to
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
	"net"
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

	"github.com/wcrum/gofi/functionrpc"
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
// spec via the FLOW_SPEC environment variable. Requires access to a
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
// The listener's data port (def.Source.Listening().Port/Network) is published to the
// host on the same port number (0.0.0.0:<port> -> container:<port>), so it's
// reachable from the host machine and anything outside Docker — not just
// from other containers on the same Docker network (which would already be
// reachable unpublished, via Docker's bridge networking). Because the
// host-side port is fixed to match the container's, two listeners sharing
// the same port WILL conflict at ContainerCreate/ContainerStart time; ports
// must be unique across listeners on a given host, same as InProcess mode.
// Fetch and subscribe flows only connect outward and publish no port.
func (d *Docker) Create(ctx context.Context, def spec.FlowDefinition) error {
	raw, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("docker driver: marshal spec for %q: %w", def.Name, err)
	}

	exposed, bindings, err := dockerDataPort(def)
	if err != nil {
		return err
	}

	name := containerName(def.Name)
	cfg := &container.Config{
		Image:        d.image,
		Env:          []string{spec.FlowSpecEnv + "=" + string(raw)},
		ExposedPorts: exposed,
		Labels: map[string]string{
			labelListener: def.Name,
			labelManaged:  "true",
		},
	}
	if def.UsesPython() {
		bindings, err := functionrpc.Environment(ctx, def)
		if err != nil {
			return err
		}
		cfg.Env = append(cfg.Env, "GOFI_PYTHON_BINDINGS="+bindings)
	}
	hostCfg := &container.HostConfig{
		NetworkMode:  container.NetworkMode(d.network),
		PortBindings: bindings,
		RestartPolicy: container.RestartPolicy{
			// GoFi's own periodic resync (controller.Controller.Run), not
			// Docker, is the recreate path on crash — this keeps crashes
			// visible instead of silently hidden by container-runtime restarts.
			Name: container.RestartPolicyDisabled,
		},
	}

	var networks *network.NetworkingConfig
	if def.UsesPython() {
		sandboxNetwork := os.Getenv("GOFI_SANDBOX_NETWORK")
		if sandboxNetwork == "" {
			sandboxNetwork = "gofi-sandbox"
		}
		networks = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{d.network: {}, sandboxNetwork: {}}}
	}
	_, err = d.cli.ContainerCreate(ctx, cfg, hostCfg, networks, nil, name)
	if err != nil && cerrdefs.IsConflict(err) {
		existing, inspectErr := d.cli.ContainerInspect(ctx, name)
		if inspectErr != nil && !cerrdefs.IsNotFound(inspectErr) {
			return fmt.Errorf("docker driver: inspect conflicting container %q: %w", name, inspectErr)
		}
		if inspectErr == nil {
			if !d.owns(existing, def.Name) {
				return fmt.Errorf("docker driver: container name %q is already used by an unmanaged container or another deployment; refusing to remove it", name)
			}
			slog.Warn("docker driver: removing owned stale container and retrying", "name", name)
			if rerr := d.cli.ContainerRemove(ctx, existing.ID, container.RemoveOptions{Force: true}); rerr != nil && !cerrdefs.IsNotFound(rerr) {
				return fmt.Errorf("docker driver: remove stale container %q: %w", name, rerr)
			}
		}
		_, err = d.cli.ContainerCreate(ctx, cfg, hostCfg, networks, nil, name)
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
	info, err := d.cli.ContainerInspect(ctx, cn)
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("docker driver: inspect before delete %q: %w", name, err)
	}
	if !d.owns(info, name) {
		return fmt.Errorf("docker driver: refusing to delete container %q outside this deployment", cn)
	}
	// Act on the inspected ID so a concurrent rename/replacement cannot turn
	// this request into deletion of an unrelated container with the same name.
	cn = info.ID
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
// decoded back from each container's FLOW_SPEC env var.
func (d *Docker) List(ctx context.Context) ([]spec.FlowDefinition, error) {
	f := filters.NewArgs()
	f.Add("label", labelManaged+"=true")
	f.Add("network", d.network)

	summaries, err := d.cli.ContainerList(ctx, container.ListOptions{All: false, Filters: f})
	if err != nil {
		return nil, fmt.Errorf("docker driver: list: %w", err)
	}

	out := make([]spec.FlowDefinition, 0, len(summaries))
	for _, s := range summaries {
		info, err := d.cli.ContainerInspect(ctx, s.ID)
		if err != nil {
			slog.Warn("docker driver: inspect during list failed", "container", s.ID, "error", err)
			continue
		}
		def, ok := decodeListenerSpec(info)
		if !ok {
			slog.Warn("docker driver: managed container missing/invalid flow spec, skipping", "container", s.ID)
			continue
		}
		if !d.owns(info, def.Name) {
			continue
		}
		out = append(out, def)
	}
	return out, nil
}

// decodeListenerSpec extracts and decodes the flow spec env var from a
// container's inspect result.
func decodeListenerSpec(info container.InspectResponse) (spec.FlowDefinition, bool) {
	if info.Config == nil {
		return spec.FlowDefinition{}, false
	}
	for _, e := range info.Config.Env {
		v, ok := strings.CutPrefix(e, spec.FlowSpecEnv+"=")
		if !ok {
			continue
		}
		var def spec.FlowDefinition
		if err := json.Unmarshal([]byte(v), &def); err != nil {
			return spec.FlowDefinition{}, false
		}
		return def, true
	}
	return spec.FlowDefinition{}, false
}

// owns verifies both the managed identity and the selected deployment network.
func (d *Docker) owns(info container.InspectResponse, name string) bool {
	if info.ContainerJSONBase == nil || info.ID == "" || info.Config == nil || info.Config.Labels[labelManaged] != "true" || info.Config.Labels[labelListener] != name || info.NetworkSettings == nil {
		return false
	}
	_, attached := info.NetworkSettings.Networks[d.network]
	return attached
}

// Tap resolves only a listener owned by this deployment's selected network.
func (d *Docker) Tap(ctx context.Context, name string) (TapTarget, bool, error) {
	info, err := d.cli.ContainerInspect(ctx, containerName(name))
	if cerrdefs.IsNotFound(err) {
		return TapTarget{}, false, nil
	}
	if err != nil {
		return TapTarget{}, false, fmt.Errorf("docker driver: inspect tap %q: %w", name, err)
	}
	if !d.owns(info, name) {
		return TapTarget{}, false, nil
	}
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

	if !d.owns(info, name) {
		return RuntimeInfo{}, false, nil
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

// Endpoints lists running containers attached to the controller's network.
// Only exposed or published ports are advertised; DNS names remain usable
// directly for containers whose image does not document EXPOSE ports.
func (d *Docker) Endpoints(ctx context.Context) ([]Endpoint, error) {
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{Filters: filters.NewArgs(filters.Arg("network", d.network))})
	if err != nil {
		return nil, fmt.Errorf("docker driver: discover endpoints: %w", err)
	}
	out := make([]Endpoint, 0, len(containers))
	for _, c := range containers {
		name := c.ID
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		e := Endpoint{Name: name, Kind: "docker", Type: "container", Host: name, Ports: []EndpointPort{}}
		for _, port := range c.Ports {
			e.Ports = append(e.Ports, EndpointPort{Port: int32(port.PrivatePort), Protocol: strings.ToUpper(port.Type), NodePort: int32(port.PublicPort), URL: port.Type + "://" + net.JoinHostPort(name, strconv.Itoa(int(port.PrivatePort)))})
			if port.PublicPort != 0 && port.IP != "" {
				e.ExternalAddresses = append(e.ExternalAddresses, port.IP)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// dockerDataPort returns the exposed port and host binding for a listen
// flow's data port; other sources publish nothing.
func dockerDataPort(def spec.FlowDefinition) (nat.PortSet, nat.PortMap, error) {
	if def.Source.Listen == nil {
		return nil, nil, nil
	}
	proto := "tcp"
	if strings.EqualFold(def.Source.Listening().Network, "udp") {
		proto = "udp"
	}
	port, err := nat.NewPort(proto, strconv.Itoa(def.Source.Listening().Port))
	if err != nil {
		return nil, nil, fmt.Errorf("docker driver: invalid port %d/%s for %q: %w", def.Source.Listening().Port, proto, def.Name, err)
	}
	return nat.PortSet{port: struct{}{}}, nat.PortMap{port: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: strconv.Itoa(def.Source.Listening().Port)}}}, nil
}
