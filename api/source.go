package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/sandbox"
	"google.golang.org/protobuf/proto"
)

const maxPythonSourceBytes = 65536
const maxPythonSampleBytes = 1 << 20

func sourceSizeValid(source string) error {
	if len(source) > maxPythonSourceBytes {
		return rpcError(connect.CodeInvalidArgument, errors.New("source exceeds 65536 bytes"))
	}
	return nil
}
func workerClient(binding sandbox.Binding) (gofiv1connect.WorkerServiceClient, func()) {
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 5 * time.Second}
	return gofiv1connect.NewWorkerServiceClient(&http.Client{Transport: transport}, binding.Endpoint, connect.WithReadMaxBytes(2<<20), connect.WithSendMaxBytes(2<<20)), transport.CloseIdleConnections
}
func (h *sandboxHandler) FormatSource(ctx context.Context, req *connect.Request[gofiv1.FormatSourceRequest]) (*connect.Response[gofiv1.FormatSourceResponse], error) {
	if err := sourceSizeValid(req.Msg.Source); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	binding, err := h.manager.ToolingBinding(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, errors.New("Python formatting worker unavailable"))
	}
	client, closeIdle := workerClient(binding)
	defer closeIdle()
	call := connect.NewRequest(&gofiv1.WorkerFormatSourceRequest{Source: req.Msg.Source})
	call.Header().Set("Authorization", "Bearer "+binding.Token)
	callCtx, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	response, err := client.FormatSource(callCtx, call)
	if err != nil {
		return nil, err
	}
	if len(response.Msg.Source) > maxPythonSourceBytes {
		return nil, rpcError(connect.CodeResourceExhausted, errors.New("formatted source exceeds 65536 bytes"))
	}
	return connect.NewResponse(&gofiv1.FormatSourceResponse{Source: response.Msg.Source}), nil
}
func (h *sandboxHandler) ValidateSource(ctx context.Context, req *connect.Request[gofiv1.ValidateSourceRequest]) (*connect.Response[gofiv1.ValidateSourceResponse], error) {
	if err := sourceSizeValid(req.Msg.Source); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	binding, err := h.manager.ToolingBinding(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, errors.New("Python validation worker unavailable"))
	}
	client, closeIdle := workerClient(binding)
	defer closeIdle()
	call := connect.NewRequest(&gofiv1.WorkerValidateSourceRequest{Source: req.Msg.Source})
	call.Header().Set("Authorization", "Bearer "+binding.Token)
	callCtx, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	response, err := client.ValidateSource(callCtx, call)
	if err != nil {
		return nil, err
	}
	if len(response.Msg.Diagnostics) > 256 {
		return nil, rpcError(connect.CodeResourceExhausted, errors.New("validation returned too many diagnostics"))
	}
	return connect.NewResponse(&gofiv1.ValidateSourceResponse{Valid: response.Msg.Valid, Diagnostics: response.Msg.Diagnostics}), nil
}
func (h *sandboxHandler) RunFunction(ctx context.Context, req *connect.Request[gofiv1.RunFunctionRequest]) (*connect.Response[gofiv1.RunFunctionResponse], error) {
	if req.Msg.Library == "" || strings.TrimSpace(req.Msg.Name) == "" || len(req.Msg.Name) > 128 {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("library and function name are required"))
	}
	switch req.Msg.Kind {
	case "listener", "parser", "transformer", "sink":
	default:
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("function kind must be listener, parser, transformer, or sink"))
	}
	if proto.Size(req.Msg) > maxPythonSampleBytes {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("function sample exceeds 1 MiB"))
	}
	if _, found := h.manager.Get(req.Msg.Library); !found {
		return nil, rpcError(connect.CodeNotFound, errors.New("Python library not found"))
	}
	bindings, err := h.manager.Bindings(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	binding, ready := bindings[req.Msg.Library]
	if !ready {
		return nil, rpcError(connect.CodeFailedPrecondition, errors.New("Python library worker is not ready; save and launch the library first"))
	}
	timeout := binding.TimeoutSeconds
	if timeout < 1 || timeout > 30 {
		timeout = 5
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	client, closeIdle := workerClient(binding)
	defer closeIdle()
	call := connect.NewRequest(&gofiv1.WorkerInvokeRequest{Name: req.Msg.Name, Kind: req.Msg.Kind, Raw: req.Msg.Raw, Fields: req.Msg.Fields, Meta: req.Msg.Meta})
	call.Header().Set("Authorization", "Bearer "+binding.Token)
	result, err := client.Invoke(callCtx, call)
	if err != nil {
		return nil, err
	}
	if proto.Size(result.Msg) > maxPythonSampleBytes {
		return nil, rpcError(connect.CodeResourceExhausted, errors.New("function result exceeds 1 MiB"))
	}
	return connect.NewResponse(&gofiv1.RunFunctionResponse{Fields: result.Msg.Fields, Raw: result.Msg.Raw, Drop: result.Msg.Drop}), nil
}
