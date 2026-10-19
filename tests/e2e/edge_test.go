//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wcrum/gofi/internal/testkit/flows"
	"github.com/wcrum/gofi/internal/testkit/gofiapi"
	"github.com/wcrum/gofi/internal/testkit/invariants"
	"github.com/wcrum/gofi/internal/testkit/proc"
	"github.com/wcrum/gofi/internal/testkit/report"
	"github.com/wcrum/gofi/internal/testkit/traffic"
)

// TestEdgeProfile runs the static binary inside a cgroup-limited container
// (128 MiB, half a CPU, GOMEMLIMIT=64MiB, GOFI_PROFILE=edge) on the host
// network: it must start, serve 20 flows, survive a 10k-message burst
// without being OOM-killed, and come back from SIGKILL with its configuration
// intact. linux/arm64 runs too when the daemon can emulate it (binfmt).
// Only the binary mode harness provides the static binary this needs.
func TestEdgeProfile(t *testing.T) {
	if deployment.Name() != "binary" {
		t.Skip("edge profile runs from the binary harness")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	platforms := []string{"linux/amd64"}
	if os.Getenv("GOFI_EDGE_ARM64") != "" {
		platforms = append(platforms, "linux/arm64")
	}
	for _, platform := range platforms {
		t.Run(strings.ReplaceAll(platform, "/", "-"), func(t *testing.T) { runEdge(t, platform) })
	}
}

func runEdge(t *testing.T, platform string) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	rep := report.Start(strings.ReplaceAll(t.Name(), "/", "-"), "edge")
	defer func() {
		rep.Finish(!t.Failed(), "", nil)
		if path, err := rep.Write(); err == nil {
			t.Logf("report: %s", path)
		}
	}()
	image := envOr("GOFI_EDGE_BASE_IMAGE", "alpine:3.23")
	bin := binaryPath
	if platform != "linux/amd64" {
		arch := strings.TrimPrefix(platform, "linux/")
		out := filepath.Join(repoRoot, "bin", "gofi-e2e-"+arch)
		cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", out, ".")
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch, "GOWORK=off")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cross build: %v\n%s", err, output)
		}
		bin = out
		if _, err := docker(ctx, "run", "--rm", "--platform", platform, image, "uname", "-m"); err != nil {
			t.Skipf("%s emulation unavailable: %v", platform, err)
		}
	}
	dir, err := os.MkdirTemp("", "gofi-edge-")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0o777)
	defer os.RemoveAll(dir)
	port, err := proc.FreePort()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("gofi-edge-%d", port)
	defer docker(context.Background(), "rm", "-f", name)
	memLimit := envOr("GOFI_EDGE_MEMORY", "128m")
	run := func() error {
		_, err := docker(ctx, "run", "-d", "--name", name, "--platform", platform, "--network", "host",
			"--memory", memLimit, "--memory-swap", memLimit, "--cpus", envOr("GOFI_EDGE_CPUS", "0.5"),
			"--read-only", "--tmpfs", "/tmp", "-v", bin+":/gofi:ro", "-v", dir+":/data",
			"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), // non-root, and the host can read what it writes
			"-e", "GOMEMLIMIT=64MiB", "-e", "GOFI_PROFILE=edge", "-e", "GOFI_DRIVER=inprocess", "-e", "GOFI_DEPLOY_MODE=binary",
			"-e", "GOFI_CONFIG_FILE=/data/listeners.yaml", "-e", "GOFI_HISTORY_DIR=/data/history",
			"-e", fmt.Sprintf("INGRESS_API_ADDR=127.0.0.1:%d", port), "-e", "TAP_RELAY_ADDR=127.0.0.1:0", "-e", "GOFI_DRAIN_TIMEOUT=3s",
			"--entrypoint", "/gofi", image)
		return err
	}
	startedAt := time.Now()
	if err := run(); err != nil {
		t.Fatal(err)
	}
	api := gofiapi.New(fmt.Sprintf("http://127.0.0.1:%d", port))
	readyCtx, cancelReady := context.WithTimeout(ctx, 90*time.Second)
	if err := api.WaitReady(readyCtx); err != nil {
		cancelReady()
		logs, _ := docker(ctx, "logs", name)
		t.Fatalf("edge container not ready: %v\n%s", err, logs)
	}
	cancelReady()
	rep.Set("startSeconds", time.Since(startedAt).Seconds())
	rep.Set("platform", platform)
	rep.Set("memoryLimit", memLimit)

	rx, err := traffic.StartReceiver("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rx.Close()
	const n = 20
	base := 36000
	names := []string{}
	for i := 0; i < n; i++ {
		def := flows.Listen(fmt.Sprintf("edge-%02d", i), base+i, flows.HTTPSink(rx.URL()))
		if err := api.Upsert(ctx, def); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		names = append(names, def.Name)
	}
	if err := api.WaitAllPhase(ctx, names, "running"); err != nil {
		t.Fatal(err)
	}
	rep.Set("flows", n)
	rep.Set("memAfterFlowsMiB", containerMemMiB(ctx, name))

	// 10k-message burst over 8 connections into one flow. The edge preset
	// bounds the sink queue (drop-oldest), so the burst may be shed: what
	// matters is that the process survives, memory stays bounded, and every
	// message is either delivered or counted as dropped.
	gen := &traffic.Generator{Network: "tcp", Addr: fmt.Sprintf("127.0.0.1:%d", base), Prefix: "burst", Connections: 8, Pad: 256}
	burstStart := time.Now()
	res := gen.Run(ctx, 10000)
	quiet, cancelQuiet := context.WithTimeout(ctx, 60*time.Second)
	_ = rx.WaitQuiet(quiet, 3*time.Second)
	cancelQuiet()
	samples, _ := api.Metrics(ctx)
	dropped := samples.Value("gofi_sink_queue_dropped_total", map[string]string{"listener": names[0]})
	delivered := rx.Stats().Unique
	rep.Set("burst.sent", res.Sent)
	rep.Set("burst.delivered", delivered)
	rep.Set("burst.droppedByQueue", dropped)
	rep.Set("burst.seconds", time.Since(burstStart).Seconds())
	rep.Set("memAfterBurstMiB", containerMemMiB(ctx, name))
	oom, _ := docker(ctx, "inspect", "--format", "{{.State.OOMKilled}} {{.State.Running}}", name)
	rep.Set("stateAfterBurst", oom)
	t.Logf("burst: sent %d, delivered %d, dropped by sink queue %.0f, memory %.1f MiB", res.Sent, delivered, dropped, containerMemMiB(ctx, name))
	if oom != "false true" {
		logs, _ := docker(ctx, "logs", "--tail", "20", name)
		t.Fatalf("container state after burst: %q\n%s", oom, logs)
	}
	if float64(delivered)+dropped < float64(res.Sent)*0.99 {
		t.Fatalf("burst accounting: %d sent, %d delivered, %.0f dropped (unaccounted loss)", res.Sent, delivered, dropped)
	}

	// SIGKILL while saves are in flight, then restart and verify config.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for round := 1; ; round++ {
			select {
			case <-stop:
				return
			default:
			}
			for i := 0; i < n; i++ {
				_ = api.Upsert(ctx, flows.Listen(fmt.Sprintf("edge-%02d", i), base+i, flows.HTTPSink(rx.URL()), flows.Transform("r", "round", fmt.Sprintf(`"%d"`, round))))
			}
		}
	}()
	time.Sleep(700 * time.Millisecond)
	if _, err := docker(ctx, "kill", "-s", "KILL", name); err != nil {
		t.Fatal(err)
	}
	close(stop)
	<-done
	if _, err := docker(ctx, "start", name); err != nil {
		t.Fatal(err)
	}
	readyCtx, cancelReady = context.WithTimeout(ctx, 90*time.Second)
	err = api.WaitReady(readyCtx)
	cancelReady()
	if err != nil {
		logs, _ := docker(ctx, "logs", "--tail", "30", name)
		t.Fatalf("edge container did not come back after SIGKILL: %v\n%s", err, logs)
	}
	w, err := invariants.ObserveFile(ctx, filepath.Join(dir, "listeners.yaml"), filepath.Join(dir, "history"))
	if err != nil {
		t.Fatalf("flow file unreadable after SIGKILL: %v", err)
	}
	if len(w.Desired) != n {
		t.Fatalf("%d of %d flows survived SIGKILL", len(w.Desired), n)
	}
	if err := api.WaitAllPhase(ctx, names, "running"); err != nil {
		t.Fatal(err)
	}
	aw, err := invariants.ObserveAPI(ctx, api, invariants.APIOptions{})
	if err != nil {
		t.Fatal(err)
	}
	aw.History = w.History
	if v := invariants.Check(aw, invariants.Options{RuntimesExpected: true}); len(v) > 0 {
		t.Fatalf("%s", invariants.Summary(v))
	}
	rep.Set("memAfterRestartMiB", containerMemMiB(ctx, name))
	rep.Set("invariants", "ok")
	for i := 0; i < n; i++ {
		_ = api.Delete(ctx, names[i])
	}
	_, _ = docker(ctx, "stop", "-t", "10", name)
}

func containerMemMiB(ctx context.Context, name string) float64 {
	out, err := docker(ctx, "stats", "--no-stream", "--format", "{{.MemUsage}}", name)
	if err != nil {
		return -1
	}
	used, _, _ := strings.Cut(out, " / ")
	b, err := parseSize(used)
	if err != nil {
		return -1
	}
	return proc.MiB(b)
}
