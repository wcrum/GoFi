package api

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/clusterinfo"
	"github.com/wcrum/gofi/controller"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/spec"
)

// kubernetesHandler serves cluster networking discovery in Kubernetes mode.
// The other KubernetesService RPCs are not implemented yet.
type kubernetesHandler struct {
	gofiv1connect.UnimplementedKubernetesServiceHandler
	ctrl *controller.Controller
}

func (h *kubernetesHandler) cluster() (*clusterinfo.Client, error) {
	if source, ok := h.ctrl.Driver().(interface{ Cluster() *clusterinfo.Client }); ok && source.Cluster() != nil {
		return source.Cluster(), nil
	}
	return nil, rpcError(connect.CodeFailedPrecondition, errors.New("cluster networking is available only in Kubernetes mode"))
}

func (h *kubernetesHandler) DescribeCluster(ctx context.Context, _ *connect.Request[gofiv1.DescribeClusterRequest]) (*connect.Response[gofiv1.DescribeClusterResponse], error) {
	cluster, err := h.cluster()
	if err != nil {
		return nil, err
	}
	out, err := cluster.Describe(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(out), nil
}

func (h *kubernetesHandler) ListClusterNetworking(ctx context.Context, req *connect.Request[gofiv1.ListClusterNetworkingRequest]) (*connect.Response[gofiv1.ListClusterNetworkingResponse], error) {
	cluster, err := h.cluster()
	if err != nil {
		return nil, err
	}
	flows := clusterinfo.FlowLabels{}
	for _, def := range h.ctrl.List() {
		flows[def.Name] = map[string]string{"gofi.io/managed-by": "true", "gofi.io/listener": def.Name}
	}
	out, err := cluster.Networking(ctx, req.Msg.Namespace, flows)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(out), nil
}

func (h *kubernetesHandler) ListUDSPackages(ctx context.Context, _ *connect.Request[gofiv1.ListUDSPackagesRequest]) (*connect.Response[gofiv1.ListUDSPackagesResponse], error) {
	cluster, err := h.cluster()
	if err != nil {
		return nil, err
	}
	out, err := cluster.Packages(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(out), nil
}

// connectivityAnalyzer is the Kubernetes driver's UDS path verification.
type connectivityAnalyzer interface {
	AnalyzeConnectivity(ctx context.Context, def spec.FlowDefinition, all []spec.FlowDefinition) (*gofiv1.AnalyzeConnectivityResponse, error)
}

func (h *kubernetesHandler) AnalyzeConnectivity(ctx context.Context, req *connect.Request[gofiv1.AnalyzeConnectivityRequest]) (*connect.Response[gofiv1.AnalyzeConnectivityResponse], error) {
	analyzer, ok := h.ctrl.Driver().(connectivityAnalyzer)
	if !ok {
		return nil, rpcError(connect.CodeFailedPrecondition, errors.New("connectivity analysis is available only in Kubernetes mode"))
	}
	if req.Msg.Flow == nil {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("flow is required"))
	}
	var def spec.FlowDefinition
	if err := fromProto(req.Msg.Flow, &def); err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	out, err := analyzer.AnalyzeConnectivity(ctx, def, h.ctrl.List())
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(out), nil
}
