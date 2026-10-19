//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/internal/testkit/envtest"
	"github.com/wcrum/gofi/internal/testkit/fakedriver"
	"github.com/wcrum/gofi/internal/testkit/invariants"
	"github.com/wcrum/gofi/internal/testkit/report"
	"github.com/wcrum/gofi/spec"
)

// repoRoot locates the module root (tests run with cwd = package dir).
func repoRoot(t testing.TB) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// waitFor polls cond until true or timeout.
func waitFor(t testing.TB, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// startController wires a controller to store and driver and runs its loop
// until the returned stop function is called.
func startController(t testing.TB, store desired.Store, d *fakedriver.Driver) (*controller.Controller, func()) {
	t.Helper()
	ctrl := controller.New(d)
	ctx, cancel := context.WithCancel(context.Background())
	if err := ctrl.WithStore(ctx, store); err != nil {
		cancel()
		t.Fatalf("WithStore: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctrl.Run(ctx)
	}()
	return ctrl, func() {
		cancel()
		<-done
	}
}

// runtimesOf converts the fake driver's state to observed runtimes.
func runtimesOf(d *fakedriver.Driver) []invariants.Runtime {
	running := d.Running()
	out := make([]invariants.Runtime, 0, len(running))
	for name, def := range running {
		def := def
		out = append(out, invariants.Runtime{Flow: name, ID: "fake-" + name, Status: "running", Def: &def})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Flow < out[j].Flow })
	return out
}

// converged reports whether the driver runs exactly the running subset of defs.
func converged(d *fakedriver.Driver, defs []spec.FlowDefinition) bool {
	running := d.Running()
	want := 0
	for _, def := range defs {
		if def.Stopped() {
			if _, ok := running[def.Name]; ok {
				return false
			}
			continue
		}
		want++
		if _, ok := running[def.Name]; !ok {
			return false
		}
	}
	return len(running) == want
}

// checkInvariants fails the test on any violation of the in-memory world.
func checkInvariants(t testing.TB, store desired.Store, d *fakedriver.Driver, extra func(*invariants.World)) {
	t.Helper()
	defs, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store load: %v", err)
	}
	w := invariants.World{Desired: defs, Runtimes: runtimesOf(d), Observed: "store+fakedriver"}
	if extra != nil {
		extra(&w)
	}
	if violations := invariants.Check(w, invariants.Options{RuntimesExpected: true}); len(violations) > 0 {
		t.Fatalf("%s", invariants.Summary(violations))
	}
}

// fileStore returns a flow file store in a temp dir.
func fileStore(t testing.TB) (*desired.File, string) {
	dir := t.TempDir()
	return &desired.File{Path: filepath.Join(dir, "listeners.yaml")}, dir
}

// seed saves defs into store.
func seed(t testing.TB, store desired.Store, defs []spec.FlowDefinition) {
	t.Helper()
	for _, def := range defs {
		if err := store.Save(context.Background(), def); err != nil {
			t.Fatalf("seed %s: %v", def.Name, err)
		}
	}
}

// scenario opens a report for this test and writes it on cleanup.
func scenario(t testing.TB, deployment string) *report.Scenario {
	s := report.Start(t.Name(), deployment)
	t.Cleanup(func() {
		s.Finish(!t.Failed(), "", nil)
		if path, err := s.Write(); err == nil {
			t.Logf("report: %s", path)
		}
	})
	return s
}

var (
	envOnce sync.Once
	envRef  *envtest.Environment
	envErr  error
)

// sharedEnv starts one envtest control plane for the package (skipping when
// the binaries are absent) and installs GoFi's CRDs plus a stand-in UDS
// Package CRD.
func sharedEnv(t testing.TB) *envtest.Environment {
	t.Helper()
	envOnce.Do(func() {
		if _, err := envtest.Assets(); err != nil {
			envErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		envRef, envErr = envtest.Start(ctx, envtest.Options{
			CRDDirs:   []string{filepath.Join(repoRoot(t), "charts", "gofi", "crds")},
			ExtraCRDs: []*envtest.CRD{envtest.PreserveUnknownCRD("uds.dev", "v1alpha1", "Package", "packages")},
		})
	})
	if envErr != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("envtest required in CI: %v", envErr)
		}
		t.Skipf("envtest unavailable: %v", envErr)
	}
	return envRef
}

func TestMain(m *testing.M) {
	code := m.Run()
	if envRef != nil {
		envRef.Stop()
	}
	os.Exit(code)
}

var nsCounter int

// namespace creates a fresh namespace on the shared control plane.
func namespace(t testing.TB, env *envtest.Environment) string {
	t.Helper()
	nsCounter++
	name := fmt.Sprintf("gofi-it-%d-%d", os.Getpid()%10000, nsCounter)
	if err := env.EnsureNamespace(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	return name
}
