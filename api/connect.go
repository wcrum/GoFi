package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
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

func (h *Handler) ListListeners(_ context.Context, _ *connect.Request[gofiv1.ListListenersRequest]) (*connect.Response[gofiv1.ListListenersResponse], error) {
	response := &gofiv1.ListListenersResponse{}
	for _, def := range h.ctrl.List() {
		item := &gofiv1.Listener{}
		if err := toProto(def, item); err != nil {
			return nil, rpcError(connect.CodeInternal, err)
		}
		response.Listeners = append(response.Listeners, item)
	}
	return connect.NewResponse(response), nil
}
func (h *Handler) GetListener(_ context.Context, req *connect.Request[gofiv1.GetListenerRequest]) (*connect.Response[gofiv1.GetListenerResponse], error) {
	def, found := h.ctrl.Get(req.Msg.Name)
	if !found {
		return nil, rpcError(connect.CodeNotFound, errors.New("listener not found"))
	}
	item := &gofiv1.Listener{}
	if err := toProto(def, item); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.GetListenerResponse{Listener: item}), nil
}
func (h *Handler) UpsertListener(_ context.Context, req *connect.Request[gofiv1.UpsertListenerRequest]) (*connect.Response[gofiv1.UpsertListenerResponse], error) {
	if req.Msg.Listener == nil {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("listener is required"))
	}
	var def spec.ListenerDefinition
	if err := fromProto(req.Msg.Listener, &def); err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	if err := h.ctrl.Validate(def); err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	if err := h.ctrl.Add(def); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	slog.Info("listener configured via ConnectRPC", "name", def.Name, "port", def.Source.Port)
	return connect.NewResponse(&gofiv1.UpsertListenerResponse{Listener: req.Msg.Listener}), nil
}
func (h *Handler) DeleteListener(_ context.Context, req *connect.Request[gofiv1.DeleteListenerRequest]) (*connect.Response[gofiv1.DeleteListenerResponse], error) {
	if _, found := h.ctrl.Get(req.Msg.Name); !found {
		return nil, rpcError(connect.CodeNotFound, errors.New("listener not found"))
	}
	if err := h.ctrl.Remove(req.Msg.Name); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.DeleteListenerResponse{}), nil
}
func (h *Handler) GetRuntimeStatus(ctx context.Context, req *connect.Request[gofiv1.GetRuntimeStatusRequest]) (*connect.Response[gofiv1.GetRuntimeStatusResponse], error) {
	runtime, found, err := h.ctrl.RuntimeStatus(ctx, req.Msg.Name)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	if !found {
		return nil, rpcError(connect.CodeNotFound, errors.New("listener runtime not found"))
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
	return connect.NewResponse(&gofiv1.GetModeResponse{Mode: string(h.mode), ProcessRole: "controller", DriverKind: h.ctrl.DriverKind()}), nil
}
func (h *Handler) Simulate(_ context.Context, req *connect.Request[gofiv1.SimulateRequest]) (*connect.Response[gofiv1.SimulateResponse], error) {
	if req.Msg.Listener == nil {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("listener is required"))
	}
	var def spec.ListenerDefinition
	if err := fromProto(req.Msg.Listener, &def); err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	result, err := pipeline.Simulate(def, []byte(req.Msg.Raw))
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	item := &gofiv1.SimulateResult{}
	if err := toProto(result, item); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.SimulateResponse{Result: item}), nil
}
