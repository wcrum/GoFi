package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/spec"
)

func newTestHandler() (http.Handler, *controller.Controller) {
	d := driver.NewInProcess()
	ctrl := controller.New(d)
	h := New(ctrl, nil, deploymode.Mode("binary"))
	return h, ctrl
}

func postListener(t *testing.T, h http.Handler, def spec.ListenerDefinition) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal def: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/listeners", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func postSimulate(t *testing.T, h http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/simulate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func simulateBody(t *testing.T, def spec.ListenerDefinition, raw string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"listener": def, "raw": raw})
	if err != nil {
		t.Fatalf("marshal simulate request: %v", err)
	}
	return body
}

func TestSimulate_PassingDraft(t *testing.T) {
	h, ctrl := newTestHandler()
	def := spec.ListenerDefinition{
		Name:    "sim-pass",
		Source:  spec.Source{Port: 19301, Network: "tcp"},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "f1", Kind: "filter", Type: "cel", Expression: `fields["type"] == "a"`},
		},
		Sinks: []spec.SinkCfg{{Type: "stdout"}},
	}

	rec := postSimulate(t, h, simulateBody(t, def, `{"type":"a"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var res pipeline.SimulateResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if res.Dropped {
		t.Fatalf("dropped = true, want false; droppedBy=%q", res.DroppedBy)
	}
	if len(res.Stages) != 1 || !res.Stages[0].Pass {
		t.Fatalf("stages = %#v, want one passing stage", res.Stages)
	}
	if len(res.Sinks) != 1 || !res.Sinks[0].Sent {
		t.Fatalf("sinks = %#v, want one sent sink", res.Sinks)
	}

	// No side effects: the endpoint must not create a listener.
	if len(ctrl.List()) != 0 {
		t.Fatalf("ctrl.List() = %d entries, want 0 (simulate must have no side effects)", len(ctrl.List()))
	}
	if _, ok := ctrl.Get("sim-pass"); ok {
		t.Fatal("simulate must not register a listener")
	}
}

func TestSimulate_DroppedDraft(t *testing.T) {
	h, _ := newTestHandler()
	def := spec.ListenerDefinition{
		Name:    "sim-drop",
		Source:  spec.Source{Port: 19302, Network: "tcp"},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "drop-me", Kind: "filter", Type: "cel", Expression: `fields["type"] == "a"`},
		},
		Sinks: []spec.SinkCfg{{Type: "stdout"}},
	}

	rec := postSimulate(t, h, simulateBody(t, def, `{"type":"b"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var res pipeline.SimulateResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !res.Dropped || res.DroppedBy != "drop-me" {
		t.Fatalf("dropped=%v droppedBy=%q, want true/drop-me", res.Dropped, res.DroppedBy)
	}
}

func TestSimulate_DuplicateStageIDs_BadRequest(t *testing.T) {
	h, _ := newTestHandler()
	def := spec.ListenerDefinition{
		Name:   "sim-dup",
		Parser: spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "f1", Kind: "filter", Type: "cel", Expression: `true`},
			{ID: "f1", Kind: "filter", Type: "cel", Expression: `false`},
		},
	}
	rec := postSimulate(t, h, simulateBody(t, def, `{"a":"x"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestSimulate_MalformedBody_BadRequest(t *testing.T) {
	h, _ := newTestHandler()
	rec := postSimulate(t, h, []byte(`{not valid json`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestSimulate_WorksWithoutPriorCreate(t *testing.T) {
	h, ctrl := newTestHandler()
	if len(ctrl.List()) != 0 {
		t.Fatalf("precondition: ctrl.List() = %d, want 0", len(ctrl.List()))
	}
	def := spec.ListenerDefinition{
		Name:   "never-created",
		Parser: spec.ParserCfg{Format: "json"},
		Sinks:  []spec.SinkCfg{{Type: "stdout"}},
	}
	rec := postSimulate(t, h, simulateBody(t, def, `{"a":"x"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(ctrl.List()) != 0 {
		t.Fatalf("ctrl.List() = %d after simulate, want 0", len(ctrl.List()))
	}
}

func TestCreateListener_DuplicateStageIDs_Rejected(t *testing.T) {
	h, ctrl := newTestHandler()
	def := spec.ListenerDefinition{
		Name:    "dup-ids",
		Source:  spec.Source{Port: 19201, Network: "tcp"},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "f1", Kind: "filter", Type: "cel", Expression: "true"},
			{ID: "f1", Kind: "filter", Type: "cel", Expression: "false"},
		},
		Sinks: []spec.SinkCfg{{Type: "stdout"}},
	}

	rec := postListener(t, h, def)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if _, ok := ctrl.Get("dup-ids"); ok {
		t.Fatal("listener with duplicate stage IDs should not have been added")
	}
}

func TestCreateListener_UnknownFilterType_Rejected(t *testing.T) {
	h, ctrl := newTestHandler()
	def := spec.ListenerDefinition{
		Name:    "bad-type",
		Source:  spec.Source{Port: 19202, Network: "tcp"},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "f1", Kind: "filter", Type: "nope"},
		},
		Sinks: []spec.SinkCfg{{Type: "stdout"}},
	}

	rec := postListener(t, h, def)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if _, ok := ctrl.Get("bad-type"); ok {
		t.Fatal("listener with unknown filter type should not have been added")
	}
}

func TestCreateListener_WellFormedChain_SucceedsAndRoundTrips(t *testing.T) {
	h, ctrl := newTestHandler()
	def := spec.ListenerDefinition{
		Name:    "good-chain",
		Source:  spec.Source{Port: 19203, Network: "tcp"},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "f1", Kind: "filter", Type: "cel", Expression: `fields["type"] == "a"`},
			{ID: "f2", Kind: "filter", Type: "cel", Expression: `double(fields["val"]) > 10.0`},
		},
		Sinks: []spec.SinkCfg{{Type: "stdout"}},
	}

	rec := postListener(t, h, def)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	var created spec.ListenerDefinition
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(created.Stages) != 2 || created.Stages[0].ID != "f1" || created.Stages[1].ID != "f2" {
		t.Fatalf("response stages mismatch: %#v", created.Stages)
	}

	got, ok := ctrl.Get("good-chain")
	if !ok {
		t.Fatal("listener was not added to the controller")
	}
	if len(got.Stages) != 2 || got.Stages[0].Expression != def.Stages[0].Expression || got.Stages[1].Expression != def.Stages[1].Expression {
		t.Fatalf("stored def stages mismatch: %#v", got.Stages)
	}

	// GET should echo the same stages array.
	req := httptest.NewRequest(http.MethodGet, "/api/listeners/good-chain", nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, req)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body = %s", getRec.Code, getRec.Body.String())
	}
	var fetched spec.ListenerDefinition
	if err := json.Unmarshal(getRec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("unmarshal GET response: %v", err)
	}
	if len(fetched.Stages) != 2 || fetched.Stages[0].ID != "f1" || fetched.Stages[1].ID != "f2" {
		t.Fatalf("GET response stages mismatch: %#v", fetched.Stages)
	}
}
