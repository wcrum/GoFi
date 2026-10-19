package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/wcrum/gofi/assistant"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sandbox"
)

// The controller's LLM proxy. A Python library with AI on calls the
// deployment's model (the assistant configuration under Administration)
// through the controller, authenticating with its own worker token; the
// model key stays in the controller and never enters a worker, where the
// debugger could read it. The worker's LangChain client speaks the
// provider's own wire protocol to these paths:
//
//	POST /v1/chat/completions   provider "openai" (Chat Completions)
//	POST /v1/messages           provider "anthropic" (Messages API)
//
// The proxy is served on its own listener (GOFI_LLM_PROXY_ADDR), never on
// the API server: a worker's egress reaches this port and nothing else, so
// a library cannot call the unauthenticated portal RPCs. The request body is
// forwarded with the deployment's model (a request naming another model is
// refused) and its output tokens capped; streaming answers are piped through
// unbuffered. In flight: at most LLMPerLibraryInFlight calls per library and
// LLMGlobalInFlight per controller; more get 429 with Retry-After.
const (
	LLMDefaultMaxOutputTokens = 8192
	LLMPerLibraryInFlight     = 4
	LLMGlobalInFlight         = 32

	llmMaxRequestBytes = 1 << 20
	llmDeadline        = 5 * time.Minute
	anthropicVersion   = "2023-06-01"
)

type llmProxy struct {
	manager         *sandbox.Supervisor
	assistant       *assistant.Manager
	client          *http.Client
	maxOutputTokens int
	perLibraryLimit int
	global          chan struct{}
	mu              sync.Mutex
	perLibrary      map[string]int
}

// NewLLMProxy is the handler for the LLM proxy listener. maxOutputTokens
// caps max_tokens / max_completion_tokens (0: LLMDefaultMaxOutputTokens).
func NewLLMProxy(manager *sandbox.Supervisor, bot *assistant.Manager, maxOutputTokens int) http.Handler {
	return newLLMProxy(manager, bot, maxOutputTokens, LLMPerLibraryInFlight, LLMGlobalInFlight)
}

func newLLMProxy(manager *sandbox.Supervisor, bot *assistant.Manager, maxOutputTokens, perLibrary, global int) http.Handler {
	if maxOutputTokens <= 0 {
		maxOutputTokens = LLMDefaultMaxOutputTokens
	}
	p := &llmProxy{manager: manager, assistant: bot, client: assistant.HTTPClient(), maxOutputTokens: maxOutputTokens, perLibraryLimit: perLibrary, global: make(chan struct{}, global), perLibrary: map[string]int{}}
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Post("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { p.serve(w, r, assistant.ProviderOpenAI) })
	r.Post("/v1/messages", func(w http.ResponseWriter, r *http.Request) { p.serve(w, r, assistant.ProviderAnthropic) })
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		llmError(w, http.StatusNotFound, "the AI proxy serves /v1/chat/completions (openai) and /v1/messages (anthropic)")
	})
	return r
}

// llmError answers in a shape both SDKs report as the message.
func llmError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]string{"type": "gofi_proxy_error", "message": message}})
}

// workerToken is the credential a worker presents: the OpenAI SDK sends a
// bearer token, the Anthropic SDK an x-api-key header.
func workerToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return strings.TrimSpace(r.Header.Get("X-Api-Key"))
}

// llmUpstream is the provider endpoint for one wire protocol.
func llmUpstream(c assistant.Configuration, provider string) string {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if provider == assistant.ProviderOpenAI {
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		return base + "/chat/completions"
	}
	base = strings.TrimSuffix(base, "/v1")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	return base + "/v1/messages"
}

// prepareBody pins the request to the deployment's model (an empty model is
// filled in, another model refused) and clamps the output-token fields to
// maxOutputTokens.
func prepareBody(body []byte, model string, maxOutputTokens int) ([]byte, error) {
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, errors.New("the request body must be a JSON object")
	}
	if named, _ := request["model"].(string); named != "" && named != model {
		return nil, fmt.Errorf("the request names model %q, but this deployment's AI endpoint serves %q; leave model empty (gofi.llm does)", named, model)
	}
	request["model"] = model
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		if n, ok := request[key].(float64); ok && n > float64(maxOutputTokens) {
			request[key] = maxOutputTokens
		}
	}
	return json.Marshal(request)
}

// acquire reserves an in-flight slot for library; ok is false when the
// library or the controller is at its limit.
func (p *llmProxy) acquire(library string) (release func(), ok bool) {
	select {
	case p.global <- struct{}{}:
	default:
		return nil, false
	}
	p.mu.Lock()
	if p.perLibrary[library] >= p.perLibraryLimit {
		p.mu.Unlock()
		<-p.global
		return nil, false
	}
	p.perLibrary[library]++
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		if p.perLibrary[library]--; p.perLibrary[library] <= 0 {
			delete(p.perLibrary, library)
		}
		p.mu.Unlock()
		<-p.global
	}, true
}

func (p *llmProxy) serve(w http.ResponseWriter, r *http.Request, provider string) {
	started := time.Now()
	library, status := "-", 0
	defer func() { observability.LLMProxyCall(library, status, time.Since(started)) }()
	refuse := func(code int, message string) {
		status = code
		llmError(w, code, message)
	}
	if p.manager.Passive() {
		w.Header().Set("Retry-After", "5")
		refuse(http.StatusServiceUnavailable, "this controller replica is a standby; retry against the leader")
		return
	}
	lib, ok := p.manager.LibraryByToken(workerToken(r))
	if !ok {
		refuse(http.StatusUnauthorized, "a Python worker token is required")
		return
	}
	library = lib.Name
	c, err := p.assistant.Get(r.Context())
	if err != nil || !c.Enabled {
		refuse(http.StatusServiceUnavailable, "the deployment's AI endpoint is not enabled under Administration")
		return
	}
	c = assistant.Effective(c)
	if !c.AllowFunctions {
		refuse(http.StatusForbidden, "the deployment's AI endpoint is not enabled for functions; an administrator turns it on under Administration › AI assistant")
		return
	}
	key, _ := assistant.Key(c)
	if key == "" {
		refuse(http.StatusServiceUnavailable, "the deployment's AI endpoint has no API key")
		return
	}
	if configured := c.ProviderOrDefault(); configured != provider {
		refuse(http.StatusBadRequest, fmt.Sprintf("the deployment's AI endpoint speaks %s, not %s; gofi.llm picks the matching client from GOFI_LLM_PROVIDER", configured, provider))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, llmMaxRequestBytes))
	if err != nil {
		refuse(http.StatusRequestEntityTooLarge, "the request exceeds 1 MiB")
		return
	}
	model := c.ModelOrDefault()
	body, err = prepareBody(body, model, p.maxOutputTokens)
	if err != nil {
		refuse(http.StatusBadRequest, err.Error())
		return
	}
	release, ok := p.acquire(lib.Name)
	if !ok {
		w.Header().Set("Retry-After", "1")
		refuse(http.StatusTooManyRequests, fmt.Sprintf("too many AI calls in flight (%d per library, %d per controller); retry shortly", p.perLibraryLimit, cap(p.global)))
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), llmDeadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, llmUpstream(c, provider), bytes.NewReader(body))
	if err != nil {
		refuse(http.StatusBadGateway, "the AI endpoint URL is invalid")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}
	if provider == assistant.ProviderOpenAI {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.Header.Set("X-Api-Key", key)
		version := r.Header.Get("Anthropic-Version")
		if version == "" {
			version = anthropicVersion
		}
		req.Header.Set("Anthropic-Version", version)
		// anthropic-beta is deliberately not forwarded: a worker must not
		// opt the deployment's key into beta features.
	}
	resp, err := p.client.Do(req)
	if err != nil {
		// The transport error names the upstream host; that stays in the
		// controller's log, not in a worker's exception text.
		slog.Warn("AI proxy call failed", "library", lib.Name, "provider", provider, "model", model, "duration", time.Since(started), "error", clipError(err))
		refuse(http.StatusBadGateway, "the model endpoint did not answer; the controller log has the reason")
		return
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// An auth failure body can echo (part of) the key; the worker only
		// learns that the deployment's key was rejected.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		slog.Warn("AI proxy call rejected by the model endpoint", "library", lib.Name, "provider", provider, "model", model, "status", resp.StatusCode)
		refuse(http.StatusBadGateway, "the model endpoint rejected the deployment's API key; check it under Administration › AI assistant")
		return
	}
	for _, name := range []string{"Content-Type", "X-Request-Id", "Request-Id"} {
		if v := resp.Header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	// Piped unbuffered so a streaming answer (text/event-stream) reaches the
	// worker as it is produced. The client bounds the body at 8 MiB.
	flusher, _ := w.(http.Flusher)
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}
	slog.Info("AI proxy call", "library", lib.Name, "provider", provider, "model", model, "status", resp.StatusCode, "duration", time.Since(started))
}

// clipError bounds an endpoint-controlled error for logs and answers; it
// never carries bodies or keys (the transport does not include them).
func clipError(err error) string {
	text := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, err.Error())
	if len(text) > 200 {
		return text[:200] + "..."
	}
	return text
}
