//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wcrum/gofi/internal/testkit/gofiapi"
	"github.com/wcrum/gofi/internal/testkit/invariants"
	"github.com/wcrum/gofi/internal/testkit/proc"
	"github.com/wcrum/gofi/internal/testkit/traffic"
	"github.com/wcrum/gofi/spec"
)

// binaryDeployment runs the controller as a child process with in-process
// runtimes (the single-binary shape).
type binaryDeployment struct {
	ctrl *proc.Controller
	api  *gofiapi.Client
	dir  string
}

func newBinaryDeployment(ctx context.Context) (*binaryDeployment, error) {
	dir, err := os.MkdirTemp("", "gofi-e2e-binary-")
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, key := range []string{"GOFI_PROFILE", "GOMEMLIMIT", "GOFI_SANDBOX_IMAGE"} {
		if v := os.Getenv(key); v != "" {
			env[key] = v
		}
	}
	ctrl, err := proc.Start(ctx, proc.Options{Binary: binaryPath, Dir: dir, Env: env})
	if err != nil {
		return nil, err
	}
	d := &binaryDeployment{ctrl: ctrl, dir: dir, api: gofiapi.New(ctrl.URL())}
	return d, nil
}

func (d *binaryDeployment) Name() string         { return "binary" }
func (d *binaryDeployment) API() *gofiapi.Client { return d.api }
func (d *binaryDeployment) DataAddr(def spec.FlowDefinition) string {
	return fmt.Sprintf("127.0.0.1:%d", def.Source.Listening().Port)
}
func (d *binaryDeployment) SinkURL(r *traffic.Receiver) string { return r.URL() }

func (d *binaryDeployment) Restart(ctx context.Context) error {
	if _, err := d.ctrl.Stop(30 * time.Second); err != nil {
		return err
	}
	return d.ctrl.Restart(ctx)
}

func (d *binaryDeployment) Kill(ctx context.Context) error {
	if err := d.ctrl.Kill(); err != nil {
		return err
	}
	return d.ctrl.Restart(ctx)
}

func (d *binaryDeployment) KillRuntime(context.Context, string) error { return errNotSupported }

func (d *binaryDeployment) Observe(ctx context.Context) (invariants.World, error) {
	w, err := invariants.ObserveAPI(ctx, d.api, invariants.APIOptions{})
	if err != nil {
		return w, err
	}
	file, err := invariants.ObserveFile(ctx, d.ctrl.ConfigPath(), d.ctrl.HistoryDir())
	if err != nil {
		return w, err
	}
	// The file is the source of truth for history (definitions included).
	w.History = file.History
	return w, nil
}

func (d *binaryDeployment) ControllerRSS() (int64, error) { return d.ctrl.RSS() }

func (d *binaryDeployment) Close() {
	_, _ = d.ctrl.Stop(30 * time.Second)
	if os.Getenv("GOFI_E2E_KEEP") == "" {
		os.RemoveAll(d.dir)
	} else {
		fmt.Fprintln(os.Stderr, "kept", d.dir, "log", d.ctrl.LogPath())
	}
}

// controller exposes the process for binary-only scenarios.
func (d *binaryDeployment) controller() *proc.Controller { return d.ctrl }

// configPath is the flow file.
func (d *binaryDeployment) configPath() string { return filepath.Join(d.dir, "listeners.yaml") }
