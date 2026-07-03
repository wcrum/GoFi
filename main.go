package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/cnak-us/gogo/api"
	"github.com/cnak-us/gogo/controller"
	"github.com/cnak-us/gogo/driver"
	"github.com/cnak-us/gogo/listener"
	"github.com/cnak-us/gogo/spec"
)

//go:generate sh -c "cd ui && npm run build"

func main() {
	slog.Info("=== cnak-ingress starting ===")

	// Listener mode: run a single pipeline defined by the LISTENER_SPEC env var.
	// This is how Docker/Kubernetes drivers spawn independent listener instances.
	if raw := os.Getenv("LISTENER_SPEC"); raw != "" {
		runListenerMode(raw)
		return
	}

	// Controller mode: REST API + reconcile loop.
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

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	slog.Info("listener mode: shutting down", "name", def.Name)
}

// runControllerMode starts the event loop and REST API.
func runControllerMode() {
	apiAddr := envOrDefault("INGRESS_API_ADDR", ":9090")

	d := driver.NewInProcess()
	ctrl := controller.New(d)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ctrl.Run(ctx)

	// Serve embedded SPA if the dist directory was bundled at build time.
	var staticFS fs.FS
	sub, err := fs.Sub(embeddedUI, "ui/dist")
	if err == nil {
		staticFS = sub
	}

	srv := &http.Server{
		Addr:    apiAddr,
		Handler: api.New(ctrl, staticFS),
	}

	go func() {
		slog.Info("API server listening", "addr", apiAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("API server error", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("=== cnak-ingress ready ===", "api", apiAddr, "ui", apiAddr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down...")
	cancel()
	srv.Shutdown(context.Background())
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
