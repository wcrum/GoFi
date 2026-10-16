package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/expressions"
)

// newExpressionLibrary selects the CEL library store: CelExpression CRDs in
// Kubernetes, otherwise expressions.yaml next to the listener resource file
// (GOFI_EXPRESSIONS_CONFIG_FILE overrides the path).
func newExpressionLibrary(ctx context.Context, driverKind string, listeners desired.Store) *expressions.Manager {
	var store expressions.Store
	if driverKind == "kubernetes" {
		k, err := expressions.NewKubernetesStore(k8sNamespace())
		if err != nil {
			slog.Error("CEL library initialization failed", "error", err)
			return nil
		}
		store = k
	} else {
		path := os.Getenv("GOFI_EXPRESSIONS_CONFIG_FILE")
		if path == "" {
			dir := ""
			if listeners != nil {
				dir = filepath.Dir(listeners.Configuration().Path)
			}
			if dir == "" || dir == "." {
				if config, err := os.UserConfigDir(); err == nil {
					dir = filepath.Join(config, "gofi")
				}
			}
			path = filepath.Join(dir, "expressions.yaml")
		}
		store = &expressions.FileStore{Path: path}
	}
	m := expressions.NewManager(store)
	if _, err := m.Snapshot(ctx); err != nil {
		slog.Error("CEL library load failed", "error", err)
	}
	return m
}
