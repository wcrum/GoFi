package history

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/spec"
)

func legacyDecoded(name string) spec.FlowDefinition {
	def := flowDef(name, 9000)
	def.Parser.LegacyDecoders = []spec.CodecStep{{Type: "base64"}}
	def.Parser.LegacyMaxDecodedBytes = 4096
	return def
}

// A flow recorded before the decode step existed (parser.decoders) and the
// same flow in the current shape are one definition: same hash, and the
// recorder never records the conversion as a change.
func TestLegacyDecodeShapeIsNotARevision(t *testing.T) {
	legacy := legacyDecoded("route")
	current := legacy
	if err := current.Normalize(); err != nil {
		t.Fatal(err)
	}
	if Hash(legacy) != Hash(current) {
		t.Fatal("legacy and current shapes hash differently")
	}
	if norm := Normalize(legacy); norm.Decode == nil || norm.Parser.LegacyDecoders != nil {
		t.Fatalf("history stores the legacy shape: %+v", norm)
	}
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			// Revision 1 as an older GoFi wrote it: legacy definition, and a
			// hash computed over the legacy JSON.
			stored := legacyDecoded("route")
			if err := store.Append(ctx, Revision{Flow: "route", Number: 1, CreatedAt: time.Now(), Source: SourceImport, Author: UnknownAuthor, Hash: "0123456789abcdef-hash-of-the-legacy-shape", Definition: &stored}); err != nil {
				t.Fatal(err)
			}
			flows := &memoryFlows{defs: map[string]spec.FlowDefinition{"route": current}}
			r := NewRecorder(store, flows, Options{})
			if err := r.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			if revs, _ := r.List(ctx, "route", 0); len(revs) != 1 {
				t.Fatalf("spurious revision for an equivalent definition: %+v", revs)
			}
			rev, err := r.Get(ctx, "route", 1)
			if err != nil || rev.Definition == nil {
				t.Fatal(err)
			}
		})
	}
}

// A legacy desired file loads, saves in the current shape, and records one
// import revision.
func TestLegacyDesiredFileThroughController(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := &desired.File{Path: filepath.Join(dir, "flows.yaml")}
	ctrl := controller.New(&fakeDriver{defs: map[string]spec.FlowDefinition{}})
	if err := file.Save(ctx, legacyDecoded("route")); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.WithStore(ctx, file); err != nil {
		t.Fatal(err)
	}
	def, ok := ctrl.Get("route")
	if !ok || def.Decode == nil || def.Decode.MaxDecodedBytes != 4096 || def.Parser.LegacyDecoders != nil {
		t.Fatalf("controller definition = %+v", def)
	}
	r := NewRecorder(&File{Dir: filepath.Join(dir, "history")}, ctrl, Options{})
	if err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Add(def); err != nil { // a save writes the current shape
		t.Fatal(err)
	}
	if err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if revs, _ := r.List(ctx, "route", 0); len(revs) != 1 {
		t.Fatalf("revisions = %+v", revs)
	}
	loaded, err := file.Load(ctx)
	if err != nil || len(loaded) != 1 || loaded[0].Decode == nil || loaded[0].Parser.LegacyDecoders != nil {
		t.Fatalf("file after save = %+v %v", loaded, err)
	}
}

// Starting and stopping a flow is not a revision, and a rollback keeps the
// flow's current run state.
func TestRunStateIsNotVersioned(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ctrl := controller.New(&fakeDriver{defs: map[string]spec.FlowDefinition{}})
	if err := ctrl.WithStore(ctx, &desired.File{Path: filepath.Join(dir, "flows.yaml")}); err != nil {
		t.Fatal(err)
	}
	r := NewRecorder(&File{Dir: filepath.Join(dir, "history")}, ctrl, Options{})
	for _, port := range []int{9000, 9100} {
		if _, created, err := r.Apply(ctx, "route", Meta{Source: SourcePortal}, func() error { return ctrl.Add(flowDef("route", port)) }); err != nil || !created {
			t.Fatalf("save %d: %v %v", port, created, err)
		}
	}
	if err := ctrl.SetState("route", spec.StateStopped); err != nil {
		t.Fatal(err)
	}
	if err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if revs, _ := r.List(ctx, "route", 0); len(revs) != 2 {
		t.Fatalf("stop recorded a revision: %+v", revs)
	}
	rev, created, err := r.Rollback(ctx, "route", 1, Meta{}, func(def spec.FlowDefinition) error { return ctrl.Add(def) })
	if err != nil || !created || rev.Number != 3 || rev.Definition != nil {
		t.Fatalf("rollback: %+v %v %v", rev, created, err)
	}
	def, _ := ctrl.Get("route")
	if def.Source.Listen.Port != 9000 || def.State != spec.StateStopped {
		t.Fatalf("rollback changed run state: %+v", def)
	}
	full, _ := r.Get(ctx, "route", 3)
	if full.Definition == nil || full.Definition.State != "" {
		t.Fatalf("revision stores run state: %+v", full.Definition)
	}
}
