package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/wcrum/gofi/spec"
)

func defaultLibraryDef() spec.FlowDefinition {
	def := controllerDef()
	def.Python = &spec.FlowPython{Library: "shared"}
	def.Parser = spec.ParserCfg{Format: "python", Python: &spec.PythonRef{Function: "parse_syslog"}}
	def.Stages = []spec.StageSpec{
		{ID: "inner", Kind: "parse", Parse: &spec.ParseStage{From: `fields["message"]`, Format: "python", Python: &spec.PythonRef{Function: "parse_inner"}, Into: "inner"}},
		{ID: "pinned", Kind: "python", Python: &spec.PythonRef{Library: "other", Function: "enrich"}},
		{ID: "inline", Kind: "python", Python: &spec.PythonRef{InlineSource: "def transform(fields, meta=None):\n    return fields\n"}},
	}
	def.Sinks = []spec.SinkCfg{{Type: "stdout"}}
	return def
}

// The stored definition keeps its authored form (no library on refs that use
// the default); runtimes receive explicit libraries; inline stages still
// resolve to the flow's managed library.
func TestDefaultLibraryResolution(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{definitions: map[string]spec.FlowDefinition{}}
	c := New(d)
	def := defaultLibraryDef()
	if err := c.Add(def); err != nil {
		t.Fatal(err)
	}
	stored, _ := c.Get(def.Name)
	if stored.Parser.Python.Library != "" || stored.Stages[0].Parse.Python.Library != "" {
		t.Fatalf("stored definition was rewritten: %+v", stored.Parser.Python)
	}
	if ref := stored.Stages[2].Python; ref.Library != spec.ManagedLibraryName(def.Name) || ref.Function != "inline" {
		t.Fatalf("inline ref = %+v", ref)
	}
	c.sync(ctx)
	running, ok := d.definitions[def.Name]
	if !ok {
		t.Fatal("flow not created")
	}
	if running.Parser.Python.Library != "shared" || running.Stages[0].Parse.Python.Library != "shared" || running.Stages[1].Python.Library != "other" {
		t.Fatalf("runtime refs: parser %+v parse %+v pinned %+v", running.Parser.Python, running.Stages[0].Parse.Python, running.Stages[1].Python)
	}
	if running.Stages[2].Python.Library != spec.ManagedLibraryName(def.Name) {
		t.Fatalf("runtime inline ref = %+v", running.Stages[2].Python)
	}
	// An unchanged desired state is not drift.
	c.sync(ctx)
	if d.creates != 1 || d.deletes != 0 {
		t.Fatalf("resolved runtime looked like drift: creates=%d deletes=%d", d.creates, d.deletes)
	}
}

func TestValidationOfDefaultLibraryAndParseStages(t *testing.T) {
	for name, mutate := range map[string]func(*spec.FlowDefinition){
		"no default":             func(d *spec.FlowDefinition) { d.Python = nil },
		"empty default":          func(d *spec.FlowDefinition) { d.Python = &spec.FlowPython{} },
		"parse block wrong kind": func(d *spec.FlowDefinition) { d.Stages[1].Parse = &spec.ParseStage{Format: "json"} },
		"parse without block":    func(d *spec.FlowDefinition) { d.Stages[0].Parse = nil },
		"replace with into":      func(d *spec.FlowDefinition) { d.Stages[0].Parse.Mode = "replace" },
		"conflicting decode": func(d *spec.FlowDefinition) {
			d.Decode = &spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: "base64"}}}
			d.Parser.LegacyDecoders = []spec.CodecStep{{Type: "hex"}}
		},
		"bad decode": func(d *spec.FlowDefinition) { d.Decode = &spec.DecodeCfg{MaxDecodedBytes: -1} },
	} {
		def := defaultLibraryDef()
		mutate(&def)
		err := ValidateDefinition(def)
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if strings.HasPrefix(name, "no default") || name == "empty default" {
			if !strings.Contains(err.Error(), "python.library is required") {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}
	if err := ValidateDefinition(defaultLibraryDef()); err != nil {
		t.Fatal(err)
	}
	legacy := controllerDef()
	legacy.Parser.LegacyDecoders = []spec.CodecStep{{Type: "base64"}}
	if err := ValidateDefinition(legacy); err != nil {
		t.Fatalf("legacy parser.decoders rejected: %v", err)
	}
}

// A flow may reference only its own GoFi-managed inline library, never
// another flow's generated library or a scratch run.
func TestManagedLibrariesOfOtherFlowsCannotBeReferenced(t *testing.T) {
	def := defaultLibraryDef()
	def.Python.Library = spec.ManagedLibraryName("other-flow")
	if err := ValidateDefinition(def); err == nil || !strings.Contains(err.Error(), "managed by GoFi") {
		t.Fatalf("default pointing at another flow's library accepted: %v", err)
	}
	def = defaultLibraryDef()
	def.Stages[1].Python.Library = "run-abc123"
	if err := ValidateDefinition(def); err == nil || !strings.Contains(err.Error(), "managed by GoFi") {
		t.Fatalf("scratch library reference accepted: %v", err)
	}
	// Its own inline library (as ResolveInlinePython fills it in) is fine.
	def = defaultLibraryDef()
	def.ResolveInlinePython()
	if err := ValidateDefinition(def); err != nil {
		t.Fatal(err)
	}
}
