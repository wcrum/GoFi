package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/functionrpc"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// This worker is served through the actual generated Connect handler and HTTP
// client. Python execution/isolation itself is covered by the worker tests.
type testPythonWorker struct {
	gofiv1connect.UnimplementedWorkerServiceHandler
	mu    sync.Mutex
	calls []*gofiv1.WorkerInvokeRequest
}

func (w *testPythonWorker) Invoke(ctx context.Context, req *connect.Request[gofiv1.WorkerInvokeRequest]) (*connect.Response[gofiv1.WorkerInvokeResponse], error) {
	if req.Header().Get("Authorization") != "Bearer private-worker-token" {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("worker authentication required"))
	}
	kinds := map[string]string{"strip": "decoder", "decode": "parser", "parser-none": "parser", "parser-error": "parser", "slow": "parser", "normalize": "transformer", "transformer-none": "transformer", "capture": "sink"}
	if kinds[req.Msg.Name] != req.Msg.Kind {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("function not registered for requested kind"))
	}
	w.mu.Lock()
	w.calls = append(w.calls, proto.Clone(req.Msg).(*gofiv1.WorkerInvokeRequest))
	w.mu.Unlock()
	response := &gofiv1.WorkerInvokeResponse{}
	switch req.Msg.Name {
	case "strip":
		response.Raw = bytes.TrimPrefix(req.Msg.Raw, []byte("prefix:"))
	case "parser-none", "transformer-none":
		response.Drop = true
	case "parser-error":
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("custom parser failed"))
	case "slow":
		<-ctx.Done()
		return nil, ctx.Err()
	case "decode":
		var fields map[string]any
		if err := json.Unmarshal(req.Msg.Raw, &fields); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		fields["decoder"] = "python"
		response.Fields, _ = structpb.NewStruct(fields)
	case "normalize":
		fields := req.Msg.Fields.AsMap()
		fields["app"] = strings.ToLower(fields["app"].(string))
		response.Fields, _ = structpb.NewStruct(fields)
	case "capture":
	}
	return connect.NewResponse(response), nil
}
func (w *testPythonWorker) snapshot() []*gofiv1.WorkerInvokeRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*gofiv1.WorkerInvokeRequest, len(w.calls))
	copy(out, w.calls)
	return out
}
func startPythonWorker(t *testing.T) (*testPythonWorker, string) {
	t.Helper()
	worker := &testPythonWorker{}
	path, handler := gofiv1connect.NewWorkerServiceHandler(worker)
	_ = path
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	functionrpc.SetResolver(func(context.Context) (map[string]functionrpc.Binding, error) {
		return map[string]functionrpc.Binding{"telemetry": {Endpoint: server.URL, Token: "private-worker-token", TimeoutSeconds: 1}, "unused": {Endpoint: "http://unused.invalid", Token: "unused-private-token"}}, nil
	})
	t.Cleanup(func() { functionrpc.SetResolver(nil) })
	return worker, server.URL
}
func pyRef(function string) *spec.PythonRef {
	return &spec.PythonRef{Library: "telemetry", Function: function}
}
func pythonDefinition() spec.FlowDefinition {
	return spec.FlowDefinition{Name: "python-flow", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 7000, Network: "tcp"}}, Parser: spec.ParserCfg{Format: "python", Python: pyRef("decode"), Decoders: []spec.CodecStep{{Type: "python", Python: pyRef("strip")}}}, Stages: []spec.StageSpec{{ID: "normalize", Kind: "python", Python: pyRef("normalize")}}, Sinks: []spec.SinkCfg{{Type: "python", Python: pyRef("capture")}}}
}
func executePythonPipeline(t *testing.T, def spec.FlowDefinition, raw string, outputs []sink.Sink, dead sink.Sink) TapEvent {
	t.Helper()
	chain, err := filter.NewChain(def.Stages)
	if err != nil {
		t.Fatal(err)
	}
	tap := NewTap()
	events := tap.Subscribe()
	defer tap.Unsubscribe(events)
	p, err := New(def, strings.NewReader(raw), outputs, dead, chain, tap)
	if err != nil {
		t.Fatal(err)
	}
	p.Run(context.Background(), "test-sender")
	select {
	case event := <-events:
		return event
	default:
		t.Fatal("pipeline produced no traffic event")
		return TapEvent{}
	}
}
func TestPythonFunctionsConsumeRealConnectRPCInPipelineOrder(t *testing.T) {
	worker, _ := startPythonWorker(t)
	def := pythonDefinition()
	output, err := sink.New(def.Sinks[0], def.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	raw := `prefix:{"app":"PAYMENTS","count":3}`
	event := executePythonPipeline(t, def, raw, []sink.Sink{output}, nil)
	if event.Dropped || event.Msg.Fields["app"] != "payments" || event.Msg.Fields["decoder"] != "python" {
		t.Fatalf("Python chain failed: %+v", event)
	}
	calls := worker.snapshot()
	if len(calls) != 4 {
		t.Fatalf("expected four actual worker calls: %+v", calls)
	}
	for i, kind := range []string{"decoder", "parser", "transformer", "sink"} {
		if calls[i].Kind != kind {
			t.Fatalf("call %d kind %q, want %q", i, calls[i].Kind, kind)
		}
	}
	if string(calls[0].Raw) != raw || string(calls[1].Raw) != `{"app":"PAYMENTS","count":3}` {
		t.Fatal("the Python decoder did not feed its bytes to the parser")
	}
	dispatched := calls[3]
	if dispatched.Fields.AsMap()["app"] != "payments" || dispatched.Meta.AsMap()["source"] != "test-sender" || dispatched.Meta.AsMap()["name"] != def.Name || dispatched.Meta.AsMap()["ts"] == "" || string(dispatched.Raw) != raw { // decoders preserve the original frame for sinks
		t.Fatalf("worker envelope lost data: %+v", dispatched)
	}
	// Mock executes processing functions but withholds the Python sink entirely.
	result, err := Simulate(def, []byte(raw))
	if err != nil || result.Dropped || result.Stages[0].Fields["app"] != "payments" || !result.Sinks[0].Sent {
		t.Fatalf("Python simulation diverged: %+v %v", result, err)
	}
	if len(worker.snapshot()) != 7 {
		t.Fatal("simulation dispatched a real Python sink")
	}
}
func TestPythonExplicitRejectionsAlwaysStopLiveAndSimulation(t *testing.T) {
	startPythonWorker(t)
	for _, kind := range []string{"parser", "transformer"} {
		t.Run(kind, func(t *testing.T) {
			def := spec.FlowDefinition{Name: "none", Parser: spec.ParserCfg{Format: "json", ErrorPolicy: "continue"}, Sinks: []spec.SinkCfg{{Type: "stdout"}}, DeadLetterSink: &spec.SinkCfg{Type: "stdout"}}
			droppedBy := kind
			switch kind {
			case "parser":
				def.Parser = spec.ParserCfg{Format: "python", Python: pyRef("parser-none"), ErrorPolicy: "continue"}
			case "transformer":
				def.Stages = []spec.StageSpec{{ID: "none-stage", Kind: "python", Python: pyRef("transformer-none")}}
				droppedBy = "none-stage"
			}
			output, dead := &fakeSink{}, &fakeSink{}
			event := executePythonPipeline(t, def, `{"app":"PAYMENTS"}`, []sink.Sink{output}, dead)
			if !event.Dropped || event.DroppedBy != droppedBy || len(output.received()) != 0 || len(dead.received()) != 1 {
				t.Fatalf("explicit rejection failed open: %+v", event)
			}
			result, err := Simulate(def, []byte(`{"app":"PAYMENTS"}`))
			if err != nil || !result.Dropped || result.DroppedBy != droppedBy || !result.DeadLetter || result.ParseError != "" {
				t.Fatalf("explicit rejection simulation: %+v %v", result, err)
			}
		})
	}
}
func TestPythonParserFailuresAreClosedUnlessContinueIsExplicit(t *testing.T) {
	startPythonWorker(t)
	for _, function := range []string{"parser-error", "slow", "capture", "missing-library"} {
		for _, policy := range []string{"", "continue"} {
			t.Run(function+"/"+policy, func(t *testing.T) {
				ref := pyRef(function)
				if function == "missing-library" {
					ref.Library = "absent"
				}
				def := spec.FlowDefinition{Name: "failure", Parser: spec.ParserCfg{Format: "python", Python: ref, ErrorPolicy: policy}, Sinks: []spec.SinkCfg{{Type: "stdout"}}, DeadLetterSink: &spec.SinkCfg{Type: "stdout"}}
				output, dead := &fakeSink{}, &fakeSink{}
				started := time.Now()
				event := executePythonPipeline(t, def, `{"app":"PAYMENTS"}`, []sink.Sink{output}, dead)
				if time.Since(started) > 3*time.Second {
					t.Fatal("worker timeout was not bounded")
				}
				wantDrop := policy != "continue"
				if event.Dropped != wantDrop || event.Msg.Meta["parseError"] == "" {
					t.Fatalf("parser failure policy: %+v", event)
				}
				if strings.Contains(event.Msg.Meta["parseError"], "private-worker-token") {
					t.Fatal("worker error leaked runtime credential")
				}
				result, err := Simulate(def, []byte(`{"app":"PAYMENTS"}`))
				if err != nil || result.Dropped != wantDrop || result.ParseError == "" {
					t.Fatalf("simulation failure policy: %+v %v", result, err)
				}
			})
		}
	}
}
func TestPythonRuntimeBindingsDoNotBecomeDesiredConfiguration(t *testing.T) {
	_, endpoint := startPythonWorker(t)
	def := pythonDefinition()
	before, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := functionrpc.Environment(context.Background(), def)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runtime, "private-worker-token") || !strings.Contains(runtime, endpoint) || strings.Contains(runtime, "unused-private-token") {
		t.Fatalf("runtime bindings were not restricted to used libraries: %s", runtime)
	}
	after, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || bytes.Contains(after, []byte("private-worker-token")) || bytes.Contains(after, []byte(endpoint)) {
		t.Fatal("private runtime bindings were persisted into the Listener definition")
	}
	// Kind mismatch is reported through the generated Connect error envelope.
	_, err = functionrpc.Invoke(context.Background(), pyRef("capture"), "parser", &message.Message{})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("worker kind mismatch was not propagated: %v", err)
	}
}
