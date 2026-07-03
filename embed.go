package main

import (
	"embed"
	"io/fs"
	"log/slog"
)

//go:embed ui/dist
var embeddedUIRaw embed.FS

// embeddedUI is the rooted filesystem for the built SPA.
// If the ui/dist directory wasn't built yet, it will be empty and the
// controller falls back to serving only the REST API.
var embeddedUI fs.FS = embeddedUIRaw

func init() {
	entries, err := fs.ReadDir(embeddedUIRaw, "ui/dist")
	if err != nil || len(entries) == 0 {
		slog.Warn("ui/dist not found or empty — serving API only (run 'npm run build' in ui/)")
		embeddedUI = embeddedUIRaw
	}
}
