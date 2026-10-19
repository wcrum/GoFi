package api

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/sandbox"
)

type countingWorker struct {
	gofiv1connect.UnimplementedWorkerServiceHandler
	calls atomic.Int32
}

func (w *countingWorker) ListFunctions(context.Context, *connect.Request[gofiv1.WorkerListFunctionsRequest]) (*connect.Response[gofiv1.WorkerListFunctionsResponse], error) {
	w.calls.Add(1)
	return connect.NewResponse(&gofiv1.WorkerListFunctionsResponse{Functions: []*gofiv1.WorkerFunction{{Name: "enrich", Kind: "map"}}}), nil
}

// A worker is asked once per binding, however many browsers poll; a new
// binding (restart, new digest) refreshes at once.
func TestFunctionCacheAsksEachWorkerOncePerBinding(t *testing.T) {
	worker := &countingWorker{}
	path, handler := gofiv1connect.NewWorkerServiceHandler(worker)
	server := httptest.NewServer(handler)
	defer server.Close()
	_ = path
	cache := newFunctionCache()
	libraries := []sandbox.Definition{{Name: "hooks"}}
	binding := map[string]sandbox.Binding{"hooks": {Endpoint: server.URL, Token: "t1"}}
	for i := 0; i < 5; i++ {
		functions := cache.list(context.Background(), libraries, binding)
		if len(functions) != 1 || functions[0].Library != "hooks" || functions[0].Name != "enrich" {
			t.Fatalf("functions = %+v", functions)
		}
	}
	if worker.calls.Load() != 1 {
		t.Fatalf("worker asked %d times for one binding, want 1", worker.calls.Load())
	}
	binding["hooks"] = sandbox.Binding{Endpoint: server.URL, Token: "t2"}
	cache.list(context.Background(), libraries, binding)
	if worker.calls.Load() != 2 {
		t.Fatalf("worker asked %d times after a new binding, want 2", worker.calls.Load())
	}
	cache.invalidate("hooks")
	cache.list(context.Background(), libraries, binding)
	if worker.calls.Load() != 3 {
		t.Fatalf("worker asked %d times after invalidation, want 3", worker.calls.Load())
	}
}
