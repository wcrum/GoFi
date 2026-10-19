package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wcrum/gofi/config"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/history"
)

// historySyncInterval is how often the recorder looks for flow changes made
// outside GoFi; the controller itself reloads its store every 30 seconds.
const historySyncInterval = 5 * time.Second

// startHistory connects flow revision history (docs/versioning.md) and
// starts recording. It returns nil when history is disabled
// (GOFI_HISTORY=off) or its storage is unavailable; saves work without it.
func startHistory(ctx context.Context, driverKind string, store desired.Store, ctrl *controller.Controller) *history.Recorder {
	if v := strings.ToLower(os.Getenv("GOFI_HISTORY")); v == "off" || v == "false" || v == "0" {
		slog.Info("flow history disabled")
		return nil
	}
	var backend history.Store
	if driverKind == "kubernetes" {
		k, err := history.NewKubernetes(k8sNamespace())
		if err != nil {
			slog.Warn("flow history unavailable", "error", err)
			return nil
		}
		backend = k
	} else {
		dir := os.Getenv("GOFI_HISTORY_DIR")
		if dir == "" {
			if file, ok := store.(*desired.File); ok {
				dir = filepath.Join(filepath.Dir(file.Path), "history")
			}
		}
		if dir == "" {
			slog.Warn("flow history unavailable: set GOFI_HISTORY_DIR")
			return nil
		}
		backend = &history.File{Dir: dir}
	}
	// GOFI_HISTORY_LIMIT / GOFI_HISTORY_DELETED_RETENTION are parsed and
	// validated by the config package; the edge profile keeps both small so
	// flash sees fewer revision files.
	limits := config.Get().Limits
	opts := history.Options{Limit: limits.HistoryLimit, DeletedRetention: limits.HistoryDeletedRetention}
	rec := history.NewRecorder(backend, ctrl, opts)
	// Record (import) the loaded flows before the API serves saves, so an
	// early save is not the first revision of an existing flow.
	if err := rec.Sync(ctx); err != nil {
		slog.Warn("flow history: initial sync failed", "store", backend.Describe(), "error", err)
	}
	go rec.Run(ctx, historySyncInterval)
	slog.Info("flow history recording", "store", backend.Describe(), "limit", rec.Limit())
	return rec
}
