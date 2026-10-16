package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/expressions"
	"github.com/wcrum/gofi/filter"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/pipeline"
)

// Evaluate input bounds: the endpoint compiles and runs caller-supplied CEL.
const (
	maxEvaluateRaw    = 64 << 10
	maxEvaluateFields = 256 << 10
	evaluateDeadline  = 5 * time.Second
)

// expressionHandler implements ExpressionService. Every check and evaluation
// goes through filter.Env(), the same environment the pipeline uses.
type expressionHandler struct {
	ctrl *controller.Controller
	lib  *expressions.Manager
}

func (h *expressionHandler) toProto(e expressions.Expression) *gofiv1.CelExpression {
	out := &gofiv1.CelExpression{Name: e.Name, Description: e.Description, Expression: e.Expression, Tags: e.Tags, ResultType: e.ResultType, Generation: e.Generation}
	for _, def := range h.ctrl.List() {
		if uses, _ := expressions.Uses(def, e.Name, e.Generation); uses {
			out.UsedBy = append(out.UsedBy, def.Name)
		}
	}
	for _, r := range e.Revisions {
		out.PinnedRevisions = append(out.PinnedRevisions, &gofiv1.ExpressionRevision{Generation: r.Generation, Expression: r.Expression})
	}
	return out
}

func (h *expressionHandler) ListExpressions(ctx context.Context, req *connect.Request[gofiv1.ListExpressionsRequest]) (*connect.Response[gofiv1.ListExpressionsResponse], error) {
	lib, err := h.lib.Snapshot(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	names := make([]string, 0, len(lib))
	for name := range lib {
		names = append(names, name)
	}
	sort.Strings(names)
	response := &gofiv1.ListExpressionsResponse{}
outer:
	for _, name := range names {
		e := lib[name]
		for _, want := range req.Msg.Tags {
			found := false
			for _, tag := range e.Tags {
				found = found || tag == want
			}
			if !found {
				continue outer
			}
		}
		response.Expressions = append(response.Expressions, h.toProto(e))
	}
	return connect.NewResponse(response), nil
}

func (h *expressionHandler) GetExpression(ctx context.Context, req *connect.Request[gofiv1.GetExpressionRequest]) (*connect.Response[gofiv1.GetExpressionResponse], error) {
	lib, err := h.lib.Snapshot(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	e, ok := lib[req.Msg.Name]
	if !ok {
		return nil, rpcError(connect.CodeNotFound, fmt.Errorf("CEL expression %q not found", req.Msg.Name))
	}
	return connect.NewResponse(&gofiv1.GetExpressionResponse{Expression: h.toProto(e)}), nil
}

// ApplyExpression creates or updates an entry. The generation increases only
// when the expression text changes; earlier revisions are retained while a
// listener pins them. The change is tested against every listener that links
// the entry before it is saved.
func (h *expressionHandler) ApplyExpression(ctx context.Context, req *connect.Request[gofiv1.ApplyExpressionRequest]) (*connect.Response[gofiv1.ApplyExpressionResponse], error) {
	in := req.Msg.Expression
	if in == nil {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("expression is required"))
	}
	next, err := expressions.Validate(expressions.Expression{Name: in.Name, Description: in.Description, Expression: in.Expression, Tags: in.Tags, ResultType: in.ResultType})
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	h.lib.Ops.Lock()
	defer h.lib.Ops.Unlock()
	lib, err := h.lib.Snapshot(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	listeners := h.ctrl.List()
	textChanged, typeChanged := true, true
	if previous, exists := lib[next.Name]; exists {
		textChanged = previous.Expression != next.Expression
		typeChanged = previous.ResultType != next.ResultType
		next.Generation = previous.Generation
		candidates := previous.Revisions
		if textChanged {
			next.Generation++
			candidates = append(candidates, expressions.Revision{Generation: previous.Generation, Expression: previous.Expression})
		}
		pinned := map[int64]bool{}
		for _, def := range listeners {
			for _, ref := range expressions.References(def) {
				if ref.Name == next.Name && ref.Generation > 0 {
					pinned[ref.Generation] = true
				}
			}
		}
		for _, r := range candidates {
			if pinned[r.Generation] && r.Generation != next.Generation {
				next.Revisions = append(next.Revisions, r)
			}
		}
	} else {
		next.Generation = 1
	}
	// Every listener that links this entry must still validate.
	candidate := lib.With(next)
	var broken, followers []string
	for _, def := range listeners {
		uses, follows := expressions.Uses(def, next.Name, next.Generation)
		if !uses {
			continue
		}
		if follows {
			followers = append(followers, def.Name)
		}
		if err := h.ctrl.ValidateWith(def, candidate); err != nil {
			broken = append(broken, fmt.Sprintf("%s (%v)", def.Name, err))
		}
	}
	if len(broken) > 0 {
		return nil, rpcError(connect.CodeFailedPrecondition, fmt.Errorf("change would break flows linked to %q: %s", next.Name, strings.Join(broken, "; ")))
	}
	if err := h.lib.Save(ctx, next); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	response := &gofiv1.ApplyExpressionResponse{Expression: h.toProto(next)}
	if textChanged || typeChanged {
		response.ReloadedFlows = followers
		h.ctrl.NotifyRuntimeChange()
	}
	return connect.NewResponse(response), nil
}

func (h *expressionHandler) DeleteExpression(ctx context.Context, req *connect.Request[gofiv1.DeleteExpressionRequest]) (*connect.Response[gofiv1.DeleteExpressionResponse], error) {
	h.lib.Ops.Lock()
	defer h.lib.Ops.Unlock()
	lib, err := h.lib.Snapshot(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	if _, ok := lib[req.Msg.Name]; !ok {
		return nil, rpcError(connect.CodeNotFound, fmt.Errorf("CEL expression %q not found", req.Msg.Name))
	}
	var users []string
	for _, def := range h.ctrl.List() {
		if uses, _ := expressions.Uses(def, req.Msg.Name, 0); uses {
			users = append(users, def.Name)
		}
	}
	if len(users) > 0 {
		return nil, rpcError(connect.CodeFailedPrecondition, fmt.Errorf("CEL expression %q is used by flows: %s", req.Msg.Name, strings.Join(users, ", ")))
	}
	if err := h.lib.Delete(ctx, req.Msg.Name); err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.DeleteExpressionResponse{}), nil
}

func resultTypeOrDefault(t string) (string, error) {
	if t == "" {
		return filter.ResultTypeAny, nil
	}
	if !filter.ValidResultType(t) {
		return "", fmt.Errorf("resultType must be %q or %q", filter.ResultTypeBool, filter.ResultTypeAny)
	}
	return t, nil
}

func diagnosticsToProto(diags []filter.Diagnostic) []*gofiv1.ExpressionDiagnostic {
	out := make([]*gofiv1.ExpressionDiagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, &gofiv1.ExpressionDiagnostic{Line: int32(d.Line), Column: int32(d.Column), Message: d.Message})
	}
	return out
}

func (h *expressionHandler) CheckExpression(_ context.Context, req *connect.Request[gofiv1.CheckExpressionRequest]) (*connect.Response[gofiv1.CheckExpressionResponse], error) {
	resultType, err := resultTypeOrDefault(req.Msg.ResultType)
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	outputType, diags := filter.Check(req.Msg.Expression, resultType)
	return connect.NewResponse(&gofiv1.CheckExpressionResponse{Valid: len(diags) == 0, Diagnostics: diagnosticsToProto(diags), OutputType: outputType}), nil
}

// EvaluateExpression runs an expression against a sample. With a listener
// name, raw is decoded and parsed by that listener exactly as on ingress.
// Evaluation failures are reported in the response, not as RPC errors.
func (h *expressionHandler) EvaluateExpression(ctx context.Context, req *connect.Request[gofiv1.EvaluateExpressionRequest]) (*connect.Response[gofiv1.EvaluateExpressionResponse], error) {
	resultType, err := resultTypeOrDefault(req.Msg.ResultType)
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	if len(req.Msg.Raw) > maxEvaluateRaw {
		return nil, rpcError(connect.CodeInvalidArgument, fmt.Errorf("raw sample exceeds %d bytes", maxEvaluateRaw))
	}
	fields := map[string]any{}
	if req.Msg.Fields != nil {
		fields = req.Msg.Fields.AsMap()
		if encoded, _ := json.Marshal(fields); len(encoded) > maxEvaluateFields {
			return nil, rpcError(connect.CodeInvalidArgument, fmt.Errorf("fields sample exceeds %d bytes", maxEvaluateFields))
		}
	}
	response := &gofiv1.EvaluateExpressionResponse{}
	if _, diags := filter.Check(req.Msg.Expression, resultType); len(diags) > 0 {
		response.Diagnostics = diagnosticsToProto(diags)
		response.Error = diags[0].Message
		return connect.NewResponse(response), nil
	}
	program, err := filter.CompileFor(req.Msg.Expression, resultType)
	if err != nil {
		response.Error = err.Error()
		return connect.NewResponse(response), nil
	}
	ctx, cancel := context.WithTimeout(ctx, evaluateDeadline)
	defer cancel()
	var msg *message.Message
	if req.Msg.Flow != "" {
		def, found := h.ctrl.Get(req.Msg.Flow)
		if !found {
			return nil, rpcError(connect.CodeNotFound, fmt.Errorf("flow %q not found", req.Msg.Flow))
		}
		if def, err = h.ctrl.Resolve(def); err != nil {
			return nil, rpcError(connect.CodeFailedPrecondition, err)
		}
		if msg, err = pipeline.ParseSample(ctx, def, req.Msg.Raw); err != nil {
			response.Error = err.Error()
			return connect.NewResponse(response), nil
		}
	} else {
		msg = message.New(req.Msg.Raw, "evaluate", "evaluate")
		msg.Fields = fields
	}
	for k, v := range req.Msg.Meta {
		msg.Meta[k] = v
	}
	started := time.Now()
	value, cost, err := filter.EvaluateJSON(ctx, program, msg)
	response.DurationUs = time.Since(started).Microseconds()
	response.Cost = cost
	if err != nil {
		response.Error = err.Error()
	} else if _, isBool := value.GetKind().(*structpb.Value_BoolValue); resultType == filter.ResultTypeBool && !isBool {
		response.Error = "expression did not return a bool"
		response.Value = value
	} else {
		response.Value = value
	}
	if bound, err := jsonStruct(msg.Fields); err == nil {
		response.BoundFields = bound
	}
	response.BoundMeta = msg.Meta
	return connect.NewResponse(response), nil
}

// jsonStruct converts parser output (JSON-compatible values) into a Struct.
func jsonStruct(fields map[string]any) (*structpb.Struct, error) {
	if s, err := structpb.NewStruct(fields); err == nil {
		return s, nil
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	var plain map[string]any
	if err = json.Unmarshal(encoded, &plain); err != nil {
		return nil, err
	}
	return structpb.NewStruct(plain)
}

func (h *expressionHandler) DescribeEnvironment(context.Context, *connect.Request[gofiv1.DescribeEnvironmentRequest]) (*connect.Response[gofiv1.DescribeEnvironmentResponse], error) {
	variables, functions, err := filter.Describe()
	if err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	response := &gofiv1.DescribeEnvironmentResponse{CostLimit: filter.CostLimit, MaxExpressionBytes: filter.MaxExpressionBytes}
	for _, v := range variables {
		response.Variables = append(response.Variables, &gofiv1.CelVariable{Name: v.Name, Type: v.Type, Description: v.Description})
	}
	for _, f := range functions {
		response.Functions = append(response.Functions, &gofiv1.CelFunction{Name: f.Name, Signatures: f.Signatures, Description: f.Description})
	}
	return connect.NewResponse(response), nil
}
