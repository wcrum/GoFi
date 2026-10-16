package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/sandbox"
	"google.golang.org/protobuf/types/known/structpb"
)

type scratchBackend struct {
	mu       sync.Mutex
	endpoint string
	started  []sandbox.Definition
	stopped  []string
}

func (b *scratchBackend) Start(_ context.Context, d sandbox.Definition, _ string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.started = append(b.started, d)
	return b.endpoint, nil
}
func (b *scratchBackend) Stop(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = append(b.stopped, name)
	return nil
}

type scratchWorker struct {
	gofiv1connect.UnimplementedWorkerServiceHandler
	mu      sync.Mutex
	invoked []*gofiv1.WorkerInvokeRequest
}

func (w *scratchWorker) ValidateSource(_ context.Context, req *connect.Request[gofiv1.WorkerValidateSourceRequest]) (*connect.Response[gofiv1.WorkerValidateSourceResponse], error) {
	if strings.Contains(req.Msg.Source, "undefined_name") {
		return connect.NewResponse(&gofiv1.WorkerValidateSourceResponse{Diagnostics: []*gofiv1.SourceDiagnostic{{Line: 2, Column: 5, Code: "F821", Message: "Undefined name `undefined_name`"}}}), nil
	}
	return connect.NewResponse(&gofiv1.WorkerValidateSourceResponse{Valid: true}), nil
}
func (w *scratchWorker) Invoke(_ context.Context, req *connect.Request[gofiv1.WorkerInvokeRequest]) (*connect.Response[gofiv1.WorkerInvokeResponse], error) {
	w.mu.Lock()
	w.invoked = append(w.invoked, req.Msg)
	w.mu.Unlock()
	fields, _ := structpb.NewStruct(map[string]any{"limit": req.Msg.Config.AsMap()["limit"]})
	return connect.NewResponse(&gofiv1.WorkerInvokeResponse{Fields: fields, Stdout: "hello from python\n", DurationUs: 1234, Error: "", ErrorLocation: nil}), nil
}
func (w *scratchWorker) DescribeEnvironment(context.Context, *connect.Request[gofiv1.DescribePythonEnvironmentRequest]) (*connect.Response[gofiv1.DescribePythonEnvironmentResponse], error) {
	return connect.NewResponse(&gofiv1.DescribePythonEnvironmentResponse{PythonVersion: "3.14.7", Packages: []*gofiv1.PythonPackage{{Name: "msgpack", Version: "1.2.3"}}}), nil
}

func creatorClient(t *testing.T) (gofiv1connect.SandboxServiceClient, *sandbox.Supervisor, *scratchBackend, *scratchWorker) {
	t.Helper()
	worker := &scratchWorker{}
	_, handler := gofiv1connect.NewWorkerServiceHandler(worker)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	backend := &scratchBackend{endpoint: server.URL}
	manager := sandbox.New(backend, nil)
	t.Cleanup(func() { _ = manager.CloseTooling(context.Background()) })
	application := New(controller.New(driver.NewInProcess()), nil, deploymode.ModeBinary, manager)
	return gofiv1connect.NewSandboxServiceClient(handlerClient{application}, "http://gofi.test"), manager, backend, worker
}

func TestRunSourceReportsStaticErrorsWithoutStartingAWorker(t *testing.T) {
	client, _, backend, _ := creatorClient(t)
	res, err := client.RunSource(context.Background(), connect.NewRequest(&gofiv1.RunSourceRequest{Source: "import gofi\nundefined_name\n", Kind: "transformer"}))
	if err != nil || !strings.Contains(res.Msg.Error, "F821") || res.Msg.ErrorLocation.GetLine() != 2 {
		t.Fatalf("static diagnostic not reported: %v %v", res, err)
	}
	for _, d := range backend.started {
		if strings.HasPrefix(d.Name, sandbox.ScratchPrefix) {
			t.Fatal("invalid source started a scratch worker")
		}
	}
}

func TestRunSourceUsesAScratchWorkerAndRemovesIt(t *testing.T) {
	client, _, backend, worker := creatorClient(t)
	config, _ := structpb.NewStruct(map[string]any{"limit": 7})
	res, err := client.RunSource(context.Background(), connect.NewRequest(&gofiv1.RunSourceRequest{Source: "import gofi\n", Name: "t", Kind: "transformer", Config: config, TimeoutSeconds: 3}))
	if err != nil || res.Msg.Stdout != "hello from python\n" || res.Msg.DurationUs != 1234 || res.Msg.Fields.AsMap()["limit"] != float64(7) {
		t.Fatalf("scratch run result: %v %v", res, err)
	}
	var scratch *sandbox.Definition
	for i, d := range backend.started {
		if strings.HasPrefix(d.Name, sandbox.ScratchPrefix) {
			scratch = &backend.started[i]
		}
	}
	if scratch == nil || !scratch.Scratch || scratch.TimeoutSeconds != 3 || scratch.MemoryMiB != 128 {
		t.Fatalf("scratch worker definition: %+v", scratch)
	}
	stoppedScratch := false
	for _, name := range backend.stopped {
		stoppedScratch = stoppedScratch || name == scratch.Name
	}
	if !stoppedScratch {
		t.Fatal("scratch worker was not removed")
	}
	if len(worker.invoked) != 1 || worker.invoked[0].Config.AsMap()["limit"] != float64(7) {
		t.Fatal("config was not passed to the worker")
	}
}

func TestManagedLibrariesAreReadOnlyAndPrefixesReserved(t *testing.T) {
	client, manager, _, _ := creatorClient(t)
	_, err := client.ApplySandbox(context.Background(), connect.NewRequest(&gofiv1.ApplySandboxRequest{Sandbox: &gofiv1.SandboxDefinition{Name: "flow-mine", SourceCode: "import gofi"}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("reserved prefix accepted: %v", err)
	}
	if err := manager.Apply(context.Background(), sandbox.Definition{Name: "flow-demo-1a2b3c4d", SourceCode: "import gofi", ManagedBy: "flow:demo"}); err != nil {
		t.Fatal(err)
	}
	_, err = client.ApplySandbox(context.Background(), connect.NewRequest(&gofiv1.ApplySandboxRequest{Sandbox: &gofiv1.SandboxDefinition{Name: "flow-demo-1a2b3c4d", SourceCode: "import gofi"}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("managed library overwritten: %v", err)
	}
	_, err = client.DeleteSandbox(context.Background(), connect.NewRequest(&gofiv1.DeleteSandboxRequest{Name: "flow-demo-1a2b3c4d"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("managed library deleted by a user: %v", err)
	}
}

func TestDescribeEnvironmentAddsDeploymentPolicy(t *testing.T) {
	client, _, _, _ := creatorClient(t)
	res, err := client.DescribeEnvironment(context.Background(), connect.NewRequest(&gofiv1.DescribePythonEnvironmentRequest{}))
	if err != nil || res.Msg.PythonVersion != "3.14.7" || res.Msg.NetworkPolicy == "" || res.Msg.MaxTimeoutSeconds != 30 {
		t.Fatalf("environment: %v %v", res, err)
	}
}
