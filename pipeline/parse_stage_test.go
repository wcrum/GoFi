package pipeline

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

func gzipped(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write([]byte(s))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// layeredFlow is the "Layered data" example: a text line carrying JSON whose
// "blob" field is base64-gzip JSON.
func layeredFlow(onError string) spec.FlowDefinition {
	return spec.FlowDefinition{
		Name: "layered", Framing: spec.Framing{Mode: "line"}, Parser: spec.ParserCfg{Format: "none"},
		Stages: []spec.StageSpec{
			{ID: "message", Kind: "parse", Parse: &spec.ParseStage{From: `fields["raw"]`, Format: "json"}},
			{ID: "blob", Kind: "parse", Parse: &spec.ParseStage{From: `fields["blob"]`, Decoders: []spec.CodecStep{{Type: "base64"}, {Type: "gzip"}}, Format: "json", Into: "payload", OnError: onError}},
			{ID: "admins", Kind: "filter", Type: "cel", Expression: `fields["payload"]["role"] == "admin"`},
		},
		Sinks:          []spec.SinkCfg{{Type: "stdout"}},
		DeadLetterSink: &spec.SinkCfg{Type: "stdout"},
	}
}

func TestParseStagesLiveSimulateMetricsAndBulletins(t *testing.T) {
	def := layeredFlow("continue")
	observability.ClearBulletins(def.Name)
	good := `{"host":"web-1","blob":"` + gzipped(t, `{"role":"admin"}`) + `"}`
	bad := `{"host":"web-2","blob":"not-base64!"}`
	chain, err := filter.NewChain(def.Stages)
	if err != nil {
		t.Fatal(err)
	}
	output, dead := &fakeSink{}, &fakeSink{}
	p, err := New(def, strings.NewReader(good+"\n"+bad+"\n"), []sink.Sink{output}, dead, chain, NewTap())
	if err != nil {
		t.Fatal(err)
	}
	p.Run(context.Background(), "test")
	msgs := output.received()
	if len(msgs) != 2 || msgs[0].Fields["host"] != "web-1" || msgs[0].Fields["payload"].(map[string]any)["role"] != "admin" {
		t.Fatalf("live output = %#v", msgs)
	}
	// The bad blob continued unchanged (the filter's CEL error fails open);
	// the parse error is a bulletin and a stage error metric labeled with
	// the stage ID.
	if msgs[1].Fields["host"] != "web-2" || msgs[1].Fields["payload"] != nil || len(dead.received()) != 0 {
		t.Fatalf("continued message = %#v", msgs[1].Fields)
	}
	found := false
	for _, b := range observability.Bulletins(def.Name) {
		if b.Component == observability.ComponentStage("blob") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no parse stage bulletin: %+v", observability.Bulletins(def.Name))
	}
	r := httptest.NewRecorder()
	observability.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	if want := `gofi_stage_errors_total{listener="layered",stage="blob"} 1`; !strings.Contains(r.Body.String(), want) {
		t.Fatalf("metric absent: %s", want)
	}

	result, err := Simulate(def, []byte(good))
	if err != nil || result.Dropped || len(result.Stages) != 3 {
		t.Fatalf("simulate = %+v %v", result, err)
	}
	if s := result.Stages[1]; s.Kind != "parse" || s.Type != "json" || !s.Pass || s.Fields["payload"] == nil {
		t.Fatalf("parse trace = %+v", s)
	}
	result, err = Simulate(layeredFlow("drop"), []byte(bad))
	if err != nil || !result.Dropped || result.DroppedBy != "blob" || result.Stages[1].Error == "" || !result.DeadLetter {
		t.Fatalf("drop simulate = %+v %v", result, err)
	}
}

func TestDebugStepsOfAParseStage(t *testing.T) {
	def := spec.FlowDefinition{
		Name: "dbg", Parser: spec.ParserCfg{Format: "json"}, Python: &spec.FlowPython{Library: "shared"},
		Stages: []spec.StageSpec{{ID: "inner", Kind: "parse", Parse: &spec.ParseStage{
			From: `fields["b"]`, Decoders: []spec.CodecStep{{Type: "base64"}, {Type: "python", Python: &spec.PythonRef{Function: "unwrap"}}},
			Format: "python", Python: &spec.PythonRef{Function: "parse_inner"}, Into: "inner",
		}}},
	}
	step, kind, err := ResolveDebugStep(def, "")
	if err != nil || step != "inner/decoder:1" || kind != "decoder" {
		t.Fatalf("first step %q %q %v", step, kind, err)
	}
	raw := []byte(`{"b":"` + base64.StdEncoding.EncodeToString([]byte("payload")) + `","x":1}`)
	_, target, _, err := SimulateTo(def, raw, "inner/decoder:1")
	if err != nil || target == nil || string(target.Raw) != "payload" || target.Kind != "decoder" || target.Ref.Library != "shared" {
		t.Fatalf("decoder target %+v %v", target, err)
	}
	// The parser's input runs every decoder; the Python decoder cannot run
	// here, so the simulation reports the failure on the stage.
	result, target, notReached, err := SimulateTo(def, raw, "inner/parser")
	if err != nil || target != nil || notReached == "" || len(result.Stages) != 1 || result.Stages[0].Error == "" {
		t.Fatalf("parser target %+v %+v %q %v", result, target, notReached, err)
	}
	def.Stages[0].Parse.Decoders = def.Stages[0].Parse.Decoders[:1]
	_, target, _, err = SimulateTo(def, raw, "inner/parser")
	if err != nil || target == nil || string(target.Raw) != "payload" || target.Kind != "parser" || len(target.Fields) != 0 || target.Ref.Function != "parse_inner" || target.Ref.Library != "shared" {
		t.Fatalf("parser target %+v %v", target, err)
	}
}

// A flow saved before the decode step (parser.decoders) simulates the same.
func TestLegacyParserDecodersSimulate(t *testing.T) {
	def := spec.FlowDefinition{Name: "legacy", Parser: spec.ParserCfg{Format: "json", LegacyDecoders: []spec.CodecStep{{Type: "base64"}}}}
	result, err := Simulate(def, []byte(base64.StdEncoding.EncodeToString([]byte(`{"a":"b"}`))))
	if err != nil || result.ParseError != "" || result.ParsedFields["a"] != "b" {
		t.Fatalf("%+v %v", result, err)
	}
	def.Decode = &spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: "hex"}}}
	if _, err := Simulate(def, nil); err == nil {
		t.Fatal("conflicting decode locations accepted")
	}
}
