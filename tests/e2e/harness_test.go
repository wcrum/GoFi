//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wcrum/gofi/internal/testkit/gofiapi"
	"github.com/wcrum/gofi/internal/testkit/invariants"
	"github.com/wcrum/gofi/internal/testkit/proc"
	"github.com/wcrum/gofi/internal/testkit/report"
	"github.com/wcrum/gofi/internal/testkit/traffic"
	"github.com/wcrum/gofi/spec"
)

// Deployment is one shape of a running GoFi.
type Deployment interface {
	// Name is binary, compose, or kubernetes.
	Name() string
	// API talks to the controller.
	API() *gofiapi.Client
	// DataAddr is where a test sends traffic for def (host:port).
	DataAddr(def spec.FlowDefinition) string
	// SinkURL is the URL a flow runtime uses to reach a receiver the test
	// started on the host.
	SinkURL(receiver *traffic.Receiver) string
	// Restart stops the controller gracefully and starts it again.
	Restart(ctx context.Context) error
	// Kill stops the controller abruptly (SIGKILL) and starts it again.
	Kill(ctx context.Context) error
	// KillRuntime kills a flow's runtime out from under the controller; it
	// returns ErrNotSupported when the shape has no separate runtime.
	KillRuntime(ctx context.Context, flow string) error
	// Observe returns the world as this shape's own observer sees it,
	// merged with the API view.
	Observe(ctx context.Context) (invariants.World, error)
	// ControllerRSS is the controller process's resident memory in bytes.
	ControllerRSS() (int64, error)
	// Close tears the deployment down.
	Close()
}

var errNotSupported = fmt.Errorf("not supported by this deployment shape")

var (
	binaryPath string
	repoRoot   string
	deployment Deployment
	// basePort is the first data port tests allocate from (each scenario
	// takes a disjoint range so they can run in any order).
	basePort = 31000
)

func TestMain(m *testing.M) {
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	repoRoot = root
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	mode := os.Getenv("GOFI_E2E_MODE")
	if mode == "" {
		mode = "binary"
	}
	if mode == "binary" || mode == "edge" || mode == "" {
		bin, err := proc.Build(ctx, root, filepath.Join(root, "bin", "gofi-e2e"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "build:", err)
			cancel()
			os.Exit(2)
		}
		binaryPath = bin
	}
	switch mode {
	case "binary", "edge":
		deployment, err = newBinaryDeployment(ctx)
	case "compose":
		deployment, err = newComposeDeployment(ctx)
	case "kubernetes":
		deployment, err = newKubeDeployment(ctx)
	default:
		err = fmt.Errorf("unknown GOFI_E2E_MODE %q", mode)
	}
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deployment:", err)
		os.Exit(2)
	}
	code := m.Run()
	deployment.Close()
	os.Exit(code)
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// ports reserves n consecutive data ports for a scenario.
func ports(n int) int {
	p := basePort
	basePort += n
	return p
}

// scenario opens a report; it is written when the test ends.
func scenario(t *testing.T) *report.Scenario {
	s := report.Start(strings.ReplaceAll(t.Name(), "/", "-"), deployment.Name())
	t.Cleanup(func() {
		failure := ""
		if t.Failed() {
			failure = "see test log"
		}
		s.Finish(!t.Failed(), failure, nil)
		if path, err := s.Write(); err == nil {
			t.Logf("report: %s", path)
		} else {
			t.Logf("report not written: %v", err)
		}
	})
	return s
}

// receiver starts an HTTP receiver reachable from the deployment.
func receiver(t *testing.T) *traffic.Receiver {
	t.Helper()
	bind := "127.0.0.1:0"
	if deployment.Name() != "binary" {
		bind = "0.0.0.0:0"
	}
	r, err := traffic.StartReceiver(bind)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// deploy saves def and waits until it runs and (for TCP) accepts.
func deploy(t *testing.T, def spec.FlowDefinition) spec.FlowDefinition {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := deployment.API().Upsert(ctx, def); err != nil {
		t.Fatalf("upsert %s: %v", def.Name, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = deployment.API().Delete(ctx, def.Name)
	})
	if !def.Stopped() {
		waitRunning(t, def)
	}
	return def
}

func waitRunning(t *testing.T, def spec.FlowDefinition) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := deployment.API().WaitPhase(ctx, def.Name, "running"); err != nil {
		t.Fatal(err)
	}
	if def.Source.Listen != nil && def.Source.Listen.Network != "udp" {
		// tcp (or "") is probed by a dial, quic by a handshake.
		if err := traffic.WaitListening(ctx, def.Source.Listen.Network, deployment.DataAddr(def)); err != nil {
			t.Fatal(err)
		}
	}
}

// assertInvariants observes the deployment and fails on any violation,
// retrying briefly because observation races the reconcile loop.
func assertInvariants(t *testing.T, rep *report.Scenario, opts invariants.Options) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var last []invariants.Violation
	for {
		w, err := deployment.Observe(ctx)
		if err == nil {
			last = invariants.Check(w, opts)
			if len(last) == 0 {
				rep.Set("invariants", "ok")
				return
			}
		} else {
			last = []invariants.Violation{{Rule: "observe", Subject: deployment.Name(), Detail: err.Error()}}
		}
		select {
		case <-ctx.Done():
			rep.Set("invariants", last)
			t.Fatalf("%s", invariants.Summary(last))
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func defaultOptions() invariants.Options {
	return invariants.Options{RuntimesExpected: true, PendingIsHealthy: deployment.Name() == "kubernetes"}
}

// metrics scrapes the controller.
func metrics(t *testing.T) gofiapi.Samples {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := deployment.API().Metrics(ctx)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	return s
}

func mib(b int64) float64 { return proc.MiB(b) }
