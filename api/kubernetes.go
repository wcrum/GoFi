package api

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/clusterinfo"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/spec"
)

// kubernetesHandler serves cluster networking discovery, connectivity
// analysis, and GoFi's own UDS Package in Kubernetes mode.
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
	AnalyzeConnectivity(ctx context.Context, def spec.FlowDefinition, all []spec.FlowDefinition, runtime driver.ConnectivityRuntime) (*gofiv1.AnalyzeConnectivityResponse, error)
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
	// What the running flow saw applies only while it routes like this one.
	var runtime driver.ConnectivityRuntime
	var note string
	if saved, found := h.ctrl.Get(def.Name); found && sameRoutes(saved, def) {
		runtime.Saved = true
		observed, err := routeHealth(ctx, h.ctrl, def.Name)
		if err != nil {
			note = "The running flow could not be asked what it observed: " + err.Error()
		}
		runtime.Observed = observed
	} else if found {
		note = "Observed send results are hidden while the flow has unsaved route changes."
	}
	out, err := analyzer.AnalyzeConnectivity(ctx, def, h.ctrl.Flows(), runtime)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	if note != "" {
		out.Limits = append(out.Limits, note)
	}
	return connect.NewResponse(out), nil
}

// packagePlanner plans and applies GoFi's own UDS Package for a flow set.
type packagePlanner interface {
	PlanUDSPackage(ctx context.Context, flows []spec.FlowDefinition) *gofiv1.PlanUDSPackageResponse
	ApplyUDSPackage(ctx context.Context, flows []spec.FlowDefinition, digest string) (*gofiv1.PlanUDSPackageResponse, error)
}

func (h *kubernetesHandler) planner() (packagePlanner, error) {
	planner, ok := h.ctrl.Driver().(packagePlanner)
	if !ok {
		return nil, rpcError(connect.CodeFailedPrecondition, errors.New("UDS Packages are available only in Kubernetes mode"))
	}
	return planner, nil
}

func (h *kubernetesHandler) PlanUDSPackage(ctx context.Context, _ *connect.Request[gofiv1.PlanUDSPackageRequest]) (*connect.Response[gofiv1.PlanUDSPackageResponse], error) {
	planner, err := h.planner()
	if err != nil {
		return nil, err
	}
	// The same valid flow set the reconcile loop applies.
	var plan *gofiv1.PlanUDSPackageResponse
	_ = h.ctrl.WithFlowSet(func(flows []spec.FlowDefinition) error { plan = planner.PlanUDSPackage(ctx, flows); return nil })
	return connect.NewResponse(plan), nil
}

func (h *kubernetesHandler) ApplyUDSPackage(ctx context.Context, req *connect.Request[gofiv1.ApplyUDSPackageRequest]) (*connect.Response[gofiv1.ApplyUDSPackageResponse], error) {
	planner, err := h.planner()
	if err != nil {
		return nil, err
	}
	var plan *gofiv1.PlanUDSPackageResponse
	err = h.ctrl.WithFlowSet(func(flows []spec.FlowDefinition) (err error) {
		plan, err = planner.ApplyUDSPackage(ctx, flows, req.Msg.Digest)
		return err
	})
	if err != nil {
		var refused *driver.PackageRefusal
		if errors.As(err, &refused) {
			return nil, rpcError(connect.CodeFailedPrecondition, err)
		}
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	slog.Info("GoFi UDS Package applied via ConnectRPC", "namespace", plan.Namespace, "name", plan.Name)
	return connect.NewResponse(&gofiv1.ApplyUDSPackageResponse{Plan: plan}), nil
}
