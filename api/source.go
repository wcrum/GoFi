package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/functionrpc"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/sandbox"
	"github.com/wcrum/gofi/spec"
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
	if err := validKind(req.Msg.Kind); err != nil {
		return nil, err
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
	runID, err := runIdentifier(req.Msg.RunId)
	if err != nil {
		return nil, err
	}
	call := connect.NewRequest(&gofiv1.WorkerInvokeRequest{Name: req.Msg.Name, Kind: req.Msg.Kind, Raw: req.Msg.Raw, Fields: req.Msg.Fields, Meta: req.Msg.Meta, Config: req.Msg.Config, InvocationId: runID, TraceLines: req.Msg.TraceLines})
	call.Header().Set("Authorization", "Bearer "+binding.Token)
	result, err := client.Invoke(callCtx, call)
	if err != nil {
		return nil, err
	}
	return runResponse(result.Msg, runID)
}

// runIdentifier validates a client run id (it correlates live worker log
// lines and names RunSource workers), generating one when empty.
func runIdentifier(runID string) (string, error) {
	if runID == "" {
		return functionrpc.NewInvocationID(), nil
	}
	if len(runID) > 64 || sandbox.ScratchName(runID) == "" {
		return "", rpcError(connect.CodeInvalidArgument, errors.New("run_id must be 1-64 characters and contain letters or digits"))
	}
	return runID, nil
}

// runResponse relays a worker result. User-code errors are part of the
// response (with stdout, duration, and source location), not RPC failures.
func runResponse(result *gofiv1.WorkerInvokeResponse, runID string) (*connect.Response[gofiv1.RunFunctionResponse], error) {
	if proto.Size(result) > maxPythonSampleBytes+(320<<10) {
		return nil, rpcError(connect.CodeResourceExhausted, errors.New("function result exceeds 1 MiB"))
	}
	return connect.NewResponse(&gofiv1.RunFunctionResponse{Fields: result.Fields, Raw: result.Raw, Drop: result.Drop, Stdout: result.Stdout, Stderr: result.Stderr, DurationUs: result.DurationUs, Error: result.Error, ErrorLocation: result.ErrorLocation,
		EntryLine: result.EntryLine, ExecutedLines: result.ExecutedLines, RunId: runID}), nil
}

func validKind(kind string) error {
	if slices.Contains(spec.FunctionKinds, kind) {
		return nil
	}
	return rpcError(connect.CodeInvalidArgument, fmt.Errorf("function kind must be one of %s", strings.Join(spec.FunctionKinds, ", ")))
}

// RunSource executes unsaved editor source in a dedicated, short-lived
// worker with saved-library isolation. Static validation runs first so
// syntax errors are reported without starting a worker.
func (h *sandboxHandler) RunSource(ctx context.Context, req *connect.Request[gofiv1.RunSourceRequest]) (*connect.Response[gofiv1.RunFunctionResponse], error) {
	if err := sourceSizeValid(req.Msg.Source); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.Source) == "" {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("source is required"))
	}
	if err := validKind(req.Msg.Kind); err != nil {
		return nil, err
	}
	if len(req.Msg.Name) > 128 {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("function name exceeds 128 characters"))
	}
	if proto.Size(req.Msg) > maxPythonSampleBytes+maxPythonSourceBytes {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("function sample exceeds 1 MiB"))
	}
	definition, err := sandbox.Normalize(sandbox.Definition{Name: "run-check", SourceCode: req.Msg.Source, TimeoutSeconds: int(req.Msg.TimeoutSeconds), MemoryMiB: int(req.Msg.MemoryMib), AI: req.Msg.Ai})
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	validation, err := h.manager.ValidateStatic(ctx, req.Msg.Source)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	runID, err := runIdentifier(req.Msg.RunId)
	if err != nil {
		return nil, err
	}
	if !validation.Valid && len(validation.Diagnostics) > 0 {
		first := validation.Diagnostics[0]
		return connect.NewResponse(&gofiv1.RunFunctionResponse{Error: fmt.Sprintf("%s: %s", first.Code, first.Message), ErrorLocation: first, RunId: runID}), nil
	}
	var result *gofiv1.WorkerInvokeResponse
	err = h.manager.RunScratchAs(ctx, runID, definition, func(ctx context.Context, binding sandbox.Binding) error {
		client, closeIdle := workerClient(binding)
		defer closeIdle()
		call := connect.NewRequest(&gofiv1.WorkerInvokeRequest{Name: req.Msg.Name, Kind: req.Msg.Kind, Raw: req.Msg.Raw, Fields: req.Msg.Fields, Meta: req.Msg.Meta, Config: req.Msg.Config, InvocationId: runID, TraceLines: req.Msg.TraceLines})
		call.Header().Set("Authorization", "Bearer "+binding.Token)
		callCtx, done := context.WithTimeout(ctx, time.Duration(definition.TimeoutSeconds+5)*time.Second)
		defer done()
		response, err := client.Invoke(callCtx, call)
		if err == nil {
			result = response.Msg
		}
		return err
	})
	if errors.Is(err, sandbox.ErrRunInUse) {
		return nil, rpcError(connect.CodeAlreadyExists, err)
	}
	if err != nil {
		if code := connect.CodeOf(err); code != connect.CodeUnknown {
			return nil, err
		}
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	return runResponse(result, runID)
}
