package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wcrum/gofi/api"
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
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	slog.Info("=== gofi starting ===")

	// Listener mode: run a single pipeline defined by the LISTENER_SPEC env var.
	// This is how Docker/Kubernetes drivers spawn independent listener instances.
	if raw := os.Getenv("LISTENER_SPEC"); raw != "" {
		runListenerMode(raw)
		return
	}

	// Controller mode: ConnectRPC services + reconcile loop.
	runControllerMode()
}

// runListenerMode decodes a single ListenerDefinition from JSON and runs it
// until the process receives SIGTERM/SIGINT. Used by Docker and K8s drivers.
func runListenerMode(raw string) {
	var def spec.ListenerDefinition
	if err := json.Unmarshal([]byte(raw), &def); err != nil {
		slog.Error("listener mode: invalid LISTENER_SPEC", "error", err)
		os.Exit(1)
	}

	slog.Info("listener mode", "name", def.Name, "port", def.Source.Port)

	ln, err := listener.New(def)
	if err != nil {
		slog.Error("listener mode: failed to start", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		defer ln.Close()
		ln.Run(ctx)
	}()

	// Expose this listener's tap over the network so the controller (running
	// as a separate container/Pod under Docker/Kubernetes) can proxy live
	// ConnectRPC traffic to it. See taprelay and driver.TapTarget.
	relayAddr := envOrDefault("TAP_RELAY_ADDR", ":9091")
	go func() {
		if err := taprelay.New(ln.Tap).ListenAndServe(ctx, relayAddr); err != nil {
			slog.Error("tap relay server error", "name", def.Name, "error", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	slog.Info("listener mode: shutting down", "name", def.Name)
}

// runControllerMode starts the event loop and ConnectRPC services.
func runControllerMode() {
	apiAddr := envOrDefault("INGRESS_API_ADDR", ":9090")

	mode := deploymode.Detect()
	slog.Info("deployment mode detected", "mode", mode)

	d := newDriver(mode)
	slog.Info("driver selected", "kind", d.Kind())
	ctrl := controller.New(d)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var store desired.Store
	var err error
	if d.Kind() == "kubernetes" {
		store, err = desired.NewKubernetes(k8sNamespace())
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
	if store != nil {
		if err = ctrl.WithStore(ctx, store); err != nil {
			slog.Error("desired state load failed", "error", err)
			os.Exit(1)
		}
	}

	manager := startSandboxes(ctx, ctrl, mode)
	go ctrl.Run(ctx)

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
		Handler:           api.New(ctrl, staticFS, mode, manager),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		Protocols:         protocols,
	}

	go func() {
		slog.Info("API server listening", "addr", apiAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("API server error", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("=== gofi ready ===", "api", apiAddr, "ui", apiAddr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down...")
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	_ = srv.Shutdown(shutdownCtx)
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
