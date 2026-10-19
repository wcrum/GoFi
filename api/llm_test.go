package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wcrum/gofi/assistant"
	"github.com/wcrum/gofi/sandbox"
)

type llmTestBackend struct{}

func (llmTestBackend) Start(context.Context, sandbox.Definition, string) (string, error) {
	return "http://worker:8080", nil
}
func (llmTestBackend) Stop(context.Context, string) error { return nil }

// llmFixture is a proxy in front of a fake model endpoint, with one AI
// library and one plain library whose tokens the test knows.
type llmFixture struct {
	proxy    *httptest.Server
	upstream *httptest.Server
	bot      *assistant.Manager
	manager  *sandbox.Supervisor
	aiToken  string
	plain    string
	// what the upstream saw last
	mu        sync.Mutex
	gotPath   string
	gotHeader http.Header
	gotBody   map[string]any
	calls     int
}

func newLLMFixture(t *testing.T, provider string, answer func(w http.ResponseWriter, r *http.Request)) *llmFixture {
	return newLLMFixtureWithLimits(t, provider, answer, 0, LLMPerLibraryInFlight, LLMGlobalInFlight)
}

func newLLMFixtureWithLimits(t *testing.T, provider string, answer func(w http.ResponseWriter, r *http.Request), maxTokens, perLibrary, global int) *llmFixture {
	t.Helper()
	t.Setenv("GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT", "1") // httptest listens on loopback
	t.Setenv("GOFI_ASSISTANT_API_KEY", "")
	t.Setenv("GOFI_ASSISTANT_BASE_URL", "")
	f := &llmFixture{}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.gotPath, f.gotHeader, f.calls = r.URL.Path, r.Header.Clone(), f.calls+1
		f.gotBody = map[string]any{}
		_ = json.Unmarshal(body, &f.gotBody)
		f.mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(f.upstream.Close)
	f.bot = assistant.NewManager(&assistant.FileStore{Path: filepath.Join(t.TempDir(), "assistant.yaml")})
	if _, err := f.bot.Save(context.Background(), assistant.Configuration{Enabled: true, Provider: provider, BaseURL: f.upstream.URL + "/v1", Model: "configured-model", APIKey: "real-key", AllowFunctions: true}); err != nil {
		t.Fatal(err)
	}
	f.manager = sandbox.New(llmTestBackend{}, nil)
	f.manager.AIEnvironment = func() (map[string]string, []sandbox.EgressRule, bool) {
		return map[string]string{sandbox.AIEnvProxyURL: "http://controller:9091", sandbox.AIEnvProvider: provider, sandbox.AIEnvModel: "configured-model"}, nil, true
	}
	ctx := context.Background()
	for _, d := range []sandbox.Definition{{Name: "ai", SourceCode: "import gofi", AI: true}, {Name: "plain", SourceCode: "import gofi"}} {
		if err := f.manager.Apply(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	bindings, _ := f.manager.Bindings(ctx)
	f.aiToken, f.plain = bindings["ai"].Token, bindings["plain"].Token
	f.proxy = httptest.NewServer(newLLMProxy(f.manager, f.bot, maxTokens, perLibrary, global))
	t.Cleanup(f.proxy.Close)
	return f
}

func (f *llmFixture) upstreamSaw() (string, http.Header, map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotPath, f.gotHeader, f.gotBody
}

func (f *llmFixture) post(t *testing.T, path, token, body string, headers ...string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.proxy.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestLLMProxyForwardsForAILibrariesOnly(t *testing.T) {
	f := newLLMFixture(t, assistant.ProviderOpenAI, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	})
	// Unauthenticated and wrong tokens are refused before anything reaches
	// the endpoint; with the deployment's switch off, so is every worker.
	for _, tc := range []struct {
		token string
		want  int
	}{{"", 401}, {"not-a-token", 401}} {
		if resp := f.post(t, "/v1/chat/completions", tc.token, `{"messages":[]}`); resp.StatusCode != tc.want {
			t.Fatalf("token %q: %d", tc.token, resp.StatusCode)
		}
	}
	off := assistant.Configuration{Enabled: true, Provider: assistant.ProviderOpenAI, BaseURL: f.upstream.URL + "/v1", Model: "configured-model", APIKey: "real-key"}
	if _, err := f.bot.Save(context.Background(), off); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{f.aiToken, f.plain} {
		resp := f.post(t, "/v1/chat/completions", token, `{"messages":[]}`)
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 403 || !strings.Contains(string(body), "not enabled for functions") {
			t.Fatalf("switch off: %d %s", resp.StatusCode, body)
		}
	}
	if path, _, _ := f.upstreamSaw(); path != "" {
		t.Fatal("a refused call must not reach the endpoint")
	}
	off.AllowFunctions = true
	if _, err := f.bot.Save(context.Background(), off); err != nil {
		t.Fatal(err)
	}
	// With it on, a library that never set the old per-library flag passes too.
	if resp := f.post(t, "/v1/chat/completions", f.plain, `{"messages":[]}`); resp.StatusCode != 200 {
		t.Fatalf("plain library with the switch on: %d", resp.StatusCode)
	}
	resp := f.post(t, "/v1/chat/completions", f.aiToken, `{"messages":[{"role":"user","content":"hi"}]}`)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	path, header, got := f.upstreamSaw()
	if path != "/v1/chat/completions" || header.Get("Authorization") != "Bearer real-key" {
		t.Fatalf("upstream saw %s %v", path, header)
	}
	if got["model"] != "configured-model" {
		t.Fatalf("an empty model must become the deployment's: %v", got)
	}
	// The anthropic SDK presents the token as x-api-key; that works too, but
	// the deployment speaks openai, so the messages path is refused.
	req, _ := http.NewRequest(http.MethodPost, f.proxy.URL+"/v1/messages", strings.NewReader(`{"messages":[]}`))
	req.Header.Set("X-Api-Key", f.aiToken)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Fatalf("provider mismatch: %d", resp2.StatusCode)
	}
	// The deployment's model, named explicitly, passes; another model is
	// refused (cost amplification), and so are oversized or non-object bodies.
	if resp := f.post(t, "/v1/chat/completions", f.aiToken, `{"model":"configured-model","messages":[]}`); resp.StatusCode != 200 {
		t.Fatalf("named configured model: %d", resp.StatusCode)
	}
	resp = f.post(t, "/v1/chat/completions", f.aiToken, `{"model":"gpt-5-pro","messages":[]}`)
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 400 || !strings.Contains(string(body), "gpt-5-pro") || !strings.Contains(string(body), "configured-model") {
		t.Fatalf("other model: %d %s", resp.StatusCode, body)
	}
	if resp := f.post(t, "/v1/chat/completions", f.aiToken, `{"messages":"`+strings.Repeat("x", llmMaxRequestBytes)+`"}`); resp.StatusCode != 413 {
		t.Fatalf("1 MiB cap: %d", resp.StatusCode)
	}
	if resp := f.post(t, "/v1/chat/completions", f.aiToken, `[1]`); resp.StatusCode != 400 {
		t.Fatalf("non-object: %d", resp.StatusCode)
	}
	// The listener's own health probe.
	health, err := http.Get(f.proxy.URL + "/healthz")
	if err != nil || health.StatusCode != 200 {
		t.Fatalf("healthz: %v %v", err, health)
	}
	health.Body.Close()
	if resp := f.post(t, "/other", f.aiToken, `{}`); resp.StatusCode != 404 {
		t.Fatalf("unknown path: %d", resp.StatusCode)
	}
}

func TestLLMProxyCapsOutputTokens(t *testing.T) {
	f := newLLMFixtureWithLimits(t, assistant.ProviderOpenAI, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }, 1000, LLMPerLibraryInFlight, LLMGlobalInFlight)
	f.post(t, "/v1/chat/completions", f.aiToken, `{"messages":[],"max_tokens":50000,"max_completion_tokens":2000,"temperature":0}`)
	_, _, got := f.upstreamSaw()
	if got["max_tokens"] != 1000.0 || got["max_completion_tokens"] != 1000.0 || got["temperature"] != 0.0 {
		t.Fatalf("clamped body: %v", got)
	}
	f.post(t, "/v1/chat/completions", f.aiToken, `{"messages":[],"max_tokens":200}`)
	if _, _, got = f.upstreamSaw(); got["max_tokens"] != 200.0 {
		t.Fatalf("a value under the cap must pass unchanged: %v", got)
	}
	if _, ok := got["max_completion_tokens"]; ok {
		t.Fatal("an absent field must stay absent")
	}
}

func TestLLMProxyAnthropicHeadersAndStreaming(t *testing.T) {
	f := newLLMFixture(t, assistant.ProviderAnthropic, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)
		for _, event := range []string{"event: message_start\ndata: {}\n\n", "event: message_stop\ndata: {}\n\n"} {
			_, _ = io.WriteString(w, event)
			flusher.Flush()
			time.Sleep(20 * time.Millisecond)
		}
	})
	resp := f.post(t, "/v1/messages", f.aiToken, `{"messages":[{"role":"user","content":"hi"}],"stream":true,"max_tokens":10}`, "Anthropic-Beta", "tools-2024", "Anthropic-Version", "2023-06-01")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	first := make([]byte, 64)
	n, _ := resp.Body.Read(first)
	if !strings.HasPrefix(string(first[:n]), "event: message_start") {
		t.Fatalf("first chunk: %q", first[:n])
	}
	rest, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(first[:n])+string(rest), "message_stop") {
		t.Fatal("stream was cut")
	}
	// The endpoint saw the real key as x-api-key and the version header;
	// never the worker's token, and never a beta opt-in from a worker.
	path, header, got := f.upstreamSaw()
	if path != "/v1/messages" || header.Get("X-Api-Key") != "real-key" || header.Get("Anthropic-Version") != "2023-06-01" {
		t.Fatalf("upstream saw %s %v", path, header)
	}
	if header.Get("Anthropic-Beta") != "" {
		t.Fatal("anthropic-beta must not be forwarded")
	}
	if strings.Contains(header.Get("Authorization"), f.aiToken) || got["model"] != "configured-model" {
		t.Fatalf("upstream headers %v body %v", header, got)
	}
}

func TestLLMProxyLimitsCallsInFlight(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 16)
	f := newLLMFixtureWithLimits(t, assistant.ProviderOpenAI, func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = w.Write([]byte(`{}`))
	}, 0, 2, 3)
	defer close(release)
	codes := make(chan int, 8)
	for i := 0; i < 2; i++ {
		go func() { codes <- f.post(t, "/v1/chat/completions", f.aiToken, `{"messages":[]}`).StatusCode }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("calls did not reach the endpoint")
		}
	}
	// The library's two slots are taken: a third call is refused at once
	// with Retry-After, without touching the endpoint.
	third := f.post(t, "/v1/chat/completions", f.aiToken, `{"messages":[]}`)
	if third.StatusCode != 429 || third.Header.Get("Retry-After") == "" {
		t.Fatalf("third call: %d %v", third.StatusCode, third.Header)
	}
	f.mu.Lock()
	calls := f.calls
	f.mu.Unlock()
	if calls != 2 {
		t.Fatalf("endpoint calls: %d", calls)
	}
	release <- struct{}{}
	release <- struct{}{}
	for i := 0; i < 2; i++ {
		if code := <-codes; code != 200 {
			t.Fatalf("in-flight call ended with %d", code)
		}
	}
	// Slots are released: the next call goes through.
	go func() { codes <- f.post(t, "/v1/chat/completions", f.aiToken, `{"messages":[]}`).StatusCode }()
	<-started
	release <- struct{}{}
	if code := <-codes; code != 200 {
		t.Fatalf("after release: %d", code)
	}
}

func TestLLMProxyRefusesWhenTheEndpointIsOffOrOnAStandby(t *testing.T) {
	f := newLLMFixture(t, assistant.ProviderOpenAI, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	if _, err := f.bot.Save(context.Background(), assistant.Configuration{Enabled: false, Provider: assistant.ProviderOpenAI, BaseURL: f.upstream.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if resp := f.post(t, "/v1/chat/completions", f.aiToken, `{}`); resp.StatusCode != 503 {
		t.Fatalf("disabled endpoint: %d", resp.StatusCode)
	}
	f.manager.SetPassive(true)
	if resp := f.post(t, "/v1/chat/completions", f.aiToken, `{}`); resp.StatusCode != 503 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("standby: %d %v", resp.StatusCode, resp.Header)
	}
}

func TestLLMUpstreamAndPrepareBody(t *testing.T) {
	if got := llmUpstream(assistant.Configuration{}, assistant.ProviderOpenAI); got != "https://api.openai.com/v1/chat/completions" {
		t.Fatal(got)
	}
	if got := llmUpstream(assistant.Configuration{BaseURL: "https://api.groq.com/openai/v1/"}, assistant.ProviderOpenAI); got != "https://api.groq.com/openai/v1/chat/completions" {
		t.Fatal(got)
	}
	if got := llmUpstream(assistant.Configuration{}, assistant.ProviderAnthropic); got != "https://api.anthropic.com/v1/messages" {
		t.Fatal(got)
	}
	if got := llmUpstream(assistant.Configuration{BaseURL: "https://gateway.example/v1"}, assistant.ProviderAnthropic); got != "https://gateway.example/v1/messages" {
		t.Fatal(got)
	}
	body, err := prepareBody([]byte(`{"model":"","messages":[],"max_tokens":99999}`), "m", 8192)
	if err != nil || !strings.Contains(string(body), `"model":"m"`) || !strings.Contains(string(body), `"max_tokens":8192`) {
		t.Fatalf("%s %v", body, err)
	}
	if _, err := prepareBody([]byte(`{"model":"other"}`), "m", 8192); err == nil {
		t.Fatal("another model accepted")
	}
	if _, err := prepareBody([]byte(`"x"`), "m", 8192); err == nil {
		t.Fatal("non-object accepted")
	}
}
