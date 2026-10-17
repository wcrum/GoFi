package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/spec"
	"github.com/wcrum/gofi/tapstream"
)

func (h *Handler) GetRouteHealth(ctx context.Context, req *connect.Request[gofiv1.GetRouteHealthRequest]) (*connect.Response[gofiv1.GetRouteHealthResponse], error) {
	if _, found := h.ctrl.Get(req.Msg.Name); !found {
		return nil, rpcError(connect.CodeNotFound, errors.New("flow not found"))
	}
	routes, err := routeHealth(ctx, h.ctrl, req.Msg.Name)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&gofiv1.GetRouteHealthResponse{Routes: routes}), nil
}

// routeHealth asks a flow's runtime what it observed on its outbound routes:
// in this process for in-process listeners, otherwise over the listener's
// relay (the same address live traffic uses). A runtime that is not running,
// or predates route health, reports nothing.
func routeHealth(ctx context.Context, ctrl *controller.Controller, name string) ([]*gofiv1.RouteHealth, error) {
	lookupCtx, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	target, found, err := ctrl.Tap(lookupCtx, name)
	if err != nil || !found {
		return nil, err
	}
	if target.Local != nil {
		return tapstream.Routes(name), nil
	}
	if target.Addr == "" {
		return nil, nil
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	remote := gofiv1connect.NewGoFiServiceClient(&http.Client{Transport: transport, Timeout: 5 * time.Second}, "http://"+target.Addr)
	resp, err := remote.GetRouteHealth(lookupCtx, connect.NewRequest(&gofiv1.GetRouteHealthRequest{Name: name}))
	if connect.CodeOf(err) == connect.CodeUnimplemented {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return resp.Msg.Routes, nil
}

// sameRoutes reports whether two definitions of a flow connect to the same
// places in the same order, so route IDs observed on one describe the other.
func sameRoutes(a, b spec.FlowDefinition) bool {
	key := func(def spec.FlowDefinition) string {
		data, _ := json.Marshal(struct {
			Sinks      []spec.SinkCfg
			DeadLetter *spec.SinkCfg
			Source     spec.FlowSource
		}{def.Sinks, def.DeadLetterSink, def.Source})
		return string(data)
	}
	return a.Name == b.Name && key(a) == key(b)
}

// blockedPaths warns, after a save, about paths a Package blocks that GoFi
// will not fix itself. GoFi's own Package catches up on the reconcile the save
// triggers; another application's Package needs its owner. Best effort: an
// analysis that fails or is slow yields no warnings.
func (h *Handler) blockedPaths(ctx context.Context, def spec.FlowDefinition) []string {
	analyzer, ok := h.ctrl.Driver().(connectivityAnalyzer)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := analyzer.AnalyzeConnectivity(ctx, def, h.ctrl.Flows(), driver.ConnectivityRuntime{Saved: true})
	if err != nil {
		slog.Debug("save-time connectivity check", "flow", def.Name, "error", err)
		return nil
	}
	var warnings []string
	for _, path := range out.Paths {
		if path.Verdict != "blocked" {
			continue
		}
		ownedElsewhere := len(path.Fixes) == 0
		for _, fix := range path.Fixes {
			ownedElsewhere = ownedElsewhere || fix.Action != "apply-gofi-package"
		}
		if ownedElsewhere {
			warnings = append(warnings, fmt.Sprintf("%s → %s: %s", path.From, path.To, path.Summary))
		}
	}
	return warnings
}
