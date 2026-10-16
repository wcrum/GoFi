package pipeline

import (
	"strings"
	"testing"

	"github.com/wcrum/gofi/spec"
)

func debugListener() spec.FlowDefinition {
	return spec.FlowDefinition{
		Name:    "dbg",
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "hot", Kind: "filter", Type: "cel", Expression: "fields.t > 30.0"},
			{ID: "tag", Kind: "transform", TransformOp: "set", Field: "route", ValueExpr: "'ops'"},
			{ID: "enrich", Kind: "python", Python: &spec.PythonRef{InlineSource: "def transform(fields, meta):\n    return fields\n"}},
		},
		Sinks: []spec.SinkCfg{{Type: "stdout"}, {Type: "stdout", When: "fields.t > 100.0", Python: &spec.PythonRef{Library: "lib", Function: "out"}}},
	}
}

func TestResolveDebugStep(t *testing.T) {
	def := debugListener()
	if step, kind, err := ResolveDebugStep(def, ""); err != nil || step != "enrich" || kind != "transformer" {
		t.Fatalf("first Python step: %q %q %v", step, kind, err)
	}
	if step, kind, err := ResolveDebugStep(def, "sink:1"); err != nil || step != "sink:1" || kind != "sink" {
		t.Fatalf("sink step: %q %q %v", step, kind, err)
	}
	if _, _, err := ResolveDebugStep(def, "hot"); err == nil || !strings.Contains(err.Error(), "enrich, sink:1") {
		t.Fatalf("non-Python step must be rejected with the choices: %v", err)
	}
	if _, _, err := ResolveDebugStep(spec.FlowDefinition{Name: "plain"}, ""); err == nil {
		t.Fatal("a listener without Python steps cannot be debugged")
	}
}

func TestSimulateToCapturesTheStepInput(t *testing.T) {
	result, target, notReached, err := SimulateTo(debugListener(), []byte(`{"t": 42.5, "id": "a"}`), "")
	if err != nil || notReached != "" || target == nil {
		t.Fatalf("SimulateTo = %v %q %+v", err, notReached, target)
	}
	if target.Step != "enrich" || target.Kind != "transformer" || target.Ref.InlineSource == "" || target.Ref.Function != "enrich" {
		t.Fatalf("target: %+v", target)
	}
	if target.Fields["t"] != 42.5 || target.Fields["route"] != "ops" || target.Fields["id"] != "a" {
		t.Fatalf("the input is the fields after earlier stages: %v", target.Fields)
	}
	if target.Meta["listener"] == "" && len(target.Meta) == 0 {
		t.Fatalf("meta captured: %v", target.Meta)
	}
	if string(target.Raw) != `{"t": 42.5, "id": "a"}` {
		t.Fatalf("raw: %q", target.Raw)
	}
	if len(result.Stages) != 2 || result.Stages[1].ID != "tag" {
		t.Fatalf("the simulation stops before the debugged step: %+v", result.Stages)
	}
}

func TestSimulateToReportsFramesThatNeverArrive(t *testing.T) {
	_, target, notReached, err := SimulateTo(debugListener(), []byte(`{"t": 10}`), "enrich")
	if err != nil || target != nil || !strings.Contains(notReached, "dropped by hot") {
		t.Fatalf("dropped by the CEL filter: %v %+v %q", err, target, notReached)
	}
	// With errorPolicy "continue" an unparseable frame still flows on; with
	// "drop" it never reaches the step.
	strict := debugListener()
	strict.Parser.ErrorPolicy = "drop"
	_, target, notReached, err = SimulateTo(strict, []byte(`not json`), "enrich")
	if err != nil || target != nil || !strings.Contains(notReached, "dropped by parser") {
		t.Fatalf("parse failure: %v %+v %q", err, target, notReached)
	}
}

func TestSimulateToParserReceivesDecodedPayload(t *testing.T) {
	def := spec.FlowDefinition{Name: "p", Parser: spec.ParserCfg{Format: "python", Decoders: []spec.CodecStep{{Type: "base64"}}, Python: &spec.PythonRef{Library: "lib", Function: "decode"}}}
	_, target, _, err := SimulateTo(def, []byte("aGVsbG8="), "parser")
	if err != nil || target == nil || string(target.Raw) != "hello" || target.Kind != "parser" || target.Ref.Function != "decode" {
		t.Fatalf("parser input: %v %+v", err, target)
	}
	_, target, notReached, err := SimulateTo(def, []byte("%%%"), "parser")
	if err != nil || target != nil || !strings.Contains(notReached, "decoder") {
		t.Fatalf("undecodable frame: %v %+v %q", err, target, notReached)
	}
}

func TestSimulateToSinkConditions(t *testing.T) {
	def := spec.FlowDefinition{Name: "s", Parser: spec.ParserCfg{Format: "json"},
		Sinks: []spec.SinkCfg{{Type: "stdout", When: "fields.t > 100.0", Python: &spec.PythonRef{Library: "lib", Function: "out"}}}}
	_, target, notReached, err := SimulateTo(def, []byte(`{"t": 5}`), "sink:0")
	if err != nil || target != nil || !strings.Contains(notReached, "condition") {
		t.Fatalf("unselected sink: %v %+v %q", err, target, notReached)
	}
	_, target, _, err = SimulateTo(def, []byte(`{"t": 500}`), "sink:0")
	if err != nil || target == nil || target.Kind != "sink" || target.Fields["t"] != 500.0 {
		t.Fatalf("selected sink: %v %+v", err, target)
	}
}
