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
	"sync/atomic"
	"testing"
	"testing/fstest"
)

// fakeChatCompletions is a minimal OpenAI-compatible /chat/completions: it
// records request bodies and answers each streaming request with the next
// scripted SSE reply, and non-streaming ones with a fixed completion.
type fakeChatCompletions struct {
	mu       sync.Mutex
	requests []map[string]any
	replies  []string
}

func (f *fakeChatCompletions) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer gsk-test" {
		http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
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
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","object":"chat.completion","model":"openai/gpt-oss-120b","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, reply)
}

func oaSSE(chunks ...string) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString("data: " + c + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func oaToolReply(name, args string) string {
	return oaSSE(
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Looking. "}}]}`,
		fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":%q,"arguments":""}}]}}]}`, name),
		fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}}]}`, args[:len(args)/2]),
		fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}}]}`, args[len(args)/2:]),
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":7}}`,
	)
}

func oaTextReply(text string) string {
	return oaSSE(
		fmt.Sprintf(`{"choices":[{"index":0,"delta":{"role":"assistant","content":%q}}]}`, text),
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":4}}`,
	)
}

func newOpenAIService(t *testing.T, fake *fakeChatCompletions, allowChanges bool) (*Service, *stubActions) {
	t.Helper()
	t.Setenv("GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT", "1")
	t.Setenv("GOFI_ASSISTANT_API_KEY", "")
	t.Setenv("GOFI_ASSISTANT_API_KEY_FILE", "")
	t.Setenv("GOFI_ASSISTANT_BASE_URL", "")
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	m := NewManager(&FileStore{Path: filepath.Join(t.TempDir(), "assistant.yaml")})
	if _, err := m.Save(context.Background(), Configuration{Enabled: true, Provider: ProviderOpenAI, APIKey: "gsk-test", BaseURL: server.URL + "/v1", Model: "openai/gpt-oss-120b", AllowChanges: allowChanges, Effort: "low"}); err != nil {
		t.Fatal(err)
	}
	actions := &stubActions{}
	return NewService(m, actions, fstest.MapFS{"flows.md": {Data: []byte("# Flows\n")}}), actions
}

func TestOpenAICheck(t *testing.T) {
	fake := &fakeChatCompletions{}
	s, _ := newOpenAIService(t, fake, false)
	c, _ := s.manager.Get(context.Background())
	model, _, err := Check(context.Background(), c)
	if err != nil || model != "openai/gpt-oss-120b" {
		t.Fatalf("check: %q %v", model, err)
	}
	if fake.requests[0]["max_tokens"].(float64) != 16 || fake.requests[0]["stream"] == true {
		t.Fatalf("probe request: %v", fake.requests[0])
	}
}

func TestOpenAIChatRunsToolsAndStreams(t *testing.T) {
	fake := &fakeChatCompletions{replies: []string{oaToolReply("get_flow", `{"name":"telemetry"}`), oaTextReply("It listens on TCP.")}}
	s, actions := newOpenAIService(t, fake, false)
	var kinds []string
	var text string
	id, err := s.Chat(context.Background(), "", "what does telemetry do?", "#/flows", CallMeta{}, func(ev Event) error {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == "text" {
			text += ev.Text
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "text,tool_call,tool_result,text,done"; strings.Join(kinds, ",") != want {
		t.Fatalf("events %v", kinds)
	}
	if text != "Looking. It listens on TCP." || strings.Join(actions.calls, ",") != "get_flow" {
		t.Fatalf("text %q calls %v", text, actions.calls)
	}
	first, second := fake.requests[0], fake.requests[1]
	if first["model"] != "openai/gpt-oss-120b" || first["reasoning_effort"] != "low" || first["stream"] != true {
		t.Fatalf("request params: %v", first)
	}
	messages := first["messages"].([]any)
	if messages[0].(map[string]any)["role"] != "system" || !strings.Contains(fmt.Sprint(messages[1]), "#/flows") {
		t.Fatalf("system/user messages: %v", messages[:2])
	}
	tools := first["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if tools[0].(map[string]any)["type"] != "function" || fn["name"] == "" || fn["parameters"] == nil {
		t.Fatalf("tool shape: %v", tools[0])
	}
	// The second request replays the assistant tool call and a tool message
	// with the matching id, so the model can continue.
	history := second["messages"].([]any)
	assistant := history[len(history)-2].(map[string]any)
	toolMsg := history[len(history)-1].(map[string]any)
	if assistant["role"] != "assistant" || !strings.Contains(fmt.Sprint(assistant["tool_calls"]), "call_abc") {
		t.Fatalf("assistant replay: %v", assistant)
	}
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_abc" || !strings.Contains(fmt.Sprint(toolMsg["content"]), "telemetry") {
		t.Fatalf("tool message: %v", toolMsg)
	}
	// A third turn keeps the whole neutral history.
	fake.mu.Lock()
	fake.replies = []string{oaTextReply("Still.")}
	fake.mu.Unlock()
	if _, err := s.Chat(context.Background(), id, "and?", "", CallMeta{}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.requests[2]["messages"].([]any)); n != 6 {
		t.Fatalf("history length %d, want 6 (system + 5)", n)
	}
}

func TestOpenAIProposalsAndErrors(t *testing.T) {
	fake := &fakeChatCompletions{replies: []string{oaToolReply("apply_flow", `{"flow_yaml":"name: x\n"}`), oaTextReply("Proposed.")}}
	s, actions := newOpenAIService(t, fake, true)
	var pid string
	if _, err := s.Chat(context.Background(), "", "save", "", CallMeta{}, func(ev Event) error {
		if ev.Kind == "proposal" {
			pid = ev.ProposalID
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if pid == "" || len(actions.calls) != 0 {
		t.Fatalf("proposal %q calls %v", pid, actions.calls)
	}
	// A wrong path answers 404 with a hint and no body echo.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "INTERNAL PAGE", 404) }))
	defer bad.Close()
	_, _, err := Check(context.Background(), Configuration{Enabled: true, Provider: ProviderOpenAI, APIKey: "gsk-test", BaseURL: bad.URL + "/openai/v1", Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "model id") || strings.Contains(err.Error(), "INTERNAL") {
		t.Fatalf("404: %v", err)
	}
}

func TestPointAtFlowBlockEmitsHighlight(t *testing.T) {
	fake := &fakeChatCompletions{replies: []string{oaToolReply("point_at_flow_block", `{"flow":"telemetry","block":"parse","note":"the parser expects JSON"}`), oaTextReply("See the parse step.")}}
	s, _ := newOpenAIService(t, fake, false)
	var highlights []Event
	if _, err := s.Chat(context.Background(), "", "why does it drop?", "", CallMeta{}, func(ev Event) error {
		if ev.Kind == "highlight" {
			highlights = append(highlights, ev)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(highlights) != 1 || highlights[0].Flow != "telemetry" || highlights[0].Block != "parse" || highlights[0].Text != "the parser expects JSON" {
		t.Fatalf("highlights %+v", highlights)
	}
	if _, err := Tools(&stubActions{}, nil)[0].Run(context.Background(), nil, CallMeta{}, &emitter{send: func(Event) error { return nil }}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIRetriesRateLimits(t *testing.T) {
	t.Setenv("GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT", "1")
	t.Setenv("GOFI_ASSISTANT_BASE_URL", "")
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "0.01")
			http.Error(w, "slow down", 429)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	model, _, err := Check(context.Background(), Configuration{Enabled: true, Provider: ProviderOpenAI, APIKey: "k", BaseURL: server.URL + "/v1", Model: "m"})
	if err != nil || model != "m" || atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("model %q err %v hits %d", model, err, hits)
	}
}
