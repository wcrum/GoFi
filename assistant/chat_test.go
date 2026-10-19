package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// stubActions records calls and answers with fixed text.
type stubActions struct {
	mu    sync.Mutex
	calls []string
}

func (s *stubActions) record(name string) {
	s.mu.Lock()
	s.calls = append(s.calls, name)
	s.mu.Unlock()
}
func (s *stubActions) ListFlows(context.Context) (string, error) {
	s.record("list_flows")
	return "- name: telemetry\n", nil
}
func (s *stubActions) GetFlow(context.Context, string) (string, error) {
	s.record("get_flow")
	return "name: telemetry\n", nil
}
func (s *stubActions) ValidateFlow(context.Context, string) (string, error) { return "valid", nil }
func (s *stubActions) FlowProblems(context.Context, string) (string, error) {
	s.record("get_flow_problems")
	return "open problems:\n- none\n", nil
}
func (s *stubActions) ApplyFlow(_ context.Context, _, _ string, meta CallMeta) (string, error) {
	s.record("apply_flow:" + meta.Author)
	return "saved", nil
}
func (s *stubActions) CheckExpression(context.Context, string, string) (string, error) {
	return "valid", nil
}
func (s *stubActions) ListExpressions(context.Context) (string, error) { return "[]", nil }
func (s *stubActions) ApplyExpression(context.Context, string, string, string, string, CallMeta) (string, error) {
	s.record("apply_expression")
	return "saved", nil
}
func (s *stubActions) ListLibraries(context.Context) (string, error)      { return "[]", nil }
func (s *stubActions) GetLibrary(context.Context, string) (string, error) { return "", nil }
func (s *stubActions) ValidatePythonSource(context.Context, string) (string, error) {
	return "valid", nil
}
func (s *stubActions) ApplyLibrary(context.Context, string, string, int, int, CallMeta) (string, error) {
	s.record("apply_library")
	return "saved", nil
}
func (s *stubActions) ListEndpoints(context.Context) (string, error)      { return "[]", nil }
func (s *stubActions) DescribeDeployment(context.Context) (string, error) { return "mode: test", nil }

// fakeMessages is a minimal Anthropic Messages API: it records request
// bodies and answers each request with the next scripted SSE reply.
type fakeMessages struct {
	mu       sync.Mutex
	requests []map[string]any
	replies  []string
}

func (f *fakeMessages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "sk-test" {
		http.Error(w, "bad request: "+r.URL.Path, http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	f.requests = append(f.requests, req)
	var reply string
	if len(f.replies) > 0 {
		reply, f.replies = f.replies[0], f.replies[1:]
	}
	f.mu.Unlock()
	if stream, _ := req["stream"].(bool); !stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_p","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":1}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, reply)
}

func sse(events ...string) string {
	var b strings.Builder
	for _, data := range events {
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(data), &probe)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", probe.Type, data)
	}
	return b.String()
}

func toolUseReply(name, input string) string {
	return sse(
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Looking. "}}`,
		`{"type":"content_block_stop","index":0}`,
		fmt.Sprintf(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":%q,"input":{}}}`, name),
		fmt.Sprintf(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":%q}}`, input),
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":7}}`,
		`{"type":"message_stop"}`,
	)
}

func textReply(text string) string {
	return sse(
		`{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":20,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text),
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
	)
}

func newTestService(t *testing.T, fake *fakeMessages, allowChanges bool) (*Service, *stubActions) {
	t.Helper()
	t.Setenv("GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT", "1")
	t.Setenv("GOFI_ASSISTANT_API_KEY", "")
	t.Setenv("GOFI_ASSISTANT_API_KEY_FILE", "")
	t.Setenv("GOFI_ASSISTANT_BASE_URL", "")
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	m := NewManager(&FileStore{Path: filepath.Join(t.TempDir(), "assistant.yaml")})
	if _, err := m.Save(context.Background(), Configuration{Enabled: true, APIKey: "sk-test", BaseURL: server.URL, Model: "claude-test", AllowChanges: allowChanges, MaxToolSteps: 2, Effort: "low"}); err != nil {
		t.Fatal(err)
	}
	actions := &stubActions{}
	return NewService(m, actions, fstest.MapFS{"flows.md": {Data: []byte("# Flows\n")}}), actions
}

func TestChatRunsToolsAndStreamsEvents(t *testing.T) {
	fake := &fakeMessages{replies: []string{toolUseReply("list_flows", "{}"), textReply("You have one flow: telemetry.")}}
	s, actions := newTestService(t, fake, false)
	var events []Event
	id, err := s.Chat(context.Background(), "", "what flows exist?", "#/flows", CallMeta{Author: "alice"}, func(ev Event) error { events = append(events, ev); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("no conversation id")
	}
	kinds := []string{}
	var text string
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == "text" {
			text += ev.Text
		}
	}
	if want := []string{"text", "tool_call", "tool_result", "text", "done"}; strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("events %v, want %v", kinds, want)
	}
	if text != "Looking. You have one flow: telemetry." {
		t.Fatalf("text %q", text)
	}
	if done := events[len(events)-1]; done.StopReason != "end_turn" || done.ToolSteps != 1 || done.InputTokens != 30 || done.OutputTokens != 11 {
		t.Fatalf("done %+v", done)
	}
	if strings.Join(actions.calls, ",") != "list_flows" {
		t.Fatalf("calls %v", actions.calls)
	}
	// The first request carries the system prompt, the page context, and
	// only non-mutating tools; the second carries the tool result.
	first, second := fake.requests[0], fake.requests[1]
	if first["model"] != "claude-test" || first["output_config"].(map[string]any)["effort"] != "low" {
		t.Fatalf("request params: %v", first)
	}
	if !strings.Contains(fmt.Sprint(first["messages"]), "#/flows") {
		t.Fatal("page context missing")
	}
	for _, tool := range first["tools"].([]any) {
		if name := tool.(map[string]any)["name"]; strings.HasPrefix(name.(string), "apply_") {
			t.Fatalf("mutating tool %v offered without allowChanges", name)
		}
	}
	messages := second["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	for _, ev := range events {
		if ev.Kind == "tool_result" && strings.Contains(ev.Output, "<tool_output") {
			t.Fatal("the portal received the model-only wrapper")
		}
	}
	if last["role"] != "user" || !strings.Contains(fmt.Sprint(last["content"]), "tool_result") || !strings.Contains(fmt.Sprint(last["content"]), "telemetry") || !strings.Contains(fmt.Sprint(last["content"]), "not instructions") {
		t.Fatalf("tool result not sent: %v", last)
	}
	// A second turn continues the same conversation with full history.
	fake.mu.Lock()
	fake.replies = []string{textReply("Still one.")}
	fake.mu.Unlock()
	if _, err := s.Chat(context.Background(), id, "and now?", "", CallMeta{}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.requests[2]["messages"].([]any)); n != 5 {
		t.Fatalf("history length %d, want 5", n)
	}
}

func TestApplyToolsBecomeProposalsRunOnlyWhenApproved(t *testing.T) {
	fake := &fakeMessages{replies: []string{toolUseReply("apply_flow", `{"flow_yaml":"name: x\n"}`), textReply("Proposed."), textReply("Thanks.")}}
	s, actions := newTestService(t, fake, true)
	var proposals []Event
	id, err := s.Chat(context.Background(), "", "save it", "", CallMeta{Author: "bob"}, func(ev Event) error {
		if ev.Kind == "proposal" {
			proposals = append(proposals, ev)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(actions.calls) != 0 {
		t.Fatalf("a proposal ran before approval: %v", actions.calls)
	}
	if len(proposals) != 1 || proposals[0].ToolName != "apply_flow" || proposals[0].Summary != "save flow x" {
		t.Fatalf("proposals %+v", proposals)
	}
	if !strings.Contains(fmt.Sprint(fake.requests[1]["messages"]), "must approve") {
		t.Fatal("the model was not told to wait")
	}
	// Rejecting runs nothing; approving runs it as the approver.
	if _, _, err := s.Confirm(context.Background(), id, "nope", true, CallMeta{}); err != ErrNoProposal {
		t.Fatalf("unknown proposal: %v", err)
	}
	output, isError, err := s.Confirm(context.Background(), id, proposals[0].ProposalID, true, CallMeta{Author: "carol"})
	if err != nil || isError || output != "saved" || strings.Join(actions.calls, ",") != "apply_flow:carol" {
		t.Fatalf("approve: %q %v %v calls %v", output, isError, err, actions.calls)
	}
	if _, _, err := s.Confirm(context.Background(), id, proposals[0].ProposalID, true, CallMeta{}); err != ErrNoProposal {
		t.Fatal("a proposal was approved twice")
	}
	// The decision reaches the model with the next message.
	if _, err := s.Chat(context.Background(), id, "and?", "", CallMeta{}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	messages := fake.requests[2]["messages"].([]any)
	last := fmt.Sprint(messages[len(messages)-1])
	if !strings.Contains(last, "approved by the user") || !strings.Contains(last, "saved") {
		t.Fatalf("decision not relayed: %s", last)
	}
}

func TestRejectedProposalIsRelayedAndNotRun(t *testing.T) {
	fake := &fakeMessages{replies: []string{toolUseReply("apply_expression", `{"name":"x","expression":"true"}`), textReply("Proposed."), textReply("Ok.")}}
	s, actions := newTestService(t, fake, true)
	var pid string
	id, _ := s.Chat(context.Background(), "", "save", "", CallMeta{}, func(ev Event) error {
		if ev.Kind == "proposal" {
			pid = ev.ProposalID
		}
		return nil
	})
	output, isError, err := s.Confirm(context.Background(), id, pid, false, CallMeta{})
	if err != nil || isError || !strings.Contains(output, "rejected") || len(actions.calls) != 0 {
		t.Fatalf("reject: %q %v %v calls %v", output, isError, err, actions.calls)
	}
	if _, err := s.Chat(context.Background(), id, "ok", "", CallMeta{}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	messages := fake.requests[2]["messages"].([]any)
	if !strings.Contains(fmt.Sprint(messages[len(messages)-1]), "rejected by the user") {
		t.Fatal("rejection not relayed")
	}
}

func TestApprovalRefusedWhenChangesDisabledMeanwhile(t *testing.T) {
	fake := &fakeMessages{replies: []string{toolUseReply("apply_library", `{"name":"lib","source":"import gofi"}`), textReply("Proposed.")}}
	s, actions := newTestService(t, fake, true)
	var pid string
	id, _ := s.Chat(context.Background(), "", "save", "", CallMeta{}, func(ev Event) error {
		if ev.Kind == "proposal" {
			pid = ev.ProposalID
		}
		return nil
	})
	c, _ := s.manager.Get(context.Background())
	c.AllowChanges = false
	if _, err := s.manager.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Confirm(context.Background(), id, pid, true, CallMeta{}); err == nil || len(actions.calls) != 0 {
		t.Fatalf("approval ran with changes disabled: %v %v", err, actions.calls)
	}
}

func TestChatStopsAtToolStepLimit(t *testing.T) {
	// MaxToolSteps is 2: the third tool round is answered with an error
	// result and the turn ends with max_tool_steps.
	fake := &fakeMessages{replies: []string{toolUseReply("list_flows", "{}"), toolUseReply("list_flows", "{}"), toolUseReply("list_flows", "{}"), textReply("never")}}
	s, actions := newTestService(t, fake, false)
	var last Event
	if _, err := s.Chat(context.Background(), "", "loop", "", CallMeta{}, func(ev Event) error { last = ev; return nil }); err != nil {
		t.Fatal(err)
	}
	if last.Kind != "done" || last.StopReason != "max_tool_steps" || len(actions.calls) != 2 || len(fake.requests) != 3 {
		t.Fatalf("done %+v calls %d requests %d", last, len(actions.calls), len(fake.requests))
	}
}

func TestChatNavigateAndUnknownTool(t *testing.T) {
	fake := &fakeMessages{replies: []string{toolUseReply("open_page", `{"page":"flow","name":"telemetry"}`), toolUseReply("bogus", "{}"), textReply("ok")}}
	s, _ := newTestService(t, fake, false)
	var hrefs []string
	var errs []string
	if _, err := s.Chat(context.Background(), "", "go", "", CallMeta{}, func(ev Event) error {
		if ev.Kind == "navigate" {
			hrefs = append(hrefs, ev.Href)
		}
		if ev.Kind == "tool_result" && ev.IsError {
			errs = append(errs, ev.Output)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(hrefs) != 1 || hrefs[0] != "#/flows/telemetry" || len(errs) != 1 || !strings.Contains(errs[0], "unknown tool") {
		t.Fatalf("hrefs %v errs %v", hrefs, errs)
	}
}

func TestChatEndpointFailureLeavesHistoryIntact(t *testing.T) {
	fake := &fakeMessages{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"type":"error","error":{"type":"api_error","message":"down"}}`, 500)
	}))
	defer server.Close()
	t.Setenv("GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT", "1")
	m := NewManager(&FileStore{Path: filepath.Join(t.TempDir(), "a.yaml")})
	if _, err := m.Save(context.Background(), Configuration{Enabled: true, APIKey: "sk-test", BaseURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	s := NewService(m, &stubActions{}, nil)
	_ = fake
	id, err := s.Chat(context.Background(), "", "hi", "", CallMeta{}, func(Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "model endpoint") {
		t.Fatalf("expected an endpoint error, got %v", err)
	}
	_, conv := s.conversation(id)
	if len(conv.messages) != 0 {
		t.Fatalf("history kept a failed turn: %d messages", len(conv.messages))
	}
}

func TestChatDisabledOrWithoutKey(t *testing.T) {
	t.Setenv("GOFI_ASSISTANT_API_KEY", "")
	t.Setenv("GOFI_ASSISTANT_API_KEY_FILE", "")
	m := NewManager(&FileStore{Path: filepath.Join(t.TempDir(), "a.yaml")})
	s := NewService(m, &stubActions{}, nil)
	if _, err := s.Chat(context.Background(), "", "hi", "", CallMeta{}, nil); err != ErrDisabled {
		t.Fatalf("disabled: %v", err)
	}
	if _, err := m.Save(context.Background(), Configuration{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Chat(context.Background(), "", "hi", "", CallMeta{}, nil); err != ErrNoKey {
		t.Fatalf("no key: %v", err)
	}
}

func TestPrivateEndpointRefusedByDefault(t *testing.T) {
	fake := &fakeMessages{replies: []string{textReply("hi")}}
	server := httptest.NewServer(fake)
	defer server.Close()
	t.Setenv("GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT", "")
	t.Setenv("GOFI_ASSISTANT_API_KEY", "")
	t.Setenv("GOFI_ASSISTANT_API_KEY_FILE", "")
	t.Setenv("GOFI_ASSISTANT_BASE_URL", "")
	if _, _, err := Check(context.Background(), Configuration{Enabled: true, APIKey: "sk-test", BaseURL: server.URL}); err == nil || !strings.Contains(err.Error(), "private or local address") {
		t.Fatalf("loopback endpoint accepted: %v", err)
	}
	if len(fake.requests) != 0 {
		t.Fatal("the request reached the private endpoint")
	}
	t.Setenv("GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT", "1")
	if _, _, err := Check(context.Background(), Configuration{Enabled: true, APIKey: "sk-test", BaseURL: server.URL}); err != nil {
		t.Fatalf("opt-in: %v", err)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	leaked := false
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("x-api-key") != ""
		w.WriteHeader(200)
	}))
	defer sink.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	t.Setenv("GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT", "1")
	t.Setenv("GOFI_ASSISTANT_BASE_URL", "")
	if _, _, err := Check(context.Background(), Configuration{Enabled: true, APIKey: "sk-test", BaseURL: redirector.URL}); err == nil {
		t.Fatal("a redirect answered the probe")
	}
	if leaked {
		t.Fatal("the key followed a redirect to another host")
	}
}

func TestEnvironmentLocksEndpointAndKey(t *testing.T) {
	t.Setenv("GOFI_ASSISTANT_BASE_URL", "https://gateway.example/v1")
	t.Setenv("GOFI_ASSISTANT_API_KEY", "sk-env")
	m := NewManager(&FileStore{Path: filepath.Join(t.TempDir(), "a.yaml")})
	ctx := context.Background()
	if _, err := m.Save(ctx, Configuration{Enabled: true, BaseURL: "https://attacker.example/"}); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("endpoint change accepted: %v", err)
	}
	if _, err := m.Save(ctx, Configuration{Enabled: true, APIKey: "sk-other"}); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("key change accepted: %v", err)
	}
	c, err := m.Save(ctx, Configuration{Enabled: true, Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	if key, source := Key(c); key != "sk-env" || source != "environment" {
		t.Fatalf("environment key must win: %q %q", key, source)
	}
	if Effective(c).BaseURL != "https://gateway.example/v1" || !EnvironmentLocks().Endpoint || !EnvironmentLocks().Key {
		t.Fatalf("locks: %+v %+v", Effective(c), EnvironmentLocks())
	}
}

func TestEveryMutatingCallBecomesAProposal(t *testing.T) {
	two := sse(
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"apply_flow","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"flow_yaml\":\"name: a\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"b","name":"apply_expression","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"name\":\"x\",\"expression\":\"true\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	)
	fake := &fakeMessages{replies: []string{two, textReply("done")}}
	s, actions := newTestService(t, fake, true)
	var errs int
	cid, err := s.Chat(context.Background(), "", "do both", "", CallMeta{Author: "eve"}, func(ev Event) error {
		if ev.Kind == "tool_result" && ev.IsError {
			errs++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(actions.calls) != 0 || errs != 0 {
		t.Fatalf("calls %v errors %d", actions.calls, errs)
	}
	_, conv := s.conversation(cid)
	if len(conv.proposals) != 2 {
		t.Fatalf("expected two pending proposals, got %d", len(conv.proposals))
	}
}

func TestEndpointErrorsDoNotEchoBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "req_1")
		http.Error(w, "SECRET-INTERNAL-PAGE", http.StatusBadGateway)
	}))
	defer server.Close()
	t.Setenv("GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT", "1")
	t.Setenv("GOFI_ASSISTANT_BASE_URL", "")
	_, _, err := Check(context.Background(), Configuration{Enabled: true, APIKey: "sk-test", BaseURL: server.URL})
	if err == nil || strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "502") {
		t.Fatalf("error %v", err)
	}
}

func TestConcurrentTurnsAreBounded(t *testing.T) {
	fake := &fakeMessages{}
	s, _ := newTestService(t, fake, false)
	for i := 0; i < MaxConcurrentTurns; i++ {
		s.turns <- struct{}{}
	}
	if _, err := s.Chat(context.Background(), "", "hi", "", CallMeta{}, func(Event) error { return nil }); err != ErrBusy {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	for i := 0; i < MaxConcurrentTurns; i++ {
		<-s.turns
	}
	id, conv := s.conversation("")
	conv.mu.Lock()
	_, err := s.Chat(context.Background(), id, "hi", "", CallMeta{}, func(Event) error { return nil })
	conv.mu.Unlock()
	if err != ErrReplyInProgress {
		t.Fatalf("expected ErrReplyInProgress, got %v", err)
	}
}

func TestToolApprovalAllPausesForDecision(t *testing.T) {
	fake := &fakeMessages{replies: []string{toolUseReply("list_flows", "{}"), textReply("ok")}}
	s, actions := newTestService(t, fake, false)
	c, _ := s.manager.Get(context.Background())
	c.ToolApproval = ApproveAll
	if _, err := s.manager.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	var id string
	var approvedResult Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.Chat(context.Background(), "", "list", "", CallMeta{}, func(ev Event) error {
			if ev.Kind == "tool_call" && ev.NeedsApproval {
				id = ev.ToolID
				// Decide from "the portal" while the turn waits.
				go func() {
					for {
						s.mu.Lock()
						var conv *conversation
						for _, c := range s.convs {
							conv = c
						}
						s.mu.Unlock()
						conv.pmu.Lock()
						_, waiting := conv.pending[id]
						conv.pmu.Unlock()
						if waiting {
							break
						}
						time.Sleep(5 * time.Millisecond)
					}
					var cid string
					s.mu.Lock()
					for key := range s.convs {
						cid = key
					}
					s.mu.Unlock()
					if _, _, err := s.Confirm(context.Background(), cid, id, true, CallMeta{}); err != nil {
						t.Error(err)
					}
				}()
			}
			if ev.Kind == "tool_result" {
				approvedResult = ev
			}
			return nil
		})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn did not finish")
	}
	if id == "" || approvedResult.IsError || strings.Join(actions.calls, ",") != "list_flows" {
		t.Fatalf("id %q result %+v calls %v", id, approvedResult, actions.calls)
	}
}

func TestToolApprovalDeniedSkipsTheCall(t *testing.T) {
	fake := &fakeMessages{replies: []string{toolUseReply("list_flows", "{}"), textReply("ok")}}
	s, actions := newTestService(t, fake, false)
	c, _ := s.manager.Get(context.Background())
	c.ToolApproval = ApproveAll
	if _, err := s.manager.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	var result Event
	var cid string
	cid, err := s.Chat(context.Background(), "", "list", "", CallMeta{}, func(ev Event) error {
		if ev.Kind == "tool_call" && ev.NeedsApproval {
			go func() {
				time.Sleep(20 * time.Millisecond)
				s.mu.Lock()
				var key string
				for k := range s.convs {
					key = k
				}
				s.mu.Unlock()
				_, _, _ = s.Confirm(context.Background(), key, ev.ToolID, false, CallMeta{})
			}()
		}
		if ev.Kind == "tool_result" {
			result = ev
		}
		return nil
	})
	if err != nil || cid == "" {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Output, "denied") || len(actions.calls) != 0 {
		t.Fatalf("result %+v calls %v", result, actions.calls)
	}
}

func TestConversationsAndTranscript(t *testing.T) {
	fake := &fakeMessages{replies: []string{toolUseReply("list_flows", "{}"), textReply("One flow.")}}
	s, _ := newTestService(t, fake, false)
	id, err := s.Chat(context.Background(), "", "what flows exist?", "#/flows", CallMeta{}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	list := s.Conversations()
	if len(list) != 1 || list[0].ID != id || list[0].Title != "what flows exist?" || list[0].Messages != 4 {
		t.Fatalf("summaries %+v", list)
	}
	messages, pending, ok := s.Transcript(id)
	if !ok || len(pending) != 0 || len(messages) != 4 {
		t.Fatalf("transcript ok=%v pending=%d messages=%d", ok, len(pending), len(messages))
	}
	if messages[0].Text != "what flows exist?" || messages[1].Calls[0].Name != "list_flows" || messages[2].Role != "tool" || PlainOutput(messages[2].Results[0].Output) != "- name: telemetry\n" || messages[3].Text != "One flow." {
		t.Fatalf("transcript %+v", messages)
	}
	if _, _, ok := s.Transcript("missing"); ok {
		t.Fatal("unknown conversation found")
	}
}
