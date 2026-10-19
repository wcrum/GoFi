//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/wcrum/gofi/internal/testkit/gofiapi"
	"github.com/wcrum/gofi/internal/testkit/invariants"
	"github.com/wcrum/gofi/internal/testkit/proc"
	"github.com/wcrum/gofi/internal/testkit/traffic"
	"github.com/wcrum/gofi/spec"
)

// composeDeployment is the Docker shape: the controller runs in a container
// with the Docker socket and drives sibling listener containers on a private
// network, exactly as docker-compose.yml does. It uses docker directly so a
// scenario can kill and restart the controller or a listener container.
type composeDeployment struct {
	image    string
	network  string
	name     string
	dataDir  string
	port     int
	gateway  string
	api      *gofiapi.Client
	observer *invariants.DockerObserver
}

func docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func newComposeDeployment(ctx context.Context) (*composeDeployment, error) {
	image := os.Getenv("GOFI_E2E_IMAGE")
	if image == "" {
		image = "ghcr.io/wcrum/gofi:0.2.0"
	}
	if _, err := docker(ctx, "image", "inspect", image); err != nil {
		return nil, fmt.Errorf("GOFI_E2E_IMAGE %s not present locally (docker build -t %s .): %w", image, image, err)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	d := &composeDeployment{image: image, network: "gofi-e2e-" + suffix, name: "gofi-e2e-ctrl-" + suffix}
	dir, err := os.MkdirTemp("", "gofi-e2e-compose-")
	if err != nil {
		return nil, err
	}
	// The controller runs as root in the Compose shape (Docker socket).
	_ = os.Chmod(dir, 0o777)
	d.dataDir = dir
	if _, err := docker(ctx, "network", "create", d.network); err != nil {
		return nil, err
	}
	gateway, err := docker(ctx, "network", "inspect", d.network, "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if err != nil {
		d.Close()
		return nil, err
	}
	d.gateway = gateway
	port, err := proc.FreePort()
	if err != nil {
		d.Close()
		return nil, err
	}
	d.port = port
	d.api = gofiapi.New(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err := d.startController(ctx); err != nil {
		d.Close()
		return nil, err
	}
	d.observer, err = invariants.NewDockerObserver(d.network)
	if err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *composeDeployment) startController(ctx context.Context) error {
	args := []string{"run", "-d", "--name", d.name, "--network", d.network, "--user", "0:0",
		"-v", "/var/run/docker.sock:/var/run/docker.sock", "-v", d.dataDir + ":/data",
		"-e", "GOFI_DEPLOY_MODE=compose", "-e", "GOFI_DRIVER=docker", "-e", "GOFI_IMAGE=" + d.image,
		"-e", "GOFI_DOCKER_NETWORK=" + d.network, "-e", "GOFI_CONFIG_FILE=/data/listeners.yaml", "-e", "GOFI_HISTORY_DIR=/data/history",
		"-e", "GOFI_DRAIN_TIMEOUT=5s",
		"-p", fmt.Sprintf("127.0.0.1:%d:9090", d.port), d.image}
	if _, err := docker(ctx, args...); err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return d.api.WaitReady(readyCtx)
}

func (d *composeDeployment) Name() string         { return "compose" }
func (d *composeDeployment) API() *gofiapi.Client { return d.api }

// DataAddr: the Docker driver publishes each listen port 1:1 on the host.
func (d *composeDeployment) DataAddr(def spec.FlowDefinition) string {
	return fmt.Sprintf("127.0.0.1:%d", def.Source.Listening().Port)
}

// SinkURL: listener containers reach the host through the network gateway.
func (d *composeDeployment) SinkURL(r *traffic.Receiver) string {
	_, port, _ := strings.Cut(r.Addr(), ":")
	if i := strings.LastIndex(r.Addr(), ":"); i >= 0 {
		port = r.Addr()[i+1:]
	}
	return "http://" + d.gateway + ":" + port
}

func (d *composeDeployment) Restart(ctx context.Context) error {
	if _, err := docker(ctx, "stop", "-t", "30", d.name); err != nil {
		return err
	}
	if _, err := docker(ctx, "start", d.name); err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return d.api.WaitReady(readyCtx)
}

func (d *composeDeployment) Kill(ctx context.Context) error {
	if _, err := docker(ctx, "kill", "-s", "KILL", d.name); err != nil {
		return err
	}
	if _, err := docker(ctx, "start", d.name); err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return d.api.WaitReady(readyCtx)
}

// KillRuntime SIGKILLs the flow's listener container; the controller's
// resync must notice the dead container and recreate it.
func (d *composeDeployment) KillRuntime(ctx context.Context, flow string) error {
	_, err := docker(ctx, "kill", "-s", "KILL", "gofi-listener-"+flow)
	return err
}

func (d *composeDeployment) Observe(ctx context.Context) (invariants.World, error) {
	w, err := invariants.ObserveAPI(ctx, d.api, invariants.APIOptions{})
	if err != nil {
		return w, err
	}
	dw, err := d.observer.Observe(ctx, w.Desired, nil)
	if err != nil {
		return w, err
	}
	// Docker sees every container, including exited leftovers and
	// duplicates, so it replaces the API's one-per-flow view.
	w.Runtimes = dw.Runtimes
	w.Orphans = append(w.Orphans, dw.Orphans...)
	w.Bindings = dw.Bindings
	w.Observed += "+" + dw.Observed
	return w, nil
}

func (d *composeDeployment) ControllerRSS() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := docker(ctx, "stats", "--no-stream", "--format", "{{json .}}", d.name)
	if err != nil {
		return 0, err
	}
	var stats struct {
		MemUsage string `json:"MemUsage"`
	}
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		return 0, err
	}
	used, _, _ := strings.Cut(stats.MemUsage, " / ")
	return parseSize(used)
}

func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			v, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			if err != nil {
				return 0, err
			}
			return int64(v * u.mult), nil
		}
	}
	return 0, fmt.Errorf("unparsable size %q", s)
}

func (d *composeDeployment) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Stop the controller first so it does not recreate listeners we remove.
	_, _ = docker(ctx, "rm", "-f", d.name)
	if out, err := docker(ctx, "ps", "-aq", "--filter", "network="+d.network); err == nil && out != "" {
		args := append([]string{"rm", "-f"}, strings.Fields(out)...)
		_, _ = docker(ctx, args...)
	}
	_, _ = docker(ctx, "network", "rm", d.network)
	if os.Getenv("GOFI_E2E_KEEP") == "" {
		os.RemoveAll(d.dataDir)
	}
}
