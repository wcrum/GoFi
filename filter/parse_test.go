package filter

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/functionrpc"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

func gzipBase64(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func parseStageOf(t *testing.T, p spec.ParseStage) Stage {
	t.Helper()
	st, err := New(spec.StageSpec{ID: "inner", Kind: "parse", Parse: &p})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestParseStageLayersJSONFromRawAndFields(t *testing.T) {
	blob := gzipBase64(t, `{"user":"ana","roles":["admin"]}`)
	msg := message.New([]byte(`{"host":"web-1","blob":"`+blob+`"}`), "f", "test")
	msg.Fields["existing"] = "kept"

	// From the raw frame, merged into the top-level fields.
	if pass, err := parseStageOf(t, spec.ParseStage{Format: "json"}).Process(msg); !pass || err != nil {
		t.Fatalf("raw parse: %v %v", pass, err)
	}
	if msg.Fields["host"] != "web-1" || msg.Fields["existing"] != "kept" {
		t.Fatalf("merged fields = %v", msg.Fields)
	}
	// From a field, through decoders, into a nested map.
	nested := spec.ParseStage{From: `fields["blob"]`, Decoders: []spec.CodecStep{{Type: "base64"}, {Type: "gzip"}}, Format: "json", Into: "payload"}
	if pass, err := parseStageOf(t, nested).Process(msg); !pass || err != nil {
		t.Fatalf("field parse: %v %v", pass, err)
	}
	payload, ok := msg.Fields["payload"].(map[string]any)
	if !ok || payload["user"] != "ana" {
		t.Fatalf("payload = %#v", msg.Fields["payload"])
	}
	if !strings.HasPrefix(string(msg.Raw), `{"host"`) {
		t.Fatal("parse stage modified the raw frame")
	}
	// Merging into an existing map keeps its other keys; the parsed result is
	// reachable from CEL.
	msg.Fields["payload"] = map[string]any{"source": "earlier"}
	if _, err := parseStageOf(t, nested).Process(msg); err != nil {
		t.Fatal(err)
	}
	payload = msg.Fields["payload"].(map[string]any)
	if payload["source"] != "earlier" || payload["user"] != "ana" {
		t.Fatalf("merged payload = %v", payload)
	}
	filter, err := newCEL(`fields["payload"]["user"] == "ana"`)
	if err != nil {
		t.Fatal(err)
	}
	if pass, err := filter.Process(msg); !pass || err != nil {
		t.Fatalf("CEL over parsed payload: %v %v", pass, err)
	}
}

func TestParseStageReplaceXMLAndText(t *testing.T) {
	msg := message.New([]byte("ignored"), "f", "test")
	msg.Fields["doc"] = `<event id="7"><kind>login</kind></event>`
	msg.Fields["old"] = true
	if _, err := parseStageOf(t, spec.ParseStage{From: `fields["doc"]`, Format: "xml", Mode: "replace"}).Process(msg); err != nil {
		t.Fatal(err)
	}
	if msg.Fields["event.id"] != "7" || msg.Fields["event.kind"] != "login" || msg.Fields["old"] != nil {
		t.Fatalf("replaced fields = %v", msg.Fields)
	}
	msg.Fields["encoded"] = base64.StdEncoding.EncodeToString([]byte("plain text"))
	if _, err := parseStageOf(t, spec.ParseStage{From: `fields["encoded"]`, Decoders: []spec.CodecStep{{Type: "base64"}}, Format: "text", Into: "note"}).Process(msg); err != nil {
		t.Fatal(err)
	}
	if msg.Fields["note"] != "plain text" {
		t.Fatalf("text = %#v", msg.Fields["note"])
	}
	// "raw" names the raw frame, not a field.
	if _, err := parseStageOf(t, spec.ParseStage{From: "raw", Format: "text", Into: "frame"}).Process(msg); err != nil || msg.Fields["frame"] != "ignored" {
		t.Fatalf("raw text = %#v %v", msg.Fields["frame"], err)
	}
}

func TestParseStageOnError(t *testing.T) {
	for _, tc := range []struct {
		onError string
		pass    bool
	}{{"", true}, {"continue", true}, {"drop", false}} {
		msg := message.New([]byte("not json"), "f", "test")
		msg.Fields["a"] = "b"
		pass, err := parseStageOf(t, spec.ParseStage{Format: "json", OnError: tc.onError}).Process(msg)
		if err == nil || pass != tc.pass {
			t.Fatalf("onError %q: pass=%v err=%v", tc.onError, pass, err)
		}
		if len(msg.Fields) != 1 || msg.Fields["a"] != "b" {
			t.Fatalf("onError %q changed fields: %v", tc.onError, msg.Fields)
		}
	}
	// A missing field and a non-string value are errors too.
	msg := message.New(nil, "f", "test")
	msg.Fields["n"] = 3.0
	for _, from := range []string{`fields["missing"]`, `fields["n"]`} {
		if pass, err := parseStageOf(t, spec.ParseStage{From: from, Format: "json"}).Process(msg); err == nil || !pass {
			t.Fatalf("%s: pass=%v err=%v", from, pass, err)
		}
	}
	// A failing decoder never exposes partial results.
	if _, err := parseStageOf(t, spec.ParseStage{Decoders: []spec.CodecStep{{Type: "gzip"}}, Format: "json"}).Process(msg); err == nil || len(msg.Fields) != 1 {
		t.Fatalf("decoder failure: %v %v", err, msg.Fields)
	}
	// Chain errors carry the stage ID for bulletins and metrics.
	chain, err := NewChain([]spec.StageSpec{{ID: "inner", Kind: "parse", Parse: &spec.ParseStage{Format: "json"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = chain.Pass(message.New([]byte("{"), "f", "test"))
	var stageErr *StageError
	if !asStageError(err, &stageErr) || stageErr.ID != "inner" {
		t.Fatalf("chain error = %v", err)
	}
}

func asStageError(err error, target **StageError) bool {
	se, ok := err.(*StageError)
	if ok {
		*target = se
	}
	return ok
}

func TestParseStageValidation(t *testing.T) {
	for name, p := range map[string]*spec.ParseStage{
		"missing block":     nil,
		"no format":         {},
		"unknown format":    {Format: "yaml"},
		"python without fn": {Format: "python"},
		"python on json":    {Format: "json", Python: &spec.PythonRef{Library: "l", Function: "f"}},
		"replace with into": {Format: "json", Mode: "replace", Into: "x"},
		"unknown mode":      {Format: "json", Mode: "append"},
		"text without into": {Format: "text"},
		"text replace":      {Format: "text", Mode: "replace"},
		"bad onError":       {Format: "json", OnError: "pass"},
		"bad decoder":       {Format: "json", Decoders: []spec.CodecStep{{Type: "rot13"}}},
		"bad from":          {Format: "json", From: `fields[`},
		"library-less fn":   {Format: "python", Python: &spec.PythonRef{Function: "f"}},
	} {
		if _, err := New(spec.StageSpec{ID: "p", Kind: "parse", Parse: p}); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if _, err := New(spec.StageSpec{ID: "p", Kind: "parse", OnError: "drop", Parse: &spec.ParseStage{Format: "json"}}); err == nil {
		t.Fatal("stage-level onError drop accepted for a parse stage (use parse.onError)")
	}
}

type parseWorker struct {
	gofiv1connect.UnimplementedWorkerServiceHandler
	mu    sync.Mutex
	calls []*gofiv1.WorkerInvokeRequest
}

func (w *parseWorker) Invoke(_ context.Context, req *connect.Request[gofiv1.WorkerInvokeRequest]) (*connect.Response[gofiv1.WorkerInvokeResponse], error) {
	w.mu.Lock()
	w.calls = append(w.calls, req.Msg)
	w.mu.Unlock()
	if req.Msg.Kind != "parser" {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	out := &gofiv1.WorkerInvokeResponse{Stdout: "parsed\n", DurationUs: 5}
	switch req.Msg.Name {
	case "kv":
		fields := map[string]any{}
		for _, pair := range strings.Split(string(req.Msg.Raw), ";") {
			if k, v, ok := strings.Cut(pair, "="); ok {
				fields[k] = v
			}
		}
		out.Fields, _ = structpb.NewStruct(fields)
	case "reject":
		out.Drop = true
	}
	return connect.NewResponse(out), nil
}

func TestParseStagePythonFunction(t *testing.T) {
	worker := &parseWorker{}
	_, handler := gofiv1connect.NewWorkerServiceHandler(worker)
	server := httptest.NewServer(handler)
	defer server.Close()
	functionrpc.SetResolver(func(context.Context) (map[string]functionrpc.Binding, error) {
		return map[string]functionrpc.Binding{"shared": {Endpoint: server.URL, Token: "t", TimeoutSeconds: 1}}, nil
	})
	defer functionrpc.SetResolver(nil)

	msg := message.New([]byte("frame"), "f", "test")
	msg.Fields["kv"] = base64.StdEncoding.EncodeToString([]byte("a=1;b=2"))
	msg.Fields["keep"] = "yes"
	msg.Meta["source"] = "10.0.0.1"
	st := parseStageOf(t, spec.ParseStage{From: `fields["kv"]`, Decoders: []spec.CodecStep{{Type: "base64"}}, Format: "python", Python: &spec.PythonRef{Library: "shared", Function: "kv"}, Into: "kv"})
	if pass, err := st.Process(msg); !pass || err != nil {
		t.Fatalf("python parse: %v %v", pass, err)
	}
	kv := msg.Fields["kv"].(map[string]any)
	if kv["a"] != "1" || kv["b"] != "2" || msg.Fields["keep"] != "yes" {
		t.Fatalf("fields = %v", msg.Fields)
	}
	call := worker.calls[0]
	if string(call.Raw) != "a=1;b=2" || len(call.Fields.AsMap()) != 0 || call.Meta.AsMap()["source"] != "10.0.0.1" {
		t.Fatalf("worker input raw=%q fields=%v meta=%v", call.Raw, call.Fields.AsMap(), call.Meta.AsMap())
	}
	if stdout, us := st.(Observed).Observation(); stdout != "parsed\n" || us != 5 {
		t.Fatalf("observation = %q %d", stdout, us)
	}
	// An explicit rejection drops even with onError continue.
	reject := parseStageOf(t, spec.ParseStage{Format: "python", Python: &spec.PythonRef{Library: "shared", Function: "reject"}, OnError: "continue"})
	if pass, err := reject.Process(msg); pass || err == nil {
		t.Fatalf("rejection: pass=%v err=%v", pass, err)
	}
}
