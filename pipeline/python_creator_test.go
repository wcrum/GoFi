package pipeline

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/functionrpc"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

type traceWorker struct {
	gofiv1connect.UnimplementedWorkerServiceHandler
}

func (traceWorker) Invoke(_ context.Context, req *connect.Request[gofiv1.WorkerInvokeRequest]) (*connect.Response[gofiv1.WorkerInvokeResponse], error) {
	if req.Msg.Name == "explode" {
		return connect.NewResponse(&gofiv1.WorkerInvokeResponse{Stdout: "before failure\n", DurationUs: 9, Error: "KeyError: 'missing'", ErrorLocation: &gofiv1.SourceDiagnostic{Line: 4}}), nil
	}
	fields := req.Msg.Fields.AsMap()
	fields["limit"] = req.Msg.Config.AsMap()["limit"]
	out, _ := structpb.NewStruct(fields)
	return connect.NewResponse(&gofiv1.WorkerInvokeResponse{Fields: out, Stdout: "checked\n", DurationUs: 77}), nil
}

func TestPythonStageTraceCarriesStdoutDurationAndUserErrors(t *testing.T) {
	_, handler := gofiv1connect.NewWorkerServiceHandler(traceWorker{})
	server := httptest.NewServer(handler)
	defer server.Close()
	functionrpc.SetResolver(func(context.Context) (map[string]functionrpc.Binding, error) {
		return map[string]functionrpc.Binding{"lib": {Endpoint: server.URL, Token: "t", TimeoutSeconds: 2}}, nil
	})
	defer functionrpc.SetResolver(nil)
	def := spec.FlowDefinition{Name: "trace", Parser: spec.ParserCfg{Format: "json"}, Stages: []spec.StageSpec{
		{ID: "check", Kind: "python", Python: &spec.PythonRef{Library: "lib", Function: "check", Config: map[string]any{"limit": 5}}},
		{ID: "boom", Kind: "python", Python: &spec.PythonRef{Library: "lib", Function: "explode"}},
	}}
	result, err := Simulate(def, []byte(`{"temperature": 9}`))
	if err != nil {
		t.Fatal(err)
	}
	check := result.Stages[0]
	if !check.Pass || check.Stdout != "checked\n" || check.DurationUs != 77 || check.Fields["limit"] != float64(5) {
		t.Fatalf("python trace: %+v", check)
	}
	boom := result.Stages[1]
	if boom.Pass || !strings.Contains(boom.Error, "KeyError") || !strings.Contains(boom.Error, "line 4") || boom.Stdout != "before failure\n" || result.DroppedBy != "boom" {
		t.Fatalf("user errors must fail closed and keep stdout: %+v", boom)
	}
}

func TestSimulationRunsTheCurrentInlineSource(t *testing.T) {
	var seen []*spec.PythonRef
	functionrpc.SetInlineRunner(func(_ context.Context, ref *spec.PythonRef, req *gofiv1.WorkerInvokeRequest) (*gofiv1.WorkerInvokeResponse, error) {
		seen = append(seen, ref)
		out, _ := structpb.NewStruct(map[string]any{"via": "inline", "name": req.Name})
		return &gofiv1.WorkerInvokeResponse{Fields: out, Stdout: "inline ran\n"}, nil
	})
	defer functionrpc.SetInlineRunner(nil)
	stored := &spec.PythonRef{Library: spec.ManagedLibraryName("demo"), Function: "enrich", InlineSource: "def transform(fields):\n    return fields\n"}
	def := spec.FlowDefinition{Name: "demo", Parser: spec.ParserCfg{Format: "json"}, Stages: []spec.StageSpec{{ID: "enrich", Kind: "python", Python: stored}}}
	result, err := Simulate(def, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].Library != "" || seen[0].InlineSource == "" || result.Stages[0].Fields["name"] != "enrich" || result.Stages[0].Stdout != "inline ran\n" {
		t.Fatalf("inline simulation must run the snippet itself: %+v %+v", seen, result.Stages)
	}
	if stored.Library != spec.ManagedLibraryName("demo") {
		t.Fatal("Simulate mutated the caller's definition")
	}
}
