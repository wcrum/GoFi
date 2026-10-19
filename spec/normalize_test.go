package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeMovesLegacyParserDecodersToDecode(t *testing.T) {
	var def FlowDefinition
	legacy := `{"name":"f","parser":{"format":"json","decoders":["base64",{"type":"gzip"}],"maxDecodedBytes":2048,"errorPolicy":"drop"}}`
	if err := json.Unmarshal([]byte(legacy), &def); err != nil {
		t.Fatal(err)
	}
	if err := def.Normalize(); err != nil {
		t.Fatal(err)
	}
	if def.Decode == nil || len(def.Decode.Decoders) != 2 || def.Decode.Decoders[1].Type != "gzip" || def.Decode.MaxDecodedBytes != 2048 {
		t.Fatalf("decode = %+v", def.Decode)
	}
	if def.Parser.LegacyDecoders != nil || def.Parser.LegacyMaxDecodedBytes != 0 || def.Parser.ErrorPolicy != "drop" {
		t.Fatalf("parser = %+v", def.Parser)
	}
	data, _ := json.Marshal(def)
	if strings.Contains(string(data), `"parser":{"format":"json","decoders"`) || !strings.Contains(string(data), `"decode":{"decoders":[{"type":"base64"},{"type":"gzip"}],"maxDecodedBytes":2048}`) {
		t.Fatalf("written shape: %s", data)
	}
	again := def
	if err := again.Normalize(); err != nil || again.Decode.MaxDecodedBytes != 2048 {
		t.Fatalf("not idempotent: %+v %v", again.Decode, err)
	}
}

func TestNormalizeAcceptsAgreeingAndRejectsConflictingLocations(t *testing.T) {
	steps := []CodecStep{{Type: "base64"}}
	same := FlowDefinition{Decode: &DecodeCfg{Decoders: steps}, Parser: ParserCfg{LegacyDecoders: []CodecStep{{Type: "base64"}}, LegacyMaxDecodedBytes: 99}}
	if err := same.Normalize(); err != nil || same.Decode.MaxDecodedBytes != 99 || same.Parser.LegacyDecoders != nil {
		t.Fatalf("agreeing: %+v %v", same, err)
	}
	for name, def := range map[string]FlowDefinition{
		"decoders": {Decode: &DecodeCfg{Decoders: steps}, Parser: ParserCfg{LegacyDecoders: []CodecStep{{Type: "hex"}}}},
		"max":      {Decode: &DecodeCfg{MaxDecodedBytes: 1}, Parser: ParserCfg{LegacyMaxDecodedBytes: 2}},
	} {
		if err := def.Normalize(); err == nil || !strings.Contains(err.Error(), "deprecated") {
			t.Fatalf("%s: conflict accepted: %v", name, err)
		}
	}
	empty := FlowDefinition{Decode: &DecodeCfg{}, Python: &FlowPython{}}
	if err := empty.Normalize(); err != nil || empty.Decode != nil || empty.Python != nil {
		t.Fatalf("empty blocks should be dropped: %+v", empty)
	}
}

func TestWithDefaultLibraryFillsOnlyLibraryLessFunctionRefs(t *testing.T) {
	explicit := &PythonRef{Library: "other", Function: "send"}
	def := FlowDefinition{
		Name:    "f",
		Python:  &FlowPython{Library: "shared"},
		Framing: Framing{Mode: "python", Python: &PythonRef{Function: "split"}},
		Decode:  &DecodeCfg{Decoders: []CodecStep{{Type: "python", Python: &PythonRef{Function: "unwrap"}}}},
		Parser:  ParserCfg{Format: "python", Python: &PythonRef{Function: "parse"}},
		Stages: []StageSpec{
			{ID: "inline", Kind: "python", Python: &PythonRef{InlineSource: "def transform(fields):\n    return fields\n"}},
			{ID: "inner", Kind: "parse", Parse: &ParseStage{Format: "python", Python: &PythonRef{Function: "inner"}, Decoders: []CodecStep{{Type: "python", Python: &PythonRef{Function: "dec"}}}}},
		},
		Sinks:          []SinkCfg{{Type: "python", Python: explicit, Encoding: &Encoding{Encoders: []CodecStep{{Type: "python", Python: &PythonRef{Function: "enc"}}}}}},
		DeadLetterSink: &SinkCfg{Type: "python", Python: &PythonRef{Function: "dead"}},
	}
	out := def.WithDefaultLibrary()
	if def.Parser.Python.Library != "" || def.Stages[1].Parse.Python.Library != "" {
		t.Fatal("WithDefaultLibrary wrote through to the caller's definition")
	}
	paths := map[string]string{}
	for _, use := range out.PythonRefs() {
		paths[use.Path] = use.Ref.Library
	}
	want := map[string]string{
		"framing.python":                       "shared",
		"decode.decoders[0].python":            "shared",
		"parser.python":                        "shared",
		"stages[0].python":                     "", // inline source keeps its managed library
		"stages[1].parse.decoders[0].python":   "shared",
		"stages[1].parse.python":               "shared",
		"sinks[0].encoding.encoders[0].python": "shared",
		"sinks[0].python":                      "other",
		"deadLetterSink.python":                "shared",
	}
	for path, lib := range want {
		if got, ok := paths[path]; !ok || got != lib {
			t.Fatalf("%s: library %q (present %v), want %q; all %v", path, got, ok, lib, paths)
		}
	}
	if len(paths) != len(want) {
		t.Fatalf("unexpected refs %v", paths)
	}
	// Inline stages still resolve to the flow's managed library.
	out.ResolveInlinePython()
	if out.Stages[0].Python.Library != ManagedLibraryName("f") || out.Stages[0].Python.Function != "inline" {
		t.Fatalf("inline ref = %+v", out.Stages[0].Python)
	}
	if got := len(out.InlineStages()); got != 1 {
		t.Fatalf("inline stages = %d", got)
	}
}

func TestPythonRefWithoutLibraryNeedsADefault(t *testing.T) {
	ref := &PythonRef{Function: "parse"}
	if err := ref.Validate(); err == nil || !strings.Contains(err.Error(), "python.library") {
		t.Fatalf("library-less ref accepted: %v", err)
	}
	def := FlowDefinition{Parser: ParserCfg{Format: "python", Python: ref}}
	if def.WithDefaultLibrary().Parser.Python.Library != "" {
		t.Fatal("no default must leave the library empty")
	}
}
