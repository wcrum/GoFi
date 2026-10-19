package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wcrum/gofi/api"
	"github.com/wcrum/gofi/config"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/listener"
	"github.com/wcrum/gofi/spec"
	"github.com/wcrum/gofi/taprelay"
)

//go:generate sh -c "cd ui && npm run build"

func main() {
	// Runtime limits (GOFI_PROFILE presets + GOFI_* overrides) are resolved
	// and applied first: log handler, GOMEMLIMIT from the cgroup/GOFI_MEMORY_LIMIT,
	// GOMAXPROCS. See docs/configuration.md.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	config.Set(cfg)
	applied := config.Apply(cfg)
	slog.Info("=== gofi starting ===", "profile", cfg.Profile)
	slog.Info("runtime limits applied", "memoryBudget", applied.MemoryLimitBytes, "memorySource", applied.MemorySource, "gomemlimit", applied.GOMEMLIMIT, "gomaxprocs", applied.GOMAXPROCS, "logLevel", applied.LogLevel.String())

	// Runtime mode: run a single flow defined by the FLOW_SPEC env var. This is
	// how the Docker/Kubernetes drivers spawn independent flow runtimes.
	if raw := os.Getenv(spec.FlowSpecEnv); raw != "" {
		runListenerMode(raw)
		return
	}

	// Controller mode: ConnectRPC services + reconcile loop.
	runControllerMode()
}

// runListenerMode decodes a single FlowDefinition from JSON and runs it
// until the process receives SIGTERM/SIGINT. Used by Docker and K8s drivers.
func runListenerMode(raw string) {
	var def spec.FlowDefinition
	if err := json.Unmarshal([]byte(raw), &def); err != nil {
		slog.Error("runtime mode: invalid flow spec", "error", err)
		os.Exit(1)
	}

	slog.Info("runtime mode", "flow", def.Name, "port", def.Source.Listening().Port)

	// Bind every port before anything runs: the tap relay's, then the
	// flow's own (listener.New binds it and connects the sinks). A port
	// that cannot be bound stops the process here, with nothing started
	// and nothing to drain (fail fast; the controller reports the exit).
	relayAddr := envOrDefault("TAP_RELAY_ADDR", ":9091")
	relayLn, err := net.Listen("tcp", relayAddr)
	if err != nil {
		slog.Error("runtime mode: tap relay bind failed", "flow", def.Name, "addr", relayAddr, "error", err)
		os.Exit(1)
	}
	ln, err := listener.New(def)
	if err != nil {
		_ = relayLn.Close()
		slog.Error("runtime mode: failed to start", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		defer ln.Close()
		ln.Run(ctx)
	}()

	// Expose this listener's tap over the network so the controller (running
	// as a separate container/Pod under Docker/Kubernetes) can proxy live
	// ConnectRPC traffic to it. See taprelay and driver.TapTarget. The relay
	// outlives the drain, so metrics and bulletins stay readable meanwhile.
	relayCtx, stopRelay := context.WithCancel(context.Background())
	defer stopRelay()
	go func() {
		if err := taprelay.New(ln.Tap).Serve(relayCtx, relayLn); err != nil {
			slog.Error("tap relay server error", "name", def.Name, "error", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	// Graceful stop: stop accepting, finish frames already received, flush
	// sinks, then exit; a second signal or the deadline stops hard.
	timeout := driver.DrainTimeout()
	slog.Info("runtime mode: draining", "flow", def.Name, "timeout", timeout)
	drainCtx, stopDrain := context.WithTimeout(context.Background(), timeout)
	defer stopDrain()
	go func() {
		select {
		case <-sig:
			slog.Warn("runtime mode: second signal; stopping hard", "flow", def.Name)
			stopDrain()
		case <-drainCtx.Done():
		}
	}()
	started := time.Now()
	if err := ln.Shutdown(drainCtx); err != nil {
		slog.Warn("runtime mode: drain incomplete", "flow", def.Name, "error", err)
	}
	cancel()
	<-runDone
	slog.Info("runtime mode: shut down", "flow", def.Name, "drainedIn", time.Since(started).Round(time.Millisecond))
}

// runControllerMode starts the event loop and ConnectRPC services.
func runControllerMode() {
	apiAddr := envOrDefault("INGRESS_API_ADDR", ":9090")
	// Bind the API port before the driver, the store, or any flow starts: a
	// port conflict stops the process with nothing started, instead of
	// after in-process flows bound their ports and containers were created.
	apiLn, err := net.Listen("tcp", apiAddr)
	if err != nil {
		slog.Error("API bind failed", "addr", apiAddr, "error", err)
		os.Exit(1)
	}

	mode := deploymode.Detect()
	slog.Info("deployment mode detected", "mode", mode)

	d := newDriver(mode)
	slog.Info("driver selected", "kind", d.Kind())
	ctrl := controller.New(d)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Kubernetes: informer caches, leader election, readiness (kubernetes_runtime.go).
	kubeRuntime = startKubernetes(ctx, d, ctrl)

	var store desired.Store
	if d.Kind() == "kubernetes" {
		store, err = kubeRuntime.flowStore(k8sNamespace())
	} else {
		path := os.Getenv("GOFI_CONFIG_FILE")
		if path == "" {
			dir, dirErr := os.UserConfigDir()
			if dirErr != nil {
				slog.Error("configuration directory unavailable", "error", dirErr)
				os.Exit(1)
			}
			path = filepath.Join(dir, "gofi", "listeners.yaml")
		}
		store = &desired.File{Path: path}
	}
	if err != nil {
		slog.Error("desired state initialization failed", "error", err)
		os.Exit(1)
	}
	// The CEL library must be connected before listeners load, because
	// stored listeners may link library entries.
	if lib := newExpressionLibrary(ctx, d.Kind(), store); lib != nil {
		ctrl.SetExpressions(lib)
	}
	if store != nil {
		if err = ctrl.WithStore(ctx, store); err != nil {
			slog.Error("desired state load failed", "error", err)
			os.Exit(1)
		}
	}

	// The assistant configuration comes first: AI libraries inherit its
	// endpoint through the sandbox supervisor.
	bot := newAssistant(ctx, d.Kind(), store)
	manager, llmServer := startSandboxes(ctx, ctrl, mode, bot)
	kubeRuntime.elect(ctx, ctrl, manager)
	go ctrl.Run(ctx)
	revisions := startHistory(ctx, d.Kind(), store, ctrl)

	// Serve embedded SPA if the dist directory was bundled at build time.
	var staticFS fs.FS
	sub, err := fs.Sub(embeddedUI, "ui/dist")
	if err == nil {
		staticFS = sub
	}

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{
		Addr:              apiAddr,
		Handler:           kubeRuntime.wrap(api.NewWithDeps(ctrl, staticFS, mode, api.Deps{History: revisions, Assistant: bot, Docs: assistantDocsFS()}, manager)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		Protocols:         protocols,
	}

	go func() {
		slog.Info("API server listening", "addr", apiLn.Addr().String())
		if err := srv.Serve(apiLn); err != nil && err != http.ErrServerClosed {
			slog.Error("API server error", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("=== gofi ready ===", "api", apiAddr, "ui", apiAddr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down...")
	cancel() // stops reconciling; in-process runtimes keep running until drained
	// In-process flows drain before the controller exits (other drivers'
	// runtimes keep running without it).
	if drainer, ok := d.(driver.Drainer); ok {
		drainCtx, stopDrain := context.WithTimeout(context.Background(), driver.DrainTimeout())
		if err := drainer.DrainAll(drainCtx); err != nil {
			slog.Warn("flow drain incomplete", "error", err)
		}
		stopDrain()
	}
	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	_ = srv.Shutdown(shutdownCtx)
	if llmServer != nil {
		_ = llmServer.Shutdown(shutdownCtx)
	}
	if manager != nil {
		if err := manager.CloseTooling(shutdownCtx); err != nil {
			slog.Warn("Python tooling cleanup failed", "error", err)
		}
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// newDriver selects the runtime driver to use, based on the detected
// deployment mode unless overridden by GOFI_DRIVER. Note that ModeDocker (a
// generic "we're in some container but can't tell it's Compose" fallback)
// intentionally falls through to InProcess, not Docker — we should never
// assume Docker-socket/network capability we haven't actually confirmed.
//
// If a driver's prerequisites are missing, this fails fast (os.Exit(1))
// rather than silently falling back to InProcess: a deployment that
// explicitly declares compose/kubernetes should error loudly, not silently
// run every listener in the controller's own process.
func newDriver(mode deploymode.Mode) driver.Driver {
	kind := os.Getenv("GOFI_DRIVER")
	if kind == "" {
		switch mode {
		case deploymode.ModeCompose:
			kind = "docker"
		case deploymode.ModeKubernetes:
			kind = "kubernetes"
		default:
			kind = "inprocess"
		}
	}

	switch kind {
	case "docker":
		d, err := driver.NewDocker(driver.DockerConfig{
			Image:   os.Getenv("GOFI_IMAGE"),
			Network: os.Getenv("GOFI_DOCKER_NETWORK"),
		})
		if err != nil {
			slog.Error("docker driver init failed", "error", err)
			os.Exit(1)
		}
		return d
	case "kubernetes":
		d, err := driver.NewKube(k8sNamespace(), os.Getenv("GOFI_IMAGE"))
		if err != nil {
			slog.Error("kube driver init failed", "error", err)
			os.Exit(1)
		}
		return d
	default:
		return driver.NewInProcess()
	}
}

// k8sNamespace resolves the namespace the Kubernetes driver should manage
// Pods in: an explicit GOFI_K8S_NAMESPACE override, or else the namespace
// the projected serviceaccount token tells us we're running in.
func k8sNamespace() string {
	if v := os.Getenv("GOFI_K8S_NAMESPACE"); v != "" {
		return v
	}
	b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
