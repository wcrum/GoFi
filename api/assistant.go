package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/assistant"
	"github.com/wcrum/gofi/expressions"
	"github.com/wcrum/gofi/filter"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/history"
	"github.com/wcrum/gofi/sandbox"
	"github.com/wcrum/gofi/spec"
	"sigs.k8s.io/yaml"
)

// assistantHandler serves AssistantService. Its tools call the same handlers
// the portal uses (UpsertFlow, ApplyExpression, ApplySandbox), so validation,
// credential redaction, admission, and revision history apply unchanged.
type assistantHandler struct {
	gofiv1connect.UnimplementedAssistantServiceHandler
	flows       *Handler
	expressions *expressionHandler // nil without a CEL library
	sandbox     *sandboxHandler    // nil without Python workers
	ops         *operationsHandler // bulletins (the Problems tab)
	manager     *assistant.Manager
	service     *assistant.Service
}

func newAssistantHandler(flows *Handler, expressions *expressionHandler, sb *sandboxHandler, ops *operationsHandler, manager *assistant.Manager, docs fs.FS) *assistantHandler {
	h := &assistantHandler{flows: flows, expressions: expressions, sandbox: sb, ops: ops, manager: manager}
	h.service = assistant.NewService(manager, h, docs)
	return h
}

func configurationToProto(c assistant.Configuration) *gofiv1.AssistantConfiguration {
	_, source := assistant.Key(c)
	c = assistant.Public(c)
	return &gofiv1.AssistantConfiguration{
		Enabled: c.Enabled, Provider: c.ProviderOrDefault(), BaseUrl: c.BaseURL, Model: c.Model, HasApiKey: source != "none", ApiKeySource: source,
		AllowChanges: c.AllowChanges, AllowFunctions: c.AllowFunctions, Instructions: c.Instructions, MaxToolSteps: int32(c.MaxToolSteps), Effort: c.Effort, ToolApproval: c.ToolApprovalOrDefault(),
	}
}

func configurationFromProto(m *gofiv1.AssistantConfiguration) assistant.Configuration {
	if m == nil {
		return assistant.Configuration{}
	}
	return assistant.Configuration{
		Enabled: m.Enabled, Provider: m.Provider, BaseURL: m.BaseUrl, Model: m.Model, APIKey: m.ApiKey, AllowChanges: m.AllowChanges, AllowFunctions: m.AllowFunctions,
		Instructions: m.Instructions, MaxToolSteps: int(m.MaxToolSteps), Effort: m.Effort, ToolApproval: m.ToolApproval,
	}
}

func (h *assistantHandler) GetAssistantConfiguration(ctx context.Context, _ *connect.Request[gofiv1.GetAssistantConfigurationRequest]) (*connect.Response[gofiv1.GetAssistantConfigurationResponse], error) {
	c, err := h.manager.Get(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	ready, message, _ := h.service.Ready(ctx)
	locks := assistant.EnvironmentLocks()
	shown := configurationToProto(assistant.Effective(c))
	return connect.NewResponse(&gofiv1.GetAssistantConfigurationResponse{Configuration: shown, Backend: h.manager.Backend(), Ready: ready, ReadyMessage: message, EndpointLocked: locks.Endpoint, KeyLocked: locks.Key, FunctionsLocked: locks.Functions}), nil
}

func (h *assistantHandler) ApplyAssistantConfiguration(ctx context.Context, req *connect.Request[gofiv1.ApplyAssistantConfigurationRequest]) (*connect.Response[gofiv1.ApplyAssistantConfigurationResponse], error) {
	if req.Msg.Configuration == nil {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("configuration is required"))
	}
	c, err := h.manager.Save(ctx, configurationFromProto(req.Msg.Configuration), req.Msg.ClearApiKey)
	if err != nil {
		if strings.Contains(err.Error(), "pinned") || strings.Contains(err.Error(), "set by the environment") {
			return nil, rpcError(connect.CodeFailedPrecondition, err)
		}
		if strings.Contains(err.Error(), "must") || strings.Contains(err.Error(), "exceed") {
			return nil, rpcError(connect.CodeInvalidArgument, err)
		}
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	slog.Info("assistant configuration saved", "enabled", c.Enabled, "endpoint", endpointHost(c), "model", c.ModelOrDefault(), "allowChanges", c.AllowChanges, "allowFunctions", c.AllowFunctions, "author", history.AuthorFromHeader(req.Header()))
	return connect.NewResponse(&gofiv1.ApplyAssistantConfigurationResponse{Configuration: configurationToProto(c)}), nil
}

func (h *assistantHandler) CheckAssistantConnection(ctx context.Context, req *connect.Request[gofiv1.CheckAssistantConnectionRequest]) (*connect.Response[gofiv1.CheckAssistantConnectionResponse], error) {
	stored, err := h.manager.Get(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	c := stored
	if req.Msg.Configuration != nil {
		c = configurationFromProto(req.Msg.Configuration)
		if c, err = assistant.Normalize(c); err != nil {
			return nil, rpcError(connect.CodeInvalidArgument, err)
		}
		// The stored key is only ever sent to the stored endpoint: a test of
		// another endpoint must bring its own key, or it would hand the
		// stored key to whatever address the caller typed.
		if c.APIKey == "" || c.APIKey == "***" {
			if assistant.Effective(c).BaseURL != assistant.Effective(stored).BaseURL {
				return nil, rpcError(connect.CodeInvalidArgument, errors.New("testing a different endpoint requires its API key; save the endpoint first or enter the key"))
			}
			c.APIKey = stored.APIKey
		} else if assistant.Effective(c).BaseURL != assistant.Effective(stored).BaseURL {
			slog.Info("assistant connection test against an unsaved endpoint", "endpoint", endpointHost(c), "author", history.AuthorFromHeader(req.Header()))
		}
	}
	model, latency, err := assistant.Check(ctx, c)
	if req.Msg.Configuration == nil {
		h.service.RecordCheck(c, model, err)
	}
	if err != nil {
		return connect.NewResponse(&gofiv1.CheckAssistantConnectionResponse{Ok: false, Message: err.Error(), LatencyMs: latency.Milliseconds()}), nil
	}
	return connect.NewResponse(&gofiv1.CheckAssistantConnectionResponse{Ok: true, Model: model, Message: "the endpoint answered", LatencyMs: latency.Milliseconds()}), nil
}

func (h *assistantHandler) ListAssistantTools(ctx context.Context, _ *connect.Request[gofiv1.ListAssistantToolsRequest]) (*connect.Response[gofiv1.ListAssistantToolsResponse], error) {
	tools, allowChanges, err := h.service.Tools(ctx)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	out := &gofiv1.ListAssistantToolsResponse{}
	for _, t := range tools {
		out.Tools = append(out.Tools, &gofiv1.AssistantTool{Name: t.Name, Description: t.Description, Mutating: t.Mutating, Enabled: !t.Mutating || allowChanges})
	}
	return connect.NewResponse(out), nil
}

func (h *assistantHandler) Chat(ctx context.Context, req *connect.Request[gofiv1.ChatRequest], stream *connect.ServerStream[gofiv1.AssistantEvent]) error {
	meta := assistant.CallMeta{Author: history.AuthorFromHeader(req.Header()), Header: req.Header()}
	id := req.Msg.ConversationId
	announced := false
	send := func(ev assistant.Event) error {
		out := &gofiv1.AssistantEvent{ConversationId: id}
		switch ev.Kind {
		case "text":
			out.Event = &gofiv1.AssistantEvent_TextDelta{TextDelta: ev.Text}
		case "tool_call":
			out.Event = &gofiv1.AssistantEvent_ToolCall{ToolCall: &gofiv1.AssistantToolCall{Id: ev.ToolID, Name: ev.ToolName, InputJson: ev.Input, NeedsApproval: ev.NeedsApproval}}
		case "tool_result":
			out.Event = &gofiv1.AssistantEvent_ToolResult{ToolResult: &gofiv1.AssistantToolResult{Id: ev.ToolID, Name: ev.ToolName, Output: ev.Output, IsError: ev.IsError}}
		case "navigate":
			out.Event = &gofiv1.AssistantEvent_Navigate{Navigate: &gofiv1.AssistantNavigate{Href: ev.Href}}
		case "highlight":
			out.Event = &gofiv1.AssistantEvent_Highlight{Highlight: &gofiv1.AssistantHighlight{Flow: ev.Flow, Block: ev.Block, Note: ev.Text}}
		case "proposal":
			out.Event = &gofiv1.AssistantEvent_Proposal{Proposal: &gofiv1.AssistantProposal{Id: ev.ProposalID, Name: ev.ToolName, InputJson: ev.Input, Summary: ev.Summary}}
		case "done":
			out.Event = &gofiv1.AssistantEvent_Done{Done: &gofiv1.AssistantDone{StopReason: ev.StopReason, InputTokens: ev.InputTokens, OutputTokens: ev.OutputTokens, ToolSteps: int32(ev.ToolSteps)}}
		}
		announced = true
		return stream.Send(out)
	}
	// The service assigns the id before the first event; capture it by
	// sending through a closure that reads the latest value.
	// One turn is bounded in wall-clock whatever the endpoint does.
	ctx, cancel := context.WithTimeout(ctx, assistant.TurnTimeout)
	defer cancel()
	var err error
	id, err = h.service.Chat(ctx, id, req.Msg.Message, req.Msg.PageContext, meta, func(ev assistant.Event) error {
		return send(ev)
	})
	if err != nil {
		switch {
		case errors.Is(err, assistant.ErrDisabled), errors.Is(err, assistant.ErrNoKey):
			return rpcError(connect.CodeFailedPrecondition, err)
		case errors.Is(err, assistant.ErrBusy), errors.Is(err, assistant.ErrReplyInProgress):
			return rpcError(connect.CodeResourceExhausted, err)
		case errors.Is(err, context.DeadlineExceeded):
			return rpcError(connect.CodeDeadlineExceeded, errors.New("the reply exceeded the turn time limit"))
		case errors.Is(err, context.Canceled):
			return nil
		case strings.Contains(err.Error(), "is required") || strings.Contains(err.Error(), "exceeds"):
			return rpcError(connect.CodeInvalidArgument, err)
		}
		if announced {
			slog.Warn("assistant turn failed", "conversation", shortID(id), "error", err)
		}
		return rpcError(connect.CodeUnavailable, err)
	}
	return nil
}

func (h *assistantHandler) ConfirmAssistantAction(ctx context.Context, req *connect.Request[gofiv1.ConfirmAssistantActionRequest]) (*connect.Response[gofiv1.ConfirmAssistantActionResponse], error) {
	meta := assistant.CallMeta{Author: history.AuthorFromHeader(req.Header()), Header: req.Header()}
	output, isError, err := h.service.Confirm(ctx, req.Msg.ConversationId, req.Msg.ProposalId, req.Msg.Approve, meta)
	if err != nil {
		if errors.Is(err, assistant.ErrNoProposal) {
			return nil, rpcError(connect.CodeNotFound, err)
		}
		return nil, rpcError(connect.CodeFailedPrecondition, err)
	}
	slog.Info("assistant proposal decided", "conversation", shortID(req.Msg.ConversationId), "proposal", shortID(req.Msg.ProposalId), "approved", req.Msg.Approve, "author", meta.Author, "error", isError)
	return connect.NewResponse(&gofiv1.ConfirmAssistantActionResponse{Output: output, IsError: isError}), nil
}

func (h *assistantHandler) ListAssistantConversations(_ context.Context, _ *connect.Request[gofiv1.ListAssistantConversationsRequest]) (*connect.Response[gofiv1.ListAssistantConversationsResponse], error) {
	out := &gofiv1.ListAssistantConversationsResponse{}
	for _, c := range h.service.Conversations() {
		out.Conversations = append(out.Conversations, &gofiv1.AssistantConversationSummary{Id: c.ID, Title: c.Title, UpdatedUnixMs: c.Updated.UnixMilli(), Messages: int32(c.Messages), PendingProposals: int32(c.PendingProposals)})
	}
	return connect.NewResponse(out), nil
}

func (h *assistantHandler) GetAssistantConversation(_ context.Context, req *connect.Request[gofiv1.GetAssistantConversationRequest]) (*connect.Response[gofiv1.GetAssistantConversationResponse], error) {
	messages, pending, ok := h.service.Transcript(req.Msg.ConversationId)
	if !ok {
		return nil, rpcError(connect.CodeNotFound, errors.New("conversation not found; the controller holds conversations in memory for 12 hours"))
	}
	out := &gofiv1.GetAssistantConversationResponse{ConversationId: req.Msg.ConversationId}
	for _, m := range messages {
		item := &gofiv1.AssistantTranscriptMessage{Role: m.Role, Text: m.Text}
		for _, call := range m.Calls {
			item.Calls = append(item.Calls, &gofiv1.AssistantToolCall{Id: call.ID, Name: call.Name, InputJson: string(call.Input)})
		}
		for _, r := range m.Results {
			item.Results = append(item.Results, &gofiv1.AssistantToolResult{Id: r.ID, Name: r.Name, Output: assistant.PlainOutput(r.Output), IsError: r.IsError})
		}
		out.Messages = append(out.Messages, item)
	}
	for _, p := range pending {
		out.PendingProposals = append(out.PendingProposals, &gofiv1.AssistantProposal{Id: p.ID, Name: p.Name, InputJson: string(p.Input), Summary: p.Summary})
	}
	return connect.NewResponse(out), nil
}

func (h *assistantHandler) ResetConversation(_ context.Context, req *connect.Request[gofiv1.ResetConversationRequest]) (*connect.Response[gofiv1.ResetConversationResponse], error) {
	h.service.Reset(req.Msg.ConversationId)
	return connect.NewResponse(&gofiv1.ResetConversationResponse{}), nil
}

// shortID keeps conversation and proposal ids (bearer-like) out of logs.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

// endpointHost names the endpoint for logs without its path or query.
func endpointHost(c assistant.Configuration) string {
	c = assistant.Effective(c)
	if c.BaseURL == "" {
		return "api.anthropic.com"
	}
	if u, err := url.Parse(c.BaseURL); err == nil {
		return u.Host
	}
	return "invalid"
}

// --- assistant.Actions -------------------------------------------------------

func toYAML(v any) (string, error) {
	data, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// flowSummary is what list_flows shows per flow: enough to pick one.
type flowSummary struct {
	Name    string   `json:"name"`
	Source  string   `json:"source,omitempty"`
	Stages  int      `json:"stages"`
	Sinks   []string `json:"sinks,omitempty"`
	Desired string   `json:"desired,omitempty"`
	// Runtime is the controller's view: phase, runtime status, restarts.
	Runtime string `json:"runtime,omitempty"`
	// Problem is the current failure, when there is one: the reason a
	// runtime cannot start or the last error it reported.
	Problem string `json:"problem,omitempty"`
	// OpenProblems counts the Problems tab's open errors and warnings.
	OpenProblems int `json:"openProblems,omitempty"`
}

// describeStatus adds the run state the portal shows (Flows page, Problems
// tab) so the model can tell a flow that is failing from one that is idle.
func (h *assistantHandler) describeStatus(ctx context.Context, out *flowSummary) {
	status, ok := h.flows.ctrl.Status(ctx, out.Name)
	if !ok {
		return
	}
	out.Desired = status.Desired
	parts := []string{}
	if status.Phase != "" {
		parts = append(parts, status.Phase)
	}
	if status.RuntimeStatus != "" && status.RuntimeStatus != status.Phase {
		parts = append(parts, "runtime "+status.RuntimeStatus)
	}
	if status.Restarts > 0 {
		parts = append(parts, fmt.Sprintf("%d restarts", status.Restarts))
	}
	out.Runtime = strings.Join(parts, ", ")
	out.Problem = status.Message
	if runtime, found, err := h.flows.ctrl.RuntimeStatus(ctx, out.Name); err == nil && found && runtime.Error != "" {
		out.Problem = runtime.Error
	}
	if h.ops != nil {
		for _, b := range h.ops.openBulletins(ctx, out.Name, false) {
			if b.Severity == "error" || b.Severity == "warning" {
				out.OpenProblems++
			}
		}
	}
}

// FlowProblems is get_flow_problems: bulletins plus Python binding checks.
func (h *assistantHandler) FlowProblems(ctx context.Context, name string) (string, error) {
	def, ok := h.flows.ctrl.Get(name)
	if !ok {
		return "", fmt.Errorf("flow %q does not exist; call list_flows", name)
	}
	var b strings.Builder
	item := flowSummary{Name: name}
	h.describeStatus(ctx, &item)
	fmt.Fprintf(&b, "flow: %s\nstatus: %s\n", name, item.Runtime)
	if item.Problem != "" {
		fmt.Fprintf(&b, "runtime problem: %s\n", strings.ReplaceAll(item.Problem, "\n", " "))
	}
	b.WriteString("open problems:\n")
	count := 0
	if h.ops != nil {
		for _, bl := range h.ops.openBulletins(ctx, name, false) {
			count++
			last := ""
			if bl.LastSeen != nil {
				last = bl.LastSeen.AsTime().UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(&b, "- %s at %s: %s (seen %d times, last %s)\n", bl.Severity, bl.Component, strings.ReplaceAll(bl.Message, "\n", " "), bl.Count, last)
		}
	}
	if count == 0 {
		b.WriteString("- none\n")
	}
	uses := def.PythonRefs()
	if len(uses) == 0 {
		b.WriteString("python bindings: none (the flow uses no Python functions)\n")
		return b.String(), nil
	}
	b.WriteString("python bindings:\n")
	for _, use := range uses {
		ref := use.Ref
		if ref == nil {
			continue
		}
		library := ref.Library
		if ref.InlineSource != "" && library == "" {
			fmt.Fprintf(&b, "- %s: inline Python (%s); GoFi generates its library on save\n", use.Path, use.Kind)
			continue
		}
		if h.sandbox == nil {
			fmt.Fprintf(&b, "- %s: %s.%s (%s): this controller runs no Python workers\n", use.Path, library, ref.Function, use.Kind)
			continue
		}
		d, exists := h.sandbox.manager.Get(library)
		if !exists {
			fmt.Fprintf(&b, "- %s: %s.%s (%s): library %q does not exist; create it or point the step at one of the existing libraries (list_libraries)\n", use.Path, library, ref.Function, use.Kind, library)
			continue
		}
		phase := "unknown"
		if st, ok := h.sandbox.manager.Status(library); ok {
			phase = st.Phase
			if st.Error != "" {
				phase += " (" + strings.ReplaceAll(st.Error, "\n", " ") + ")"
			}
		}
		registered := "function not yet reported"
		if res, err := h.sandbox.ListFunctions(ctx, connect.NewRequest(&gofiv1.ListLibraryFunctionsRequest{Library: library})); err == nil {
			registered = fmt.Sprintf("function %q is not registered by the library (it registers:", ref.Function)
			names := []string{}
			for _, f := range res.Msg.Functions {
				names = append(names, f.Kind+" "+f.Name)
				if f.Name == ref.Function {
					registered = "function registered as " + f.Kind
				}
			}
			if strings.HasPrefix(registered, "function \"") {
				registered += " " + strings.Join(names, ", ") + ")"
			}
		}
		managed := ""
		if d.ManagedBy != "" {
			managed = ", generated from " + d.ManagedBy
		}
		fmt.Fprintf(&b, "- %s: %s.%s (%s): library exists%s, worker %s, %s\n", use.Path, library, ref.Function, use.Kind, managed, phase, registered)
	}
	return b.String(), nil
}

// summarize reads the top-level shape of a definition without depending on
// every spec field, so list output stays stable as the schema grows.
func summarize(def spec.FlowDefinition) flowSummary {
	out := flowSummary{Name: def.Name}
	data, err := json.Marshal(spec.Redact(def))
	if err != nil {
		return out
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return out
	}
	if source, ok := m["source"].(map[string]any); ok {
		for key, value := range source {
			if value != nil {
				out.Source = key
				if inner, ok := value.(map[string]any); ok {
					if network, ok := inner["network"].(string); ok {
						out.Source += " " + network
					}
					if port, ok := inner["port"].(float64); ok {
						out.Source += fmt.Sprintf(":%d", int(port))
					}
				}
				break
			}
		}
	}
	if stages, ok := m["stages"].([]any); ok {
		out.Stages = len(stages)
	}
	if sinks, ok := m["sinks"].([]any); ok {
		for _, s := range sinks {
			if sink, ok := s.(map[string]any); ok {
				label := ""
				if id, ok := sink["id"].(string); ok {
					label = id
				}
				for _, key := range []string{"http", "tcp", "udp", "nats", "mqtt", "kafka", "file", "stdout", "python", "aster"} {
					if sink[key] != nil {
						label = strings.TrimSpace(label + " (" + key + ")")
						break
					}
				}
				out.Sinks = append(out.Sinks, label)
			}
		}
	}
	return out
}

func (h *assistantHandler) ListFlows(ctx context.Context) (string, error) {
	defs := h.flows.ctrl.List()
	if len(defs) == 0 {
		return "no flows are defined", nil
	}
	summaries := make([]flowSummary, 0, len(defs))
	for _, def := range defs {
		item := summarize(def)
		h.describeStatus(ctx, &item)
		summaries = append(summaries, item)
	}
	return toYAML(summaries)
}

func (h *assistantHandler) GetFlow(ctx context.Context, name string) (string, error) {
	def, ok := h.flows.ctrl.Get(name)
	if !ok {
		return "", fmt.Errorf("flow %q does not exist; call list_flows", name)
	}
	def.StripDerivedPython()
	text, err := toYAML(spec.Redact(def))
	if err != nil {
		return "", err
	}
	item := flowSummary{Name: name}
	h.describeStatus(ctx, &item)
	status := "# status: " + item.Runtime
	if item.Problem != "" {
		status += "\n# problem: " + strings.ReplaceAll(item.Problem, "\n", " ")
	}
	return status + "\n" + text, nil
}

// parseFlow accepts the bare definition get_flow returns or a full Flow
// resource (apiVersion/kind/metadata/spec), as docs/flows.md shows it.
func parseFlow(text string) (spec.FlowDefinition, error) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		return spec.FlowDefinition{}, fmt.Errorf("flow YAML: %w", err)
	}
	if kind, _ := raw["kind"].(string); kind == "Flow" {
		specPart, _ := raw["spec"].(map[string]any)
		if specPart == nil {
			return spec.FlowDefinition{}, errors.New("flow resource has no spec")
		}
		if meta, ok := raw["metadata"].(map[string]any); ok {
			if name, ok := meta["name"].(string); ok && specPart["name"] == nil {
				specPart["name"] = name
			}
		}
		raw = specPart
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return spec.FlowDefinition{}, err
	}
	var def spec.FlowDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return spec.FlowDefinition{}, fmt.Errorf("flow definition: %w", err)
	}
	if strings.TrimSpace(def.Name) == "" {
		return def, errors.New("the flow needs a name")
	}
	return def, nil
}

func (h *assistantHandler) upsert(ctx context.Context, text, message string, validateOnly bool, meta assistant.CallMeta) (string, error) {
	def, err := parseFlow(text)
	if err != nil {
		return "", err
	}
	item := &gofiv1.Flow{}
	if err := toProto(def, item); err != nil {
		return "", err
	}
	req := connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: item, ValidateOnly: validateOnly, Message: message})
	history.CopyIdentity(req.Header(), meta.Header)
	res, err := h.flows.UpsertFlow(ctx, req)
	if err != nil {
		return "", errors.New(connect.CodeOf(err).String() + ": " + strings.TrimPrefix(err.Error(), connect.CodeOf(err).String()+": "))
	}
	verb := "valid"
	if !validateOnly {
		verb = "saved flow " + def.Name + " (#/flows/" + def.Name + ")"
	}
	if len(res.Msg.Warnings) > 0 {
		return verb + "\nwarnings:\n- " + strings.Join(res.Msg.Warnings, "\n- "), nil
	}
	return verb, nil
}

func (h *assistantHandler) ValidateFlow(ctx context.Context, text string) (string, error) {
	return h.upsert(ctx, text, "", true, assistant.CallMeta{})
}

func (h *assistantHandler) ApplyFlow(ctx context.Context, text, message string, meta assistant.CallMeta) (string, error) {
	// Every revision the bot writes says so, whatever note the model chose.
	message = strings.TrimSpace("GoFiBot: " + strings.TrimPrefix(strings.TrimSpace(message), "GoFiBot:"))
	return h.upsert(ctx, text, message, false, meta)
}

func (h *assistantHandler) CheckExpression(_ context.Context, expression, resultType string) (string, error) {
	if resultType == "" {
		resultType = filter.ResultTypeBool
	}
	if resultType != filter.ResultTypeBool && resultType != filter.ResultTypeAny {
		return "", errors.New("result_type must be bool or any")
	}
	outputType, diags := filter.Check(expression, resultType)
	if len(diags) == 0 {
		return "valid; output type " + outputType, nil
	}
	var b strings.Builder
	b.WriteString("invalid:\n")
	for _, d := range diags {
		fmt.Fprintf(&b, "- line %d, column %d: %s\n", d.Line, d.Column, d.Message)
	}
	return b.String(), nil
}

func (h *assistantHandler) ListExpressions(ctx context.Context) (string, error) {
	if h.expressions == nil {
		return "this deployment has no CEL library", nil
	}
	lib, err := h.expressions.lib.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	if len(lib) == 0 {
		return "the CEL library is empty", nil
	}
	items := make([]expressions.Expression, 0, len(lib))
	for _, e := range lib {
		e.Revisions = nil
		items = append(items, e)
	}
	return toYAML(items)
}

func (h *assistantHandler) ApplyExpression(ctx context.Context, name, expression, description, resultType string, meta assistant.CallMeta) (string, error) {
	if h.expressions == nil {
		return "", errors.New("this deployment has no CEL library")
	}
	item := &gofiv1.CelExpression{}
	if err := toProto(expressions.Expression{Name: name, Expression: expression, Description: description, ResultType: resultType}, item); err != nil {
		return "", err
	}
	req := connect.NewRequest(&gofiv1.ApplyExpressionRequest{Expression: item})
	history.CopyIdentity(req.Header(), meta.Header)
	res, err := h.expressions.ApplyExpression(ctx, req)
	if err != nil {
		return "", err
	}
	out := "saved expression " + name + " (#/cel?name=" + name + ")"
	if len(res.Msg.ReloadedFlows) > 0 {
		out += "; recompiled flows: " + strings.Join(res.Msg.ReloadedFlows, ", ")
	}
	return out, nil
}

type librarySummary struct {
	Name      string   `json:"name"`
	Phase     string   `json:"phase,omitempty"`
	Error     string   `json:"error,omitempty"`
	ManagedBy string   `json:"managedBy,omitempty"`
	Functions []string `json:"functions,omitempty"`
	Egress    int      `json:"egressRules,omitempty"`
}

func (h *assistantHandler) ListLibraries(ctx context.Context) (string, error) {
	if h.sandbox == nil {
		return "this deployment runs no Python workers", nil
	}
	defs := h.sandbox.manager.List()
	if len(defs) == 0 {
		return "no Python libraries are defined", nil
	}
	functions := map[string][]string{}
	if res, err := h.sandbox.ListFunctions(ctx, connect.NewRequest(&gofiv1.ListLibraryFunctionsRequest{})); err == nil {
		for _, f := range res.Msg.Functions {
			functions[f.Library] = append(functions[f.Library], f.Kind+" "+f.Name)
		}
	}
	items := make([]librarySummary, 0, len(defs))
	for _, d := range defs {
		item := librarySummary{Name: d.Name, ManagedBy: d.ManagedBy, Functions: functions[d.Name], Egress: len(d.Egress)}
		if st, ok := h.sandbox.manager.Status(d.Name); ok {
			item.Phase, item.Error = st.Phase, st.Error
		}
		items = append(items, item)
	}
	return toYAML(items)
}

func (h *assistantHandler) GetLibrary(_ context.Context, name string) (string, error) {
	if h.sandbox == nil {
		return "", errors.New("this deployment runs no Python workers")
	}
	d, ok := h.sandbox.manager.Get(name)
	if !ok {
		return "", fmt.Errorf("library %q does not exist; call list_libraries", name)
	}
	d.Tests = nil
	return toYAML(d)
}

func (h *assistantHandler) ValidatePythonSource(ctx context.Context, source string) (string, error) {
	if h.sandbox == nil {
		return "", errors.New("this deployment runs no Python workers")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	res, err := h.sandbox.manager.ValidateStatic(ctx, source)
	if err != nil {
		return "", err
	}
	if res.Valid {
		return "valid", nil
	}
	var b strings.Builder
	b.WriteString("invalid:\n")
	for _, d := range res.Diagnostics {
		fmt.Fprintf(&b, "- line %d, column %d: %s %s\n", d.Line, d.Column, d.Code, d.Message)
	}
	return b.String(), nil
}

func (h *assistantHandler) ApplyLibrary(ctx context.Context, name, source string, timeoutSeconds, memoryMiB int, meta assistant.CallMeta) (string, error) {
	if h.sandbox == nil {
		return "", errors.New("this deployment runs no Python workers")
	}
	def := sandbox.Definition{Name: name, SourceCode: source, TimeoutSeconds: timeoutSeconds, MemoryMiB: memoryMiB}
	if existing, ok := h.sandbox.manager.Get(name); ok {
		// Keep what the model did not mention: tests, egress rules, and the
		// AI opt-in (the assistant cannot grant or revoke proxy access).
		def.Tests, def.Egress, def.AI = existing.Tests, existing.Egress, existing.AI
		if timeoutSeconds == 0 {
			def.TimeoutSeconds = existing.TimeoutSeconds
		}
		if memoryMiB == 0 {
			def.MemoryMiB = existing.MemoryMiB
		}
	}
	item := &gofiv1.SandboxDefinition{}
	if err := toProto(def, item); err != nil {
		return "", err
	}
	req := connect.NewRequest(&gofiv1.ApplySandboxRequest{Sandbox: item})
	history.CopyIdentity(req.Header(), meta.Header)
	res, err := h.sandbox.ApplySandbox(ctx, req)
	if err != nil {
		return "", err
	}
	phase := ""
	if res.Msg.Status != nil {
		phase = res.Msg.Status.Phase
	}
	return "saved library " + name + " (#/functions); worker " + phase + "; functions register once it is ready", nil
}

func (h *assistantHandler) ListEndpoints(ctx context.Context) (string, error) {
	endpoints, err := h.flows.ctrl.Endpoints(ctx)
	if err != nil {
		return "", err
	}
	if len(endpoints) == 0 {
		return "no endpoints discovered", nil
	}
	return toYAML(endpoints)
}

func (h *assistantHandler) DescribeDeployment(ctx context.Context) (string, error) {
	res, err := h.flows.GetMode(ctx, connect.NewRequest(&gofiv1.GetModeRequest{}))
	if err != nil {
		return "", err
	}
	m := res.Msg
	out := fmt.Sprintf("mode: %s\ndriver: %s\nprocess role: %s\n", m.Mode, m.DriverKind, m.ProcessRole)
	if m.NodePortMax > 0 {
		out += fmt.Sprintf("nodePort range: %d-%d\n", m.NodePortMin, m.NodePortMax)
	}
	out += "python workers: " + map[bool]string{true: "available", false: "not configured"}[h.sandbox != nil] + "\n"
	out += "cel library: " + map[bool]string{true: "available", false: "not configured"}[h.expressions != nil] + "\n"
	return out, nil
}
