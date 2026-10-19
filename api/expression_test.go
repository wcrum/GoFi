package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/expressions"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/spec"
)

func newExpressionClients(t *testing.T) (gofiv1connect.ExpressionServiceClient, gofiv1connect.GoFiServiceClient, *controller.Controller) {
	t.Helper()
	ctrl := controller.New(driver.NewInProcess())
	ctrl.SetExpressions(expressions.NewManager(&expressions.FileStore{Path: filepath.Join(t.TempDir(), "expressions.yaml")}))
	handler := New(ctrl, nil, deploymode.ModeBinary)
	client := handlerClient{handler}
	return gofiv1connect.NewExpressionServiceClient(client, "http://gofi.test"), gofiv1connect.NewGoFiServiceClient(client, "http://gofi.test"), ctrl
}

func applyExpression(t *testing.T, c gofiv1connect.ExpressionServiceClient, e *gofiv1.CelExpression) (*gofiv1.ApplyExpressionResponse, error) {
	t.Helper()
	res, err := c.ApplyExpression(context.Background(), connect.NewRequest(&gofiv1.ApplyExpressionRequest{Expression: e}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func linkedListener(generation int64) spec.FlowDefinition {
	def := basicDefinition()
	def.Stages = []spec.StageSpec{{ID: "hot", Kind: "filter", Type: "cel", ExpressionRef: &spec.ExpressionRef{Name: "hot", Generation: generation}}}
	return def
}

func TestExpressionLibraryLifecycle(t *testing.T) {
	exprs, gofi, ctrl := newExpressionClients(t)
	ctx := context.Background()

	if _, err := applyExpression(t, exprs, &gofiv1.CelExpression{Name: "bad", Expression: `fields["a"] ==`}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("compile error: got %v", err)
	}
	created, err := applyExpression(t, exprs, &gofiv1.CelExpression{Name: "hot", Expression: `fields["t"] > 30.0`, Tags: []string{"temp"}})
	if err != nil || created.Expression.Generation != 1 || created.Expression.ResultType != "bool" {
		t.Fatalf("create: %+v %v", created, err)
	}

	// A listener may link the entry; a missing reference is rejected.
	if _, err := gofi.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, linkedListener(0))})); err != nil {
		t.Fatalf("link: %v", err)
	}
	missing := linkedListener(0)
	missing.Name, missing.Stages[0].ExpressionRef.Name = "other", "nope"
	if _, err := gofi.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, missing)})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("missing reference: got %v", err)
	}

	// Metadata edits keep the generation; text edits bump it and reload followers.
	meta, err := applyExpression(t, exprs, &gofiv1.CelExpression{Name: "hot", Expression: `fields["t"] > 30.0`, Description: "hot readings"})
	if err != nil || meta.Expression.Generation != 1 || len(meta.ReloadedFlows) != 0 {
		t.Fatalf("metadata edit: %+v %v", meta, err)
	}
	changed, err := applyExpression(t, exprs, &gofiv1.CelExpression{Name: "hot", Expression: `fields["t"] > 35.0`})
	if err != nil || changed.Expression.Generation != 2 || strings.Join(changed.ReloadedFlows, ",") != "route" || strings.Join(changed.Expression.UsedBy, ",") != "route" {
		t.Fatalf("text edit: %+v %v", changed, err)
	}
	resolved, err := ctrl.Resolve(linkedListener(0))
	if err != nil || resolved.Stages[0].Expression != `fields["t"] > 35.0` {
		t.Fatalf("resolution after edit: %q %v", resolved.Stages[0].Expression, err)
	}

	// Changing a linked filter's entry to a non-bool result would break it.
	if _, err := applyExpression(t, exprs, &gofiv1.CelExpression{Name: "hot", Expression: `fields["t"]`, ResultType: "any"}); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "route") {
		t.Fatalf("breaking change: got %v", err)
	}

	// Pinning keeps the pinned revision across later edits.
	if _, err := gofi.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, linkedListener(2))})); err != nil {
		t.Fatalf("pin: %v", err)
	}
	pinned, err := applyExpression(t, exprs, &gofiv1.CelExpression{Name: "hot", Expression: `fields["t"] > 40.0`})
	if err != nil || pinned.Expression.Generation != 3 || len(pinned.Expression.PinnedRevisions) != 1 || pinned.Expression.PinnedRevisions[0].Generation != 2 || len(pinned.ReloadedFlows) != 0 {
		t.Fatalf("pinned edit: %+v %v", pinned, err)
	}
	if resolved, _ = ctrl.Resolve(linkedListener(2)); resolved.Stages[0].Expression != `fields["t"] > 35.0` {
		t.Fatalf("pinned resolution: %q", resolved.Stages[0].Expression)
	}

	// Delete is refused while referenced, then allowed.
	if _, err := exprs.DeleteExpression(ctx, connect.NewRequest(&gofiv1.DeleteExpressionRequest{Name: "hot"})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("delete while used: got %v", err)
	}
	if _, err := gofi.DeleteFlow(ctx, connect.NewRequest(&gofiv1.DeleteFlowRequest{Name: "route"})); err != nil {
		t.Fatal(err)
	}
	if _, err := exprs.DeleteExpression(ctx, connect.NewRequest(&gofiv1.DeleteExpressionRequest{Name: "hot"})); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestExpressionCheckEvaluateAndDescribe(t *testing.T) {
	exprs, gofi, _ := newExpressionClients(t)
	ctx := context.Background()

	check, err := exprs.CheckExpression(ctx, connect.NewRequest(&gofiv1.CheckExpressionRequest{Expression: `fields["a"] ==`, ResultType: "bool"}))
	if err != nil || check.Msg.Valid || len(check.Msg.Diagnostics) == 0 {
		t.Fatalf("check: %+v %v", check, err)
	}

	fields, _ := structpb.NewStruct(map[string]any{"t": 31.5})
	eval, err := exprs.EvaluateExpression(ctx, connect.NewRequest(&gofiv1.EvaluateExpressionRequest{Expression: `fields["t"] > 30.0`, ResultType: "bool", Fields: fields}))
	if err != nil || eval.Msg.Error != "" || !eval.Msg.Value.GetBoolValue() || eval.Msg.Cost == 0 {
		t.Fatalf("evaluate fields: %+v %v", eval, err)
	}

	// With a listener, raw is decoded and parsed exactly as on ingress.
	def := basicDefinition()
	def.Decode = &spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: "base64"}}}
	if _, err := gofi.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, def)})); err != nil {
		t.Fatal(err)
	}
	eval, err = exprs.EvaluateExpression(ctx, connect.NewRequest(&gofiv1.EvaluateExpressionRequest{Expression: `fields["sensor"] + "!"`, Flow: "route", Raw: []byte("eyJzZW5zb3IiOiJhbHBoYSJ9")}))
	if err != nil || eval.Msg.Error != "" || eval.Msg.Value.GetStringValue() != "alpha!" || eval.Msg.BoundFields.AsMap()["sensor"] != "alpha" {
		t.Fatalf("evaluate listener: %+v %v", eval, err)
	}

	// Evaluation errors are reported in the response.
	eval, err = exprs.EvaluateExpression(ctx, connect.NewRequest(&gofiv1.EvaluateExpressionRequest{Expression: `fields["missing"]`}))
	if err != nil || eval.Msg.Error == "" {
		t.Fatalf("evaluate error: %+v %v", eval, err)
	}

	env, err := exprs.DescribeEnvironment(ctx, connect.NewRequest(&gofiv1.DescribeEnvironmentRequest{}))
	if err != nil || len(env.Msg.Variables) != 3 || env.Msg.CostLimit == 0 || len(env.Msg.Functions) < 10 {
		t.Fatalf("describe: %+v %v", env, err)
	}
}
