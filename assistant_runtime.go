package main

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wcrum/gofi/assistant"
	"github.com/wcrum/gofi/desired"
)

// assistantDocs are the user docs GoFiBot can read to answer questions, so
// its answers follow this build's documentation rather than the model's
// memory of it.
//
//go:embed docs/*.md
var assistantDocs embed.FS

// newAssistant selects the configuration store: a Secret in Kubernetes,
// otherwise assistant.yaml next to the flow file (GOFI_ASSISTANT_CONFIG_FILE
// overrides the path). The API key may instead come from the environment
// (GOFI_ASSISTANT_API_KEY or GOFI_ASSISTANT_API_KEY_FILE).
func newAssistant(ctx context.Context, driverKind string, listeners desired.Store) *assistant.Manager {
	var store assistant.Store
	if driverKind == "kubernetes" {
		k, err := assistant.NewKubernetesStore(k8sNamespace())
		if err != nil {
			slog.Error("assistant configuration initialization failed", "error", err)
			return nil
		}
		store = k
	} else {
		path := os.Getenv("GOFI_ASSISTANT_CONFIG_FILE")
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
			path = filepath.Join(dir, "assistant.yaml")
		}
		store = &assistant.FileStore{Path: path}
	}
	m := assistant.NewManager(store)
	if _, err := m.Get(ctx); err != nil {
		slog.Error("assistant configuration load failed", "error", err)
	}
	return m
}

// assistantDocsFS is the docs directory rooted at docs/.
func assistantDocsFS() fs.FS {
	sub, err := fs.Sub(assistantDocs, "docs")
	if err != nil {
		return nil
	}
	return sub
}
