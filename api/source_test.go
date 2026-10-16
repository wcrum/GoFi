package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/sandbox"
	"google.golang.org/protobuf/types/known/structpb"
)

type sourceBackend struct {
	mu          sync.Mutex
	endpoint    string
	definitions []sandbox.Definition
	tokens      map[string]string
}

func (b *sourceBackend) Start(_ context.Context, d sandbox.Definition, token string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.definitions = append(b.definitions, d)
	b.tokens[token] = d.Name
	return b.endpoint, nil
}
func (b *sourceBackend) Stop(context.Context, string) error            { return nil }
func (b *sourceBackend) Health(context.Context, sandbox.Binding) error { return nil }
func (b *sourceBackend) owner(token string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tokens[strings.TrimPrefix(token, "Bearer ")]
}

type sourceWorker struct {
	gofiv1connect.UnimplementedWorkerServiceHandler
	backend              *sourceBackend
	mu                   sync.Mutex
	formatted, validated []string
}

func (w *sourceWorker) FormatSource(ctx context.Context, req *connect.Request[gofiv1.WorkerFormatSourceRequest]) (*connect.Response[gofiv1.WorkerFormatSourceResponse], error) {
	if !strings.HasPrefix(w.backend.owner(req.Header().Get("Authorization")), "gofi-tools-") {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("private tool credential required"))
	}
	w.mu.Lock()
	w.formatted = append(w.formatted, req.Msg.Source)
	w.mu.Unlock()
	switch req.Msg.Source {
	case "syntax error":
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("source cannot be parsed"))
	case "large result":
		return connect.NewResponse(&gofiv1.WorkerFormatSourceResponse{Source: strings.Repeat("x", maxPythonSourceBytes+1)}), nil
	case "wait":
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return connect.NewResponse(&gofiv1.WorkerFormatSourceResponse{Source: "formatted: " + req.Msg.Source}), nil
}
func (w *sourceWorker) ValidateSource(_ context.Context, req *connect.Request[gofiv1.WorkerValidateSourceRequest]) (*connect.Response[gofiv1.WorkerValidateSourceResponse], error) {
	if !strings.HasPrefix(w.backend.owner(req.Header().Get("Authorization")), "gofi-tools-") {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("private tool credential required"))
	}
	w.mu.Lock()
	w.validated = append(w.validated, req.Msg.Source)
	w.mu.Unlock()
	if req.Msg.Source == "invalid" {
		return connect.NewResponse(&gofiv1.WorkerValidateSourceResponse{Valid: false, Diagnostics: []*gofiv1.SourceDiagnostic{{Line: 3, Column: 8, Code: "F821", Message: "Undefined name"}}}), nil
	}
	return connect.NewResponse(&gofiv1.WorkerValidateSourceResponse{Valid: true}), nil
}
func (w *sourceWorker) Invoke(_ context.Context, req *connect.Request[gofiv1.WorkerInvokeRequest]) (*connect.Response[gofiv1.WorkerInvokeResponse], error) {
	if w.backend.owner(req.Header().Get("Authorization")) != "library" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("library credential required"))
	}
	if req.Msg.Name == "empty" && req.Msg.Kind == "encoder" {
		return connect.NewResponse(&gofiv1.WorkerInvokeResponse{Raw: []byte{}}), nil
	}
	if req.Msg.Name != "decode" || req.Msg.Kind != "parser" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("function not registered"))
	}
	fields, _ := structpb.NewStruct(map[string]any{"raw": string(req.Msg.Raw), "value": req.Msg.Fields.AsMap()["value"], "source": req.Msg.Meta.AsMap()["source"]})
	return connect.NewResponse(&gofiv1.WorkerInvokeResponse{Fields: fields}), nil
}
func sourceTestClient(t *testing.T) (gofiv1connect.SandboxServiceClient, *sandbox.Supervisor, *sourceBackend, *sourceWorker) {
	t.Helper()
	backend := &sourceBackend{tokens: map[string]string{}}
	worker := &sourceWorker{backend: backend}
	_, handler := gofiv1connect.NewWorkerServiceHandler(worker)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	backend.endpoint = server.URL
	manager := sandbox.New(backend, nil)
	t.Cleanup(func() { _ = manager.CloseTooling(context.Background()) })
	ctrl := controller.New(driver.NewInProcess())
	application := New(ctrl, nil, deploymode.ModeBinary, manager)
	return gofiv1connect.NewSandboxServiceClient(handlerClient{application}, "http://gofi.test"), manager, backend, worker
}
func TestSourceToolingWorksBeforeAnyLibraryIsSaved(t *testing.T) {
	client, manager, backend, worker := sourceTestClient(t)
	source := "import gofi\nprint('formatting must not run this')"
	formatted, err := client.FormatSource(context.Background(), connect.NewRequest(&gofiv1.FormatSourceRequest{Source: source}))
	if err != nil || formatted.Msg.Source != "formatted: "+source {
		t.Fatalf("format proxy: %v %v", formatted, err)
	}
	valid, err := client.ValidateSource(context.Background(), connect.NewRequest(&gofiv1.ValidateSourceRequest{Source: "invalid"}))
	if err != nil || valid.Msg.Valid || len(valid.Msg.Diagnostics) != 1 || valid.Msg.Diagnostics[0].Line != 3 || valid.Msg.Diagnostics[0].Column != 8 || valid.Msg.Diagnostics[0].Code != "F821" {
		t.Fatalf("validation diagnostics: %v %v", valid, err)
	}
	if len(manager.List()) != 0 {
		t.Fatal("format/validation persisted a public Sandbox")
	}
	bindings, _ := manager.Bindings(context.Background())
	if len(bindings) != 0 {
		t.Fatal("private tooling binding exposed to application functions")
	}
	backend.mu.Lock()
	definitions := append([]sandbox.Definition(nil), backend.definitions...)
	backend.mu.Unlock()
	if len(definitions) != 1 || !strings.HasPrefix(definitions[0].Name, "gofi-tools-") || strings.Contains(definitions[0].SourceCode, "formatting must not run this") {
		t.Fatalf("submitted editor source used as worker startup code: %+v", definitions)
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.formatted) != 1 || worker.formatted[0] != source || len(worker.validated) != 1 {
		t.Fatal("tooling was not delegated through generated authenticated worker RPC")
	}
}
func TestSourceToolingBoundsAndStructuredWorkerErrors(t *testing.T) {
	client, _, backend, _ := sourceTestClient(t)
	for _, request := range []string{strings.Repeat("x", maxPythonSourceBytes+1)} {
		_, err := client.FormatSource(context.Background(), connect.NewRequest(&gofiv1.FormatSourceRequest{Source: request}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("oversized source accepted: %v", err)
		}
	}
	backend.mu.Lock()
	starts := len(backend.definitions)
	backend.mu.Unlock()
	if starts != 0 {
		t.Fatal("invalid source started a worker")
	}
	for source, want := range map[string]connect.Code{"syntax error": connect.CodeInvalidArgument, "large result": connect.CodeResourceExhausted} {
		_, err := client.FormatSource(context.Background(), connect.NewRequest(&gofiv1.FormatSourceRequest{Source: source}))
		if connect.CodeOf(err) != want {
			t.Fatalf("worker %s error code: %v", source, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := client.FormatSource(ctx, connect.NewRequest(&gofiv1.FormatSourceRequest{Source: "wait"}))
	if connect.CodeOf(err) != connect.CodeDeadlineExceeded {
		t.Fatalf("tooling deadline was not propagated: %v", err)
	}
}
func TestRunFunctionUsesSavedLibraryAndRetainsEnvelopeAndEmptyBytes(t *testing.T) {
	client, manager, _, _ := sourceTestClient(t)
	request := &gofiv1.RunFunctionRequest{Library: "library", Name: "decode", Kind: "parser", Raw: []byte("sample")}
	if _, err := client.RunFunction(context.Background(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unsaved library ran: %v", err)
	}
	if err := manager.Apply(context.Background(), sandbox.Definition{Name: "library", SourceCode: "import gofi\n"}); err != nil {
		t.Fatal(err)
	}
	request.Fields, _ = structpb.NewStruct(map[string]any{"value": 42})
	request.Meta, _ = structpb.NewStruct(map[string]any{"source": "editor"})
	result, err := client.RunFunction(context.Background(), connect.NewRequest(request))
	if err != nil || result.Msg.Fields.AsMap()["raw"] != "sample" || result.Msg.Fields.AsMap()["value"] != float64(42) || result.Msg.Fields.AsMap()["source"] != "editor" {
		t.Fatalf("function envelope: %v %v", result, err)
	}
	empty, err := client.RunFunction(context.Background(), connect.NewRequest(&gofiv1.RunFunctionRequest{Library: "library", Name: "empty", Kind: "encoder"}))
	if err != nil || empty.Msg.Raw == nil || !bytes.Equal(empty.Msg.Raw, []byte{}) {
		t.Fatalf("explicit empty raw result lost presence: %v %v", empty, err)
	}
	request.Kind = "sink"
	if _, err := client.RunFunction(context.Background(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("kind mismatch not surfaced as structured error: %v", err)
	}
	request.Kind = "parser"
	request.Raw = make([]byte, maxPythonSampleBytes+1)
	if _, err := client.RunFunction(context.Background(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("oversized sample accepted: %v", err)
	}
	saved, _ := manager.Get("library")
	if strings.Contains(fmt.Sprint(saved), "Bearer") {
		t.Fatal("worker credential entered public saved definition")
	}
}
