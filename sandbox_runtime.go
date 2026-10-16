package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/functionrpc"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/sandbox"
	"github.com/wcrum/gofi/spec"
)

func startSandboxes(ctx context.Context, ctrl *controller.Controller, mode deploymode.Mode) *sandbox.Supervisor {
	var backend sandbox.Backend
	var store sandbox.Store
	var err error
	if ctrl.DriverKind() == "kubernetes" {
		backend, err = sandbox.NewKubernetes(k8sNamespace(), os.Getenv("GOFI_SANDBOX_IMAGE"))
		if err == nil {
			store, err = sandbox.NewKubernetesStore(k8sNamespace())
		}
	} else {
		backend, err = sandbox.NewDocker(sandbox.DockerConfig{Image: os.Getenv("GOFI_SANDBOX_IMAGE"), Network: os.Getenv("GOFI_SANDBOX_NETWORK"), InContainer: mode == deploymode.ModeCompose || mode == deploymode.ModeDocker})
		path := os.Getenv("GOFI_SANDBOX_CONFIG_FILE")
		if path == "" {
			path = filepath.Join(filepath.Dir(ctrl.Configuration().Path), "sandboxes.yaml")
		}
		store = &sandbox.FileStore{Path: path}
	}
	if err != nil {
		slog.Error("Python sandbox initialization failed", "error", err)
		return nil
	}
	manager := sandbox.New(backend, store)
	functionrpc.SetResolver(func(ctx context.Context) (map[string]functionrpc.Binding, error) {
		bindings, err := manager.Bindings(ctx)
		out := map[string]functionrpc.Binding{}
		for name, b := range bindings {
			out[name] = functionrpc.Binding{Endpoint: b.Endpoint, Token: b.Token, TimeoutSeconds: b.TimeoutSeconds}
		}
		return out, err
	})
	functionrpc.SetInlineRunner(func(ctx context.Context, ref *spec.PythonRef, req *gofiv1.WorkerInvokeRequest) (*gofiv1.WorkerInvokeResponse, error) {
		return runInline(ctx, manager, ref, req)
	})
	wake := make(chan struct{}, 1)
	controller.SetInlineHooks(controller.InlineHooks{
		Validate: func(ctx context.Context, def spec.FlowDefinition) error { return validateInline(ctx, manager, def) },
		Changed: func() {
			select {
			case wake <- struct{}{}:
			default:
			}
		},
	})
	go syncManagedLibraries(ctx, ctrl, manager, wake)
	go func() {
		if err := manager.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("Python sandbox reconciliation stopped", "error", err)
		}
	}()
	go func() {
		previous := ""
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				bindings, _ := manager.Bindings(ctx)
				data, _ := json.Marshal(bindings)
				current := string(data)
				if current != previous {
					ctrl.NotifyRuntimeChange()
					previous = current
				}
			}
		}
	}()
	return manager
}

func toFunctionBinding(b sandbox.Binding) functionrpc.Binding {
	return functionrpc.Binding{Endpoint: b.Endpoint, Token: b.Token, TimeoutSeconds: b.TimeoutSeconds}
}

// runInline executes an unresolved inline stage (an editor simulation). A
// ready managed worker already running the identical snippet is reused;
// otherwise a scratch worker runs it, so unsaved edits are what executes.
func runInline(ctx context.Context, manager *sandbox.Supervisor, ref *spec.PythonRef, req *gofiv1.WorkerInvokeRequest) (*gofiv1.WorkerInvokeResponse, error) {
	if binding, ok := manager.InlineBinding(ref.Function, ref.InlineSource); ok {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(max(binding.TimeoutSeconds, 1))*time.Second)
		defer cancel()
		return functionrpc.Call(callCtx, toFunctionBinding(binding), req)
	}
	source := "import gofi as _gofi\n" + sandbox.WrapInline(ref.Function, ref.InlineSource)
	var out *gofiv1.WorkerInvokeResponse
	err := manager.RunScratch(ctx, sandbox.Definition{SourceCode: source}, func(ctx context.Context, binding sandbox.Binding) error {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(binding.TimeoutSeconds+5)*time.Second)
		defer cancel()
		var err error
		out, err = functionrpc.Call(callCtx, toFunctionBinding(binding), req)
		return err
	})
	return out, err
}

// validateInline statically checks every inline stage (parsed and linted in
// the tooling worker, never executed). Diagnostics use snippet line numbers.
func validateInline(ctx context.Context, manager *sandbox.Supervisor, def spec.FlowDefinition) error {
	total := 0
	for _, stage := range def.InlineStages() {
		total += len(stage.Python.InlineSource)
		result, err := manager.ValidateStatic(ctx, stage.Python.InlineSource)
		if err != nil {
			return fmt.Errorf("stage %q: inline Python cannot be validated: %w", stage.ID, err)
		}
		if !result.Valid {
			problems := make([]string, 0, len(result.Diagnostics))
			for _, d := range result.Diagnostics {
				problems = append(problems, fmt.Sprintf("line %d:%d %s %s", d.Line, d.Column, d.Code, d.Message))
			}
			return fmt.Errorf("stage %q: inline Python is invalid: %s", stage.ID, strings.Join(problems, "; "))
		}
	}
	if total > 60000 {
		return fmt.Errorf("inline Python stages exceed the 64 KiB library limit together")
	}
	return nil
}

// syncManagedLibraries keeps one managed library per listener with inline
// stages and removes managed libraries whose listener no longer needs one.
// User libraries are never touched.
func syncManagedLibraries(ctx context.Context, ctrl *controller.Controller, manager *sandbox.Supervisor, wake <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		want := map[string]sandbox.Definition{}
		for _, def := range ctrl.List() {
			if lib, ok := sandbox.InlineLibrary(def); ok {
				want[lib.Name] = lib
			}
		}
		for name, lib := range want {
			current, exists := manager.Get(name)
			status, _ := manager.Status(name)
			if exists && current.SourceCode == lib.SourceCode && current.ManagedBy == lib.ManagedBy && status.Phase == "Ready" {
				continue
			}
			if exists && !current.Managed() {
				slog.Error("managed library name collides with a user library", "library", name)
				continue
			}
			applyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			if err := manager.Apply(applyCtx, lib); err != nil {
				slog.Error("managed Python library start failed", "library", name, "listener", strings.TrimPrefix(lib.ManagedBy, "flow:"), "error", err)
			} else {
				slog.Info("managed Python library ready", "library", name, "listener", strings.TrimPrefix(lib.ManagedBy, "flow:"))
			}
			cancel()
		}
		for _, d := range manager.List() {
			if _, needed := want[d.Name]; d.Managed() && !needed {
				deleteCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
				if err := manager.Delete(deleteCtx, d.Name); err != nil {
					slog.Warn("managed Python library removal failed", "library", d.Name, "error", err)
				} else {
					slog.Info("managed Python library removed", "library", d.Name)
				}
				cancel()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
	}
}
