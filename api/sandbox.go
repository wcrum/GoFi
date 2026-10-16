package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/sandbox"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type sandboxHandler struct{ manager *sandbox.Supervisor }

func (h *sandboxHandler) ListSandboxes(_ context.Context, _ *connect.Request[gofiv1.ListSandboxesRequest]) (*connect.Response[gofiv1.ListSandboxesResponse], error) {
	out := &gofiv1.ListSandboxesResponse{}
	for _, d := range h.manager.List() {
		item := &gofiv1.SandboxDefinition{}
		if err := toProto(d, item); err != nil {
			return nil, rpcError(connect.CodeInternal, err)
		}
		out.Sandboxes = append(out.Sandboxes, item)
		st, _ := h.manager.Status(d.Name)
		status := &gofiv1.SandboxStatus{}
		_ = toProto(st, status)
		out.Statuses = append(out.Statuses, status)
	}
	return connect.NewResponse(out), nil
}
func (h *sandboxHandler) GetSandbox(_ context.Context, req *connect.Request[gofiv1.GetSandboxRequest]) (*connect.Response[gofiv1.GetSandboxResponse], error) {
	d, ok := h.manager.Get(req.Msg.Name)
	if !ok {
		return nil, rpcError(connect.CodeNotFound, errors.New("Python library not found"))
	}
	item := &gofiv1.SandboxDefinition{}
	_ = toProto(d, item)
	st, _ := h.manager.Status(d.Name)
	status := &gofiv1.SandboxStatus{}
	_ = toProto(st, status)
	return connect.NewResponse(&gofiv1.GetSandboxResponse{Sandbox: item, Status: status}), nil
}
func (h *sandboxHandler) ApplySandbox(ctx context.Context, req *connect.Request[gofiv1.ApplySandboxRequest]) (*connect.Response[gofiv1.ApplySandboxResponse], error) {
	if req.Msg.Sandbox == nil {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("sandbox is required"))
	}
	var d sandbox.Definition
	if err := fromProto(req.Msg.Sandbox, &d); err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	if existing, ok := h.manager.Get(d.Name); ok && existing.Managed() {
		return nil, rpcError(connect.CodeFailedPrecondition, errors.New("this library is generated from a flow's inline stages; edit the flow instead"))
	}
	normalized, err := sandbox.NormalizeUser(d)
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err = h.manager.Apply(ctx, normalized); err != nil {
		if _, saved := h.manager.Get(d.Name); !saved {
			return nil, rpcError(connect.CodeUnavailable, err)
		}
	}
	item := &gofiv1.SandboxDefinition{}
	_ = toProto(normalized, item)
	st, _ := h.manager.Status(d.Name)
	status := &gofiv1.SandboxStatus{}
	_ = toProto(st, status)
	return connect.NewResponse(&gofiv1.ApplySandboxResponse{Sandbox: item, Status: status}), nil
}
func (h *sandboxHandler) DeleteSandbox(ctx context.Context, req *connect.Request[gofiv1.DeleteSandboxRequest]) (*connect.Response[gofiv1.DeleteSandboxResponse], error) {
	existing, ok := h.manager.Get(req.Msg.Name)
	if !ok {
		return nil, rpcError(connect.CodeNotFound, errors.New("Python library not found"))
	}
	if existing.Managed() {
		return nil, rpcError(connect.CodeFailedPrecondition, errors.New("this library is generated from a flow's inline stages; remove them from the flow instead"))
	}
	if err := h.manager.Delete(ctx, req.Msg.Name); err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&gofiv1.DeleteSandboxResponse{}), nil
}
func (h *sandboxHandler) ListFunctions(ctx context.Context, req *connect.Request[gofiv1.ListLibraryFunctionsRequest]) (*connect.Response[gofiv1.ListLibraryFunctionsResponse], error) {
	bindings, err := h.manager.Bindings(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	out := &gofiv1.ListLibraryFunctionsResponse{}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	for _, d := range h.manager.List() {
		if req.Msg.Library != "" && req.Msg.Library != d.Name {
			continue
		}
		binding, ready := bindings[d.Name]
		if !ready {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		call := connect.NewRequest(&gofiv1.WorkerListFunctionsRequest{})
		call.Header().Set("Authorization", "Bearer "+binding.Token)
		res, err := gofiv1connect.NewWorkerServiceClient(&http.Client{Transport: transport}, binding.Endpoint, connect.WithReadMaxBytes(1<<20)).ListFunctions(callCtx, call)
		cancel()
		if err != nil {
			continue
		}
		for _, f := range res.Msg.Functions {
			out.Functions = append(out.Functions, &gofiv1.LibraryFunction{Library: d.Name, Name: f.Name, Kind: f.Kind, Description: f.Description, Params: f.Params})
		}
	}
	return connect.NewResponse(out), nil
}

func (h *sandboxHandler) DescribeEnvironment(ctx context.Context, _ *connect.Request[gofiv1.DescribePythonEnvironmentRequest]) (*connect.Response[gofiv1.DescribePythonEnvironmentResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	env, err := h.manager.DescribeRuntime(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(env), nil
}

// WatchWorkerLogs follows a library's worker, or one RunSource run's worker,
// live. Lines are relayed as they are written; the stream ends when the
// client disconnects, the library is deleted, or the run's worker exits.
func (h *sandboxHandler) WatchWorkerLogs(ctx context.Context, req *connect.Request[gofiv1.WatchWorkerLogsRequest], stream *connect.ServerStream[gofiv1.WorkerLogEntry]) error {
	library, runID := req.Msg.Library, req.Msg.RunId
	if (library == "") == (runID == "") {
		return rpcError(connect.CodeInvalidArgument, errors.New("set exactly one of library or run_id"))
	}
	if req.Msg.TailLines < 0 || req.Msg.TailLines > sandbox.MaxLogTail {
		return rpcError(connect.CodeInvalidArgument, fmt.Errorf("tail_lines must be between 0 and %d", sandbox.MaxLogTail))
	}
	if runID != "" && (len(runID) > 64 || sandbox.ScratchName(runID) == "") {
		return rpcError(connect.CodeInvalidArgument, errors.New("run_id must be 1-64 characters and contain letters or digits"))
	}
	err := h.manager.WatchLogs(ctx, library, runID, int(req.Msg.TailLines), func(line sandbox.LogLine) error {
		return stream.Send(&gofiv1.WorkerLogEntry{Time: timestamppb.New(line.Time), Stream: line.Stream, Line: line.Line, InvocationId: line.InvocationID, Function: line.Function, Kind: line.Kind, Level: line.Level})
	})
	switch {
	case errors.Is(err, sandbox.ErrLibraryNotFound):
		return rpcError(connect.CodeNotFound, err)
	case err != nil && ctx.Err() == nil:
		return rpcError(connect.CodeUnavailable, err)
	}
	return nil
}
