package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/spec"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// The domain contract is also persisted as Kubernetes JSON/YAML. Protobuf JSON
// supplies one lossless bridge instead of maintaining duplicate field mappings.
func toProto(value any, out proto.Message) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return protojson.Unmarshal(data, out)
}
func fromProto(value proto.Message, out any) error {
	data, err := protojson.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func rpcError(code connect.Code, err error) error { return connect.NewError(code, err) }

// flowSnapshot caches the ListFlows response for one controller revision:
// the definitions are converted once per change, not once per poll, and a
// poller that already has this revision gets an `unchanged` reply.
type flowSnapshot struct {
	mu       sync.Mutex
	revision uint64
	response *gofiv1.ListFlowsResponse
}

// current returns the response for the controller's current revision,
// rebuilding it (outside the controller lock) when the revision moved.
func (s *flowSnapshot) current(ctrl *controller.Controller) (*gofiv1.ListFlowsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	revision := ctrl.Revision()
	if s.response != nil && s.revision == revision {
		return s.response, nil
	}
	defs := ctrl.List() // clones under the controller's read lock; conversion below is lock-free
	response := &gofiv1.ListFlowsResponse{Revision: revision, Flows: make([]*gofiv1.Flow, 0, len(defs))}
	for _, def := range defs {
		item, err := shownFlow(def)
		if err != nil {
			return nil, err
		}
		response.Flows = append(response.Flows, item)
	}
	// A save between Revision() and List() is caught by the next poll: the
	// snapshot is tagged with the revision read before listing.
	s.revision, s.response = revision, response
	return response, nil
}

func (h *Handler) ListFlows(_ context.Context, req *connect.Request[gofiv1.ListFlowsRequest]) (*connect.Response[gofiv1.ListFlowsResponse], error) {
	response, err := h.flows.current(h.ctrl)
	if err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	if req.Msg.IfRevision != 0 && req.Msg.IfRevision == response.Revision {
		return connect.NewResponse(&gofiv1.ListFlowsResponse{Revision: response.Revision, Unchanged: true}), nil
	}
	return connect.NewResponse(response), nil
}

// shownFlow converts a definition for a read response: people see inline
// Python as code (not its managed library) and credentials as spec.Redacted.
// The store, history files, and FLOW_SPEC keep the real values.
func shownFlow(def spec.FlowDefinition) (*gofiv1.Flow, error) {
	def.StripDerivedPython()
	def = spec.Redact(def)
	item := &gofiv1.Flow{}
	if err := toProto(def, item); err != nil {
		return nil, err
	}
	return item, nil
}

// receivedFlow decodes a definition a client sent. A client that edits what
// a read showed sends spec.Redacted where a credential was; those positions
// take the stored flow's values, so a redacted read round-trips.
func (h *Handler) receivedFlow(msg *gofiv1.Flow) (spec.FlowDefinition, error) {
	var def spec.FlowDefinition
	if err := fromProto(msg, &def); err != nil {
		return def, err
	}
	if !spec.HasRedacted(def) {
		return def, nil
	}
	stored, _ := h.ctrl.Get(def.Name) // zero value when new: every sentinel is then an error
	return spec.MergeRedacted(def, stored)
}

func (h *Handler) GetFlow(_ context.Context, req *connect.Request[gofiv1.GetFlowRequest]) (*connect.Response[gofiv1.GetFlowResponse], error) {
	def, found := h.ctrl.Get(req.Msg.Name)
	if !found {
		return nil, rpcError(connect.CodeNotFound, errors.New("flow not found"))
	}
	item, err := shownFlow(def)
	if err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.GetFlowResponse{Flow: item}), nil
}
func (h *Handler) UpsertFlow(ctx context.Context, req *connect.Request[gofiv1.UpsertFlowRequest]) (*connect.Response[gofiv1.UpsertFlowResponse], error) {
	if req.Msg.Flow == nil {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("flow is required"))
	}
	def, err := h.receivedFlow(req.Msg.Flow)
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	if err := h.ctrl.Validate(def); err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	// The response shows the flow as a read would (credentials redacted).
	shown, err := shownFlow(def)
	if err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	if req.Msg.ValidateOnly {
		if err := h.ctrl.Check(ctx, def); err != nil {
			return nil, rpcError(connect.CodeInvalidArgument, err)
		}
		return connect.NewResponse(&gofiv1.UpsertFlowResponse{Flow: shown, Warnings: h.blockedPaths(ctx, def)}), nil
	}
	if err := saveWithHistory(ctx, h.history, def.Name, req.Header(), req.Msg.Message, func() error { return h.ctrl.Add(def) }); err != nil {
		if errors.As(err, new(controller.InvalidInlineError)) {
			return nil, rpcError(connect.CodeInvalidArgument, err)
		}
		return nil, rpcError(connect.CodeInternal, err)
	}
	slog.Info("flow configured via ConnectRPC", "name", def.Name)
	return connect.NewResponse(&gofiv1.UpsertFlowResponse{Flow: shown, Warnings: h.blockedPaths(ctx, def)}), nil
}
func (h *Handler) DeleteFlow(ctx context.Context, req *connect.Request[gofiv1.DeleteFlowRequest]) (*connect.Response[gofiv1.DeleteFlowResponse], error) {
	if _, found := h.ctrl.Get(req.Msg.Name); !found {
		return nil, rpcError(connect.CodeNotFound, errors.New("flow not found"))
	}
	if err := saveWithHistory(ctx, h.history, req.Msg.Name, req.Header(), "", func() error { return h.ctrl.Remove(req.Msg.Name) }); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.DeleteFlowResponse{}), nil
}

func (h *Handler) GetRuntimeStatus(ctx context.Context, req *connect.Request[gofiv1.GetRuntimeStatusRequest]) (*connect.Response[gofiv1.GetRuntimeStatusResponse], error) {
	runtime, found, err := h.ctrl.RuntimeStatus(ctx, req.Msg.Name)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	if !found {
		return nil, rpcError(connect.CodeNotFound, errors.New("flow runtime not found"))
	}
	item := &gofiv1.RuntimeInfo{}
	if err := toProto(runtime, item); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.GetRuntimeStatusResponse{Runtime: item}), nil
}
func (h *Handler) ListEndpoints(ctx context.Context, _ *connect.Request[gofiv1.ListEndpointsRequest]) (*connect.Response[gofiv1.ListEndpointsResponse], error) {
	endpoints, err := h.ctrl.Endpoints(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	response := &gofiv1.ListEndpointsResponse{}
	for _, endpoint := range endpoints {
		item := &gofiv1.Endpoint{}
		if err := toProto(endpoint, item); err != nil {
			return nil, rpcError(connect.CodeInternal, err)
		}
		response.Endpoints = append(response.Endpoints, item)
	}
	return connect.NewResponse(response), nil
}
func (h *Handler) GetConfiguration(_ context.Context, _ *connect.Request[gofiv1.GetConfigurationRequest]) (*connect.Response[gofiv1.GetConfigurationResponse], error) {
	configuration := &gofiv1.Configuration{}
	if err := toProto(h.ctrl.Configuration(), configuration); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.GetConfigurationResponse{Configuration: configuration}), nil
}
func (h *Handler) GetMode(_ context.Context, _ *connect.Request[gofiv1.GetModeRequest]) (*connect.Response[gofiv1.GetModeResponse], error) {
	return connect.NewResponse(&gofiv1.GetModeResponse{Mode: string(h.mode), ProcessRole: "controller", DriverKind: h.ctrl.DriverKind(), NodePortMin: int32(spec.MinNodePort), NodePortMax: int32(spec.MaxNodePort)}), nil
}
func (h *Handler) Simulate(_ context.Context, req *connect.Request[gofiv1.SimulateRequest]) (*connect.Response[gofiv1.SimulateResponse], error) {
	if req.Msg.Flow == nil {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("flow is required"))
	}
	def, err := h.receivedFlow(req.Msg.Flow)
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	// Simulate exactly what the runtime would run: linked CEL library entries
	// are resolved to their current (or pinned) text first.
	resolved, err := h.ctrl.Resolve(def)
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	result, err := pipeline.Simulate(resolved, []byte(req.Msg.Raw))
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	item := &gofiv1.SimulateResult{}
	if err := toProto(result, item); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.SimulateResponse{Result: item}), nil
}
