// Package functionrpc invokes isolated Python functions through generated
// ConnectRPC clients. It never evaluates Python in the GoFi controller.
package functionrpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

type Binding struct {
	Endpoint       string `json:"endpoint"`
	Token          string `json:"token"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}
type Resolver func(context.Context) (map[string]Binding, error)

// InlineRunner executes an unresolved inline stage (editor simulations of
// unsaved source) in an isolated worker. It is installed by the controller
// process; listener runtimes only ever see resolved managed-library refs.
type InlineRunner func(context.Context, *spec.PythonRef, *gofiv1.WorkerInvokeRequest) (*gofiv1.WorkerInvokeResponse, error)

var resolverMu sync.RWMutex
var resolve Resolver
var inlineRunner InlineRunner

// InlineRunTimeout covers starting a scratch worker as well as the call.
const InlineRunTimeout = 90 * time.Second

// NewInvocationID returns a short random id correlating one invocation's
// result with its live worker log lines.
func NewInvocationID() string {
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(id[:])
}

func SetInlineRunner(r InlineRunner) { resolverMu.Lock(); inlineRunner = r; resolverMu.Unlock() }

var transport = &http.Transport{Proxy: nil, ResponseHeaderTimeout: 5 * time.Second}
var client = &http.Client{Transport: transport}

func SetResolver(r Resolver) { resolverMu.Lock(); resolve = r; resolverMu.Unlock() }
func Bindings(ctx context.Context) (map[string]Binding, error) {
	resolverMu.RLock()
	r := resolve
	resolverMu.RUnlock()
	if r != nil {
		return r(ctx)
	}
	var bindings map[string]Binding
	if raw := os.Getenv("GOFI_PYTHON_BINDINGS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &bindings); err != nil {
			return nil, fmt.Errorf("invalid Python runtime bindings")
		}
	}
	return bindings, nil
}
func Environment(ctx context.Context, def spec.FlowDefinition) (string, error) {
	bindings, err := Bindings(ctx)
	if err != nil {
		return "", err
	}
	used := map[string]Binding{}
	for _, use := range def.PythonRefs() {
		if ref := use.Ref; ref.Library != "" {
			binding, ok := bindings[ref.Library]
			if !ok {
				return "", fmt.Errorf("Python library %q is not ready", ref.Library)
			}
			used[ref.Library] = binding
		}
	}
	data, err := json.Marshal(used)
	return string(data), err
}

// Invoke calls one Python function. A user-code failure is returned as an
// error (callers fail closed) together with the response, whose captured
// stdout, duration, and error location remain available for traces.
func Invoke(ctx context.Context, ref *spec.PythonRef, kind string, msg *message.Message) (*gofiv1.WorkerInvokeResponse, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	fields, err := structpb.NewStruct(msg.Fields)
	if err != nil {
		return nil, err
	}
	meta := map[string]any{}
	for k, v := range msg.Meta {
		meta[k] = v
	}
	metaValue, _ := structpb.NewStruct(meta)
	// Each live invocation gets an id so its output can be followed in the
	// worker's live log stream (WatchWorkerLogs).
	request := &gofiv1.WorkerInvokeRequest{Name: ref.Function, Kind: kind, Raw: msg.Raw, Fields: fields, Meta: metaValue, InvocationId: NewInvocationID()}
	if len(ref.Config) > 0 {
		if request.Config, err = structpb.NewStruct(ref.Config); err != nil {
			return nil, fmt.Errorf("python.config: %w", err)
		}
	}
	label := ref.Library + "/" + ref.Function
	var res *gofiv1.WorkerInvokeResponse
	if ref.Library == "" && ref.InlineSource != "" {
		resolverMu.RLock()
		runner := inlineRunner
		resolverMu.RUnlock()
		if runner == nil {
			return nil, fmt.Errorf("inline Python stage %q is not resolved to a managed library", ref.Function)
		}
		label = "inline " + ref.Function
		ctx, cancel := context.WithTimeout(ctx, InlineRunTimeout)
		defer cancel()
		res, err = runner(ctx, ref, request)
	} else {
		bindings, bindErr := Bindings(ctx)
		if bindErr != nil {
			return nil, bindErr
		}
		binding, ok := bindings[ref.Library]
		if !ok {
			return nil, fmt.Errorf("Python library %q is not ready", ref.Library)
		}
		timeout := binding.TimeoutSeconds
		if timeout <= 0 || timeout > 30 {
			timeout = 5
		}
		ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		res, err = Call(ctx, binding, request)
	}
	if err != nil {
		return nil, fmt.Errorf("Python %s %s: %w", kind, label, err)
	}
	if res.Error != "" {
		location := ""
		if res.ErrorLocation != nil && res.ErrorLocation.Line > 0 {
			location = fmt.Sprintf(" (line %d)", res.ErrorLocation.Line)
		}
		return res, fmt.Errorf("Python %s %s: %s%s", kind, label, res.Error, location)
	}
	return res, nil
}

// Call sends one authenticated Invoke to a worker binding.
func Call(ctx context.Context, binding Binding, request *gofiv1.WorkerInvokeRequest) (*gofiv1.WorkerInvokeResponse, error) {
	req := connect.NewRequest(request)
	req.Header().Set("Authorization", "Bearer "+binding.Token)
	rpc := gofiv1connect.NewWorkerServiceClient(client, binding.Endpoint, connect.WithReadMaxBytes(2<<20), connect.WithSendMaxBytes(2<<20))
	res, err := rpc.Invoke(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// LibraryBinding returns the worker binding of one saved library.
func LibraryBinding(ctx context.Context, library string) (Binding, error) {
	bindings, err := Bindings(ctx)
	if err != nil {
		return Binding{}, err
	}
	binding, ok := bindings[library]
	if !ok {
		return Binding{}, fmt.Errorf("Python library %q is not ready", library)
	}
	return binding, nil
}

// streamTransport serves long-lived source streams: a streamer may take any
// time to produce, so there is no response header timeout (cancellation ends
// the call instead).
var streamTransport = &http.Transport{Proxy: nil}
var streamClient = &http.Client{Transport: streamTransport}

// MaxSourceMessageBytes caps one Fetch response or Stream message.
const MaxSourceMessageBytes = 20 << 20

func workerClient(binding Binding, hc *http.Client, readMax int) gofiv1connect.WorkerServiceClient {
	return gofiv1connect.NewWorkerServiceClient(hc, binding.Endpoint, connect.WithReadMaxBytes(readMax), connect.WithSendMaxBytes(2<<20))
}

// ListFunctions asks a library's worker for its registered functions.
func ListFunctions(ctx context.Context, binding Binding) ([]*gofiv1.WorkerFunction, error) {
	req := connect.NewRequest(&gofiv1.WorkerListFunctionsRequest{})
	req.Header().Set("Authorization", "Bearer "+binding.Token)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := workerClient(binding, client, 1<<20).ListFunctions(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg.Functions, nil
}

// Fetch runs one @gofi.fetcher poll. The worker enforces the library's
// deadline; the client allows a little more for transport.
func Fetch(ctx context.Context, binding Binding, request *gofiv1.WorkerSourceRequest) (*gofiv1.WorkerFetchResponse, error) {
	timeout := binding.TimeoutSeconds
	if timeout <= 0 || timeout > 30 {
		timeout = 5
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second+5*time.Second)
	defer cancel()
	req := connect.NewRequest(request)
	req.Header().Set("Authorization", "Bearer "+binding.Token)
	res, err := workerClient(binding, client, MaxSourceMessageBytes).Fetch(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// Stream opens a @gofi.streamer. Cancelling ctx (the flow stopping) closes
// the stream, and the worker stops the streamer process.
func Stream(ctx context.Context, binding Binding, request *gofiv1.WorkerSourceRequest) (*connect.ServerStreamForClient[gofiv1.WorkerStreamResponse], error) {
	req := connect.NewRequest(request)
	req.Header().Set("Authorization", "Bearer "+binding.Token)
	return workerClient(binding, streamClient, MaxSourceMessageBytes).Stream(ctx, req)
}
