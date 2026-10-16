package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/sandbox"
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
