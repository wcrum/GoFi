package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/sandbox"
	"github.com/wcrum/gofi/spec"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	maxDebugWatches    = 20
	maxDebugWatchBytes = 500
	maxDebugSteps      = 20000
	// A recording is bounded to 4 MiB in the worker; allow result and framing.
	maxDebugResponseBytes = 8 << 20
)

// debugClient is a worker client for one debug call: recordings are larger
// than normal results, and the response arrives only when the run ends.
func debugClient(binding sandbox.Binding, timeout time.Duration) (gofiv1connect.WorkerServiceClient, func()) {
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: timeout}
	return gofiv1connect.NewWorkerServiceClient(&http.Client{Transport: transport}, binding.Endpoint, connect.WithReadMaxBytes(maxDebugResponseBytes), connect.WithSendMaxBytes(2<<20)), transport.CloseIdleConnections
}

func callDebug(ctx context.Context, binding sandbox.Binding, request *gofiv1.WorkerDebugRequest) (*gofiv1.DebugRecording, error) {
	timeout := time.Duration(max(binding.TimeoutSeconds, 1)+5) * time.Second
	client, closeIdle := debugClient(binding, timeout)
	defer closeIdle()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	call := connect.NewRequest(request)
	call.Header().Set("Authorization", "Bearer "+binding.Token)
	response, err := client.Debug(ctx, call)
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func validDebugOptions(watches []string, maxSteps int32) error {
	if len(watches) > maxDebugWatches {
		return rpcError(connect.CodeInvalidArgument, fmt.Errorf("at most %d watch expressions", maxDebugWatches))
	}
	for _, watch := range watches {
		if len(watch) > maxDebugWatchBytes {
			return rpcError(connect.CodeInvalidArgument, fmt.Errorf("watch expressions are limited to %d bytes", maxDebugWatchBytes))
		}
	}
	if maxSteps < 0 || maxSteps > maxDebugSteps {
		return rpcError(connect.CodeInvalidArgument, fmt.Errorf("max_steps must be between 0 and %d", maxDebugSteps))
	}
	return nil
}

// recordSaved debugs a function of a saved, running library.
func recordSaved(ctx context.Context, manager *sandbox.Supervisor, library string, request *gofiv1.WorkerDebugRequest) (*gofiv1.DebugRecording, error) {
	if _, found := manager.Get(library); !found {
		return nil, rpcError(connect.CodeNotFound, errors.New("Python library not found"))
	}
	bindings, err := manager.Bindings(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	binding, ready := bindings[library]
	if !ready {
		return nil, rpcError(connect.CodeFailedPrecondition, errors.New("Python library worker is not ready; save and launch the library first"))
	}
	return callDebug(ctx, binding, request)
}

// recordSource debugs unsaved source in a short-lived worker named from the
// run id (so WatchWorkerLogs can follow it), after static validation.
func recordSource(ctx context.Context, manager *sandbox.Supervisor, source string, timeoutSeconds, memoryMiB int, ai bool, runID string, request *gofiv1.WorkerDebugRequest) (*gofiv1.DebugRecording, error) {
	definition, err := sandbox.Normalize(sandbox.Definition{Name: "run-check", SourceCode: source, TimeoutSeconds: timeoutSeconds, MemoryMiB: memoryMiB, AI: ai})
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	validation, err := manager.ValidateStatic(ctx, source)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	if !validation.Valid && len(validation.Diagnostics) > 0 {
		first := validation.Diagnostics[0]
		return &gofiv1.DebugRecording{Result: &gofiv1.RunFunctionResponse{Error: fmt.Sprintf("%s: %s", first.Code, first.Message), ErrorLocation: first, RunId: runID}, RunId: runID}, nil
	}
	var recording *gofiv1.DebugRecording
	err = manager.RunScratchAs(ctx, runID, definition, func(ctx context.Context, binding sandbox.Binding) error {
		recording, err = callDebug(ctx, binding, request)
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
	return recording, nil
}

func finishRecording(recording *gofiv1.DebugRecording, runID string) (*gofiv1.DebugRecording, error) {
	if proto.Size(recording) > maxDebugResponseBytes {
		return nil, rpcError(connect.CodeResourceExhausted, errors.New("debug recording exceeds 8 MiB"))
	}
	recording.RunId = runID
	if recording.Result != nil {
		recording.Result.RunId = runID
	}
	return recording, nil
}

// DebugFunction records one bounded, traced execution of a saved library
// function or of unsaved source, for step-through replay in the portal.
func (h *sandboxHandler) DebugFunction(ctx context.Context, req *connect.Request[gofiv1.DebugFunctionRequest]) (*connect.Response[gofiv1.DebugRecording], error) {
	m := req.Msg
	if (m.Library == "") == (strings.TrimSpace(m.Source) == "") {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("set exactly one of library or source"))
	}
	if len(m.Name) > 128 || (m.Library != "" && strings.TrimSpace(m.Name) == "") {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("a function name (up to 128 characters) is required"))
	}
	if err := validKind(m.Kind); err != nil {
		return nil, err
	}
	if err := sourceSizeValid(m.Source); err != nil {
		return nil, err
	}
	if proto.Size(m) > maxPythonSampleBytes+maxPythonSourceBytes+64<<10 {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("function sample exceeds 1 MiB"))
	}
	if err := validDebugOptions(m.Watches, m.MaxSteps); err != nil {
		return nil, err
	}
	runID, err := runIdentifier(m.RunId)
	if err != nil {
		return nil, err
	}
	request := &gofiv1.WorkerDebugRequest{Name: m.Name, Kind: m.Kind, Raw: m.Raw, Fields: m.Fields, Meta: m.Meta, Config: m.Config, Watches: m.Watches, MaxSteps: m.MaxSteps, InvocationId: runID}
	var recording *gofiv1.DebugRecording
	if m.Library != "" {
		recording, err = recordSaved(ctx, h.manager, m.Library, request)
	} else {
		ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		recording, err = recordSource(ctx, h.manager, m.Source, int(m.TimeoutSeconds), 0, m.Ai, runID, request)
	}
	if err != nil {
		return nil, err
	}
	recording, err = finishRecording(recording, runID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(recording), nil
}

// DebugFrame dry-runs a captured frame through a listener up to one Python
// step (nothing is sent to any sink), then records that step's execution
// with the exact input it received there.
func (h *Handler) DebugFrame(ctx context.Context, req *connect.Request[gofiv1.DebugFrameRequest]) (*connect.Response[gofiv1.DebugFrameResponse], error) {
	m := req.Msg
	if h.sandbox == nil {
		return nil, rpcError(connect.CodeUnavailable, errors.New("Python workers are not configured in this deployment"))
	}
	if len(m.Raw) > maxPythonSampleBytes {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("frame exceeds 1 MiB"))
	}
	if err := validDebugOptions(m.Watches, m.MaxSteps); err != nil {
		return nil, err
	}
	var def spec.FlowDefinition
	switch {
	case m.Flow != nil && m.FlowName != "":
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("set exactly one of flow_name or flow"))
	case m.Flow != nil:
		var err error
		if def, err = h.receivedFlow(m.Flow); err != nil {
			return nil, rpcError(connect.CodeInvalidArgument, err)
		}
	case m.FlowName != "":
		stored, found := h.ctrl.Get(m.FlowName)
		if !found {
			return nil, rpcError(connect.CodeNotFound, errors.New("flow not found"))
		}
		def = stored
	default:
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("flow_name or flow is required"))
	}
	resolved, err := h.ctrl.Resolve(def)
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	simulation, target, notReached, err := pipeline.SimulateTo(resolved, m.Raw, m.Step)
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	out := &gofiv1.DebugFrameResponse{NotReached: notReached}
	if target == nil {
		step, _, _ := pipeline.ResolveDebugStep(resolved, m.Step)
		out.Step = step
	} else {
		out.Step = target.Step
		if err := h.debugTarget(ctx, target, m, out, simulation); err != nil {
			return nil, err
		}
	}
	out.Simulation = &gofiv1.SimulateResult{}
	if err := toProto(simulation, out.Simulation); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) debugTarget(ctx context.Context, target *pipeline.DebugTarget, m *gofiv1.DebugFrameRequest, out *gofiv1.DebugFrameResponse, simulation *pipeline.SimulateResult) error {
	fields, err := structpb.NewStruct(target.Fields)
	if err != nil {
		return rpcError(connect.CodeInvalidArgument, fmt.Errorf("fields at %s: %w", target.Step, err))
	}
	meta := map[string]any{}
	for k, v := range target.Meta {
		meta[k] = v
	}
	metaStruct, _ := structpb.NewStruct(meta)
	ref := target.Ref
	out.InputFields, out.InputMeta, out.InputRaw = fields, target.Meta, target.Raw
	out.Function = &gofiv1.PythonRef{}
	if err := toProto(ref, out.Function); err != nil {
		return rpcError(connect.CodeInternal, err)
	}
	runID, _ := runIdentifier("")
	request := &gofiv1.WorkerDebugRequest{Name: ref.Function, Kind: target.Kind, Raw: target.Raw, Fields: fields, Meta: metaStruct, Watches: m.Watches, MaxSteps: m.MaxSteps, InvocationId: runID}
	if len(ref.Config) > 0 {
		if request.Config, err = structpb.NewStruct(ref.Config); err != nil {
			return rpcError(connect.CodeInvalidArgument, fmt.Errorf("python.config: %w", err))
		}
	}
	var recording *gofiv1.DebugRecording
	switch {
	case ref.InlineSource != "":
		// An inline stage runs its own (possibly unsaved) snippet: the ready
		// managed worker when it already runs this exact source, else a scratch worker.
		request.Name = target.Step
		if binding, ok := h.sandbox.InlineBinding(target.Step, ref.InlineSource); ok {
			recording, err = callDebug(ctx, binding, request)
		} else {
			ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()
			recording, err = recordSource(ctx, h.sandbox, "import gofi as _gofi\n"+sandbox.WrapInline(target.Step, ref.InlineSource), 0, 0, false, runID, request)
		}
	case ref.Library != "":
		recording, err = recordSaved(ctx, h.sandbox, ref.Library, request)
	default:
		return rpcError(connect.CodeFailedPrecondition, fmt.Errorf("step %s has no Python function", target.Step))
	}
	if err != nil {
		return err
	}
	if out.Recording, err = finishRecording(recording, runID); err != nil {
		return err
	}
	// The simulation includes the debugged step itself, from the recording.
	if result := recording.Result; result != nil && target.Kind == "transformer" {
		trace := pipeline.StageTrace{ID: target.Step, Kind: "python", Pass: result.Error == "" && !result.Drop, Error: result.Error, Stdout: result.Stdout, DurationUs: result.DurationUs, Fields: target.Fields}
		if result.Fields != nil && result.Error == "" {
			trace.Fields = result.Fields.AsMap()
		}
		simulation.Stages = append(simulation.Stages, trace)
		if !trace.Pass {
			simulation.Dropped, simulation.DroppedBy = true, target.Step
		}
	}
	return nil
}
