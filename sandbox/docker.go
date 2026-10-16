package sandbox

import (
	"connectrpc.com/connect"
	"context"
	"encoding/base64"
	"fmt"
	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"net"
	"net/http"
	"os"
	"time"
)

type DockerConfig struct {
	Image, Network string
	InContainer    bool
}
type Docker struct {
	cli *client.Client
	cfg DockerConfig
}

func NewDocker(cfg DockerConfig) (*Docker, error) {
	if cfg.Image == "" {
		cfg.Image = DefaultImage
	}
	if cfg.Network == "" {
		cfg.Network = "gofi-sandbox"
	}
	c, e := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if e != nil {
		return nil, e
	}
	return &Docker{cli: c, cfg: cfg}, nil
}
func (d *Docker) ensureNetwork(ctx context.Context) error {
	n, e := d.cli.NetworkInspect(ctx, d.cfg.Network, network.InspectOptions{})
	if errdefs.IsNotFound(e) {
		_, e = d.cli.NetworkCreate(ctx, d.cfg.Network, network.CreateOptions{Driver: "bridge", Internal: true, Labels: map[string]string{"gofi.managed-by": "gofi", "gofi.role": "sandbox-network"}})
	} else if e == nil && !n.Internal {
		return fmt.Errorf("sandbox network %s must be internal", d.cfg.Network)
	}
	if e != nil {
		return e
	}
	if d.cfg.InContainer {
		self := os.Getenv("HOSTNAME")
		if self == "" {
			return fmt.Errorf("HOSTNAME required for sandbox controller network attachment")
		}
		info, e := d.cli.ContainerInspect(ctx, self)
		if e != nil {
			return e
		}
		if _, ok := info.NetworkSettings.Networks[d.cfg.Network]; !ok {
			return d.cli.NetworkConnect(ctx, d.cfg.Network, self, &network.EndpointSettings{})
		}
	}
	return nil
}
func (d *Docker) Start(ctx context.Context, def Definition, token string) (string, error) {
	if e := d.ensureNetwork(ctx); e != nil {
		return "", e
	}
	if e := d.Stop(ctx, def.Name); e != nil {
		return "", e
	}
	name := "gofi-python-" + def.Name
	port := nat.Port("8080/tcp")
	pids := int64(128)
	init := true
	memory := int64(def.MemoryMiB) * 1024 * 1024
	hc := &container.HostConfig{NetworkMode: container.NetworkMode(d.cfg.Network), ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, Init: &init, Tmpfs: map[string]string{"/tmp": "rw,noexec,nosuid,size=16777216,mode=1777"}, Resources: container.Resources{Memory: memory, MemorySwap: memory, NanoCPUs: 250000000, PidsLimit: &pids}}

	created, e := d.cli.ContainerCreate(ctx, &container.Config{Image: d.cfg.Image, User: "65532:65532", Env: []string{"GOFI_PYTHON_SOURCE=" + base64.StdEncoding.EncodeToString([]byte(def.SourceCode)), "GOFI_SANDBOX_TOKEN=" + token, fmt.Sprintf("GOFI_TIMEOUT_SECONDS=%d", def.TimeoutSeconds), "PYTHONDONTWRITEBYTECODE=1"}, ExposedPorts: nat.PortSet{port: struct{}{}}, Labels: map[string]string{"gofi.managed-by": "gofi", "gofi.sandbox": def.Name}}, hc, &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{d.cfg.Network: {Aliases: []string{name}}}}, nil, name)
	if e != nil {
		return "", e
	}
	if e = d.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); e != nil {
		return "", e
	}
	endpoint := "http://" + name + ":8080"
	if !d.cfg.InContainer {
		endpoint, e = d.startRelay(ctx, def.Name, name)
		if e != nil {
			return "", e
		}
	}

	if e = waitReady(ctx, endpoint, token); e != nil {
		return "", fmt.Errorf("worker %s not ready: %w", def.Name, e)
	}
	return endpoint, nil
}
func (d *Docker) startRelay(ctx context.Context, library, worker string) (string, error) {
	port := nat.Port("8080/tcp")
	pids, init := int64(32), true
	created, err := d.cli.ContainerCreate(ctx, &container.Config{
		Image: d.cfg.Image, User: "65532:65532", Entrypoint: []string{"python", "-m", "gofi.proxy"},
		Env:          []string{"GOFI_WORKER_TARGET=" + worker + ":8080", "PYTHONDONTWRITEBYTECODE=1"},
		ExposedPorts: nat.PortSet{port: struct{}{}},
		Labels:       map[string]string{"gofi.managed-by": "gofi", "gofi.sandbox": library, "gofi.role": "sandbox-control"},
	}, &container.HostConfig{
		NetworkMode: "bridge", PortBindings: nat.PortMap{port: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}}},
		ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, Init: &init,
		Sysctls:   map[string]string{"net.ipv4.ip_forward": "0"},
		Tmpfs:     map[string]string{"/tmp": "rw,noexec,nosuid,size=16777216,mode=1777"},
		Resources: container.Resources{Memory: 64 * 1024 * 1024, MemorySwap: 64 * 1024 * 1024, NanoCPUs: 100000000, PidsLimit: &pids},
	}, nil, nil, worker+"-control")
	if err != nil {
		return "", err
	}
	if err = d.cli.NetworkConnect(ctx, d.cfg.Network, created.ID, &network.EndpointSettings{}); err != nil {
		return "", err
	}
	if err = d.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", err
	}
	info, err := d.cli.ContainerInspect(ctx, created.ID)
	if err != nil {
		return "", err
	}
	bindings := info.NetworkSettings.Ports[port]
	if len(bindings) == 0 {
		return "", fmt.Errorf("worker control relay loopback binding unavailable")
	}
	return "http://" + net.JoinHostPort("127.0.0.1", bindings[0].HostPort), nil
}
func (d *Docker) Stop(ctx context.Context, name string) error {
	for _, containerName := range []string{"gofi-python-" + name + "-control", "gofi-python-" + name} {
		info, e := d.cli.ContainerInspect(ctx, containerName)
		if errdefs.IsNotFound(e) {
			continue
		}
		if e != nil {
			return e
		}
		if info.Config.Labels["gofi.sandbox"] != name || info.Config.Labels["gofi.managed-by"] != "gofi" {
			return fmt.Errorf("refusing to replace unmanaged worker container")
		}
		if e = d.cli.ContainerRemove(ctx, info.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); e != nil {
			return e
		}
	}
	return nil
}
func waitReady(ctx context.Context, endpoint, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	httpClient := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer httpClient.CloseIdleConnections()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		rpc := gofiv1connect.NewWorkerServiceClient(httpClient, endpoint)
		req := connect.NewRequest(&gofiv1.WorkerListFunctionsRequest{})
		req.Header().Set("Authorization", "Bearer "+token)
		if _, e := rpc.ListFunctions(ctx, req); e == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func checkRPC(ctx context.Context, b Binding) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	hc := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer hc.CloseIdleConnections()
	req := connect.NewRequest(&gofiv1.WorkerListFunctionsRequest{})
	req.Header().Set("Authorization", "Bearer "+b.Token)
	_, err := gofiv1connect.NewWorkerServiceClient(hc, b.Endpoint).ListFunctions(ctx, req)
	return err
}
func (d *Docker) Health(ctx context.Context, b Binding) error { return checkRPC(ctx, b) }
