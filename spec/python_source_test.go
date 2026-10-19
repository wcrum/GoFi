package spec

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPythonSourceValidation(t *testing.T) {
	ok := FlowSource{Python: &PythonSource{Library: "radar", Function: "poll", Every: "10s", Config: map[string]any{"limit": 5}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok.Kind() != "python" {
		t.Fatalf("kind = %q", ok.Kind())
	}
	for name, src := range map[string]FlowSource{
		"no library":  {Python: &PythonSource{Function: "poll"}},
		"no function": {Python: &PythonSource{Library: "radar"}},
		"bad every":   {Python: &PythonSource{Library: "radar", Function: "poll", Every: "soon"}},
		"short every": {Python: &PythonSource{Library: "radar", Function: "poll", Every: "500ms"}},
		"huge config": {Python: &PythonSource{Library: "radar", Function: "poll", Config: map[string]any{"x": strings.Repeat("a", MaxPythonSourceConfigBytes)}}},
		"two sources": {Python: &PythonSource{Library: "radar", Function: "poll"}, Listen: &ListenSource{Port: 9000}},
	} {
		if err := src.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPythonSourceJSONAndRefs(t *testing.T) {
	var src FlowSource
	if err := json.Unmarshal([]byte(`{"python":{"library":"radar","function":"poll","config":{"limit":3},"every":"5s"}}`), &src); err != nil {
		t.Fatal(err)
	}
	def := FlowDefinition{Name: "f", Source: src}
	uses := def.PythonRefs()
	if len(uses) != 1 || uses[0].Path != "source.python" || uses[0].Kind != "source" || uses[0].Ref.Library != "radar" || uses[0].Ref.Function != "poll" {
		t.Fatalf("uses = %+v", uses)
	}
	if !def.UsesPython() {
		t.Fatal("a Python source must make the runtime resolve worker bindings")
	}
	if err := json.Unmarshal([]byte(`{"python":{"library":"radar","function":"poll","script":"x"}}`), &src); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestPythonSourcePollEvery(t *testing.T) {
	p := &PythonSource{}
	if got := p.PollEvery(""); got != DefaultFetchEvery {
		t.Fatalf("default = %v", got)
	}
	if got := p.PollEvery("10s"); got != 10*time.Second {
		t.Fatalf("decorator = %v", got)
	}
	p.Every = "2m"
	if got := p.PollEvery("10s"); got != 2*time.Minute {
		t.Fatalf("override = %v", got)
	}
	if err := CheckSourceKind("radar", "poll", "transformer"); err == nil {
		t.Fatal("a transformer cannot be a source")
	}
	for _, kind := range SourceFunctionKinds {
		if err := CheckSourceKind("radar", "poll", kind); err != nil {
			t.Fatal(err)
		}
	}
}
