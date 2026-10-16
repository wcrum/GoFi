package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/spec"
)

func inlineDefinition() spec.FlowDefinition {
	return spec.FlowDefinition{Name: "inline-demo", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 18555, Network: "tcp"}}, Framing: spec.Framing{Mode: "line"}, Parser: spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{{ID: "enrich", Kind: "python", Python: &spec.PythonRef{InlineSource: "def transform(fields, meta):\n    return fields\n"}}},
		Sinks:  []spec.SinkCfg{{Type: "stdout"}}}
}

func TestInlineStagesResolveToTheManagedLibrary(t *testing.T) {
	changed, validated := 0, 0
	SetInlineHooks(InlineHooks{Validate: func(context.Context, spec.FlowDefinition) error { validated++; return nil }, Changed: func() { changed++ }})
	defer SetInlineHooks(InlineHooks{})
	c := New(driver.NewInProcess())
	def := inlineDefinition()
	if err := c.Add(def); err != nil {
		t.Fatal(err)
	}
	if def.Stages[0].Python.Library != "" {
		t.Fatal("Add mutated the caller's definition")
	}
	stored, _ := c.Get(def.Name)
	ref := stored.Stages[0].Python
	if ref.Library != spec.ManagedLibraryName(def.Name) || ref.Function != "enrich" || ref.InlineSource == "" {
		t.Fatalf("resolved ref: %+v", ref)
	}
	if validated != 1 || changed == 0 {
		t.Fatalf("hooks: validated=%d changed=%d", validated, changed)
	}
	_ = c.Remove(def.Name)
}

func TestInlineValidationAndPlacementAreEnforced(t *testing.T) {
	SetInlineHooks(InlineHooks{Validate: func(context.Context, spec.FlowDefinition) error {
		return errors.New(`stage "enrich": line 2:5 F821`)
	}})
	defer SetInlineHooks(InlineHooks{})
	c := New(driver.NewInProcess())
	if err := c.Add(inlineDefinition()); err == nil || !strings.Contains(err.Error(), "F821") {
		t.Fatalf("invalid inline source accepted: %v", err)
	}
	SetInlineHooks(InlineHooks{})
	def := inlineDefinition()
	def.Stages[0].Python.InlineSource = "def other(fields):\n    return fields\n"
	if err := c.Add(def); err == nil || !strings.Contains(err.Error(), "def transform") {
		t.Fatalf("missing entry point accepted: %v", err)
	}
	def = inlineDefinition()
	def.Sinks = []spec.SinkCfg{{Type: "python", Python: &spec.PythonRef{InlineSource: "def transform(fields):\n    return fields\n"}}}
	if err := c.Add(def); err == nil || !strings.Contains(err.Error(), "only for processing stages") {
		t.Fatalf("inline sink accepted: %v", err)
	}
}
