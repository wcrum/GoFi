package assistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// openAIProvider speaks the OpenAI Chat Completions API over plain HTTP:
// Groq, vLLM, LiteLLM, Ollama, OpenRouter, and any other endpoint that
// implements /chat/completions with streaming and function calling. The
// base URL is the API root, conventionally ending in /v1.
type openAIProvider struct {
	config Configuration
	key    string
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	// Index is a pointer so a delta without one (some servers) is told
	// apart from index 0.
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type oaRequest struct {
	Model           string          `json:"model"`
	Messages        []oaMessage     `json:"messages"`
	Tools           []oaTool        `json:"tools,omitempty"`
	Stream          bool            `json:"stream"`
	StreamOptions   *oaStreamOpts   `json:"stream_options,omitempty"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ToolChoice      json.RawMessage `json:"tool_choice,omitempty"`
}

type oaStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type oaUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

type oaChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta        oaMessage `json:"delta"`
		Message      oaMessage `json:"message"`
		FinishReason *string   `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaUsage `json:"usage"`
}

func text(s string) *string { return &s }

func toOpenAI(system string, messages []message) []oaMessage {
	out := []oaMessage{{Role: "system", Content: text(system)}}
	for _, m := range messages {
		switch m.Role {
		case "user":
			out = append(out, oaMessage{Role: "user", Content: text(m.Text)})
		case "assistant":
			item := oaMessage{Role: "assistant"}
			if strings.TrimSpace(m.Text) != "" {
				item.Content = text(m.Text)
			}
			for _, call := range m.Calls {
				tc := oaToolCall{ID: call.ID, Type: "function"}
				tc.Function.Name = call.Name
				tc.Function.Arguments = string(call.Input)
				if tc.Function.Arguments == "" {
					tc.Function.Arguments = "{}"
				}
				item.ToolCalls = append(item.ToolCalls, tc)
			}
			out = append(out, item)
		case "tool":
			for _, r := range m.Results {
				output := r.Output
				if r.IsError {
					output = "error: " + output
				}
				out = append(out, oaMessage{Role: "tool", ToolCallID: r.ID, Content: text(output)})
			}
		}
	}
	return out
}

func (p *openAIProvider) endpoint(path string) string {
	base := strings.TrimRight(p.config.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return base + path
}

// post sends one request, retrying 429 and 5xx answers twice with the
// endpoint's Retry-After (capped) or a short backoff, as the Anthropic SDK
// does for its path.
func (p *openAIProvider) post(ctx context.Context, body any) (*http.Response, error) {
	var last error
	for attempt := 0; attempt <= 2; attempt++ {
		resp, retryAfter, err := p.postOnce(ctx, body)
		if err == nil {
			return resp, nil
		}
		last = err
		if retryAfter < 0 || attempt == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, last
		case <-time.After(retryAfter):
		}
	}
	return nil, last
}

// postOnce returns retryAfter >= 0 when the answer is worth retrying.
func (p *openAIProvider) postOnce(ctx context.Context, body any) (*http.Response, time.Duration, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, -1, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint("/chat/completions"), bytes.NewReader(data))
	if err != nil {
		cancel()
		return nil, -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+p.key)
	resp, err := httpClient().Do(req)
	if err != nil {
		cancel()
		if errors.Is(err, ErrPrivateEndpoint) || ctx.Err() != nil {
			return nil, -1, endpointError(err)
		}
		return nil, time.Second, endpointError(err) // connection failures are retried once or twice
	}
	resp.Body = cancelOnClose{resp.Body, cancel}
	if resp.StatusCode/100 != 2 {
		id := clipText(resp.Header.Get("x-request-id"), 64)
		if id == "" {
			id = clipText(resp.Header.Get("cf-ray"), 64)
		}
		retryAfter := time.Duration(-1)
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			retryAfter = 2 * time.Second
			if seconds, err := strconv.ParseFloat(strings.TrimSpace(resp.Header.Get("Retry-After")), 64); err == nil && seconds > 0 {
				retryAfter = min(time.Duration(seconds*float64(time.Second)), 10*time.Second)
			}
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, retryAfter, &EndpointError{Status: resp.StatusCode, RequestID: id}
	}
	return resp, -1, nil
}

// cancelOnClose releases the request deadline when the body is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	c.cancel()
	return c.ReadCloser.Close()
}

func (p *openAIProvider) check(ctx context.Context, c Configuration, _ string) (string, error) {
	ok := "Reply with the single word OK."
	resp, err := p.post(ctx, oaRequest{Model: c.ModelOrDefault(), Messages: []oaMessage{{Role: "user", Content: &ok}}, MaxTokens: 16})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out oaChunk
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", errors.New("model endpoint: the reply was not a chat completion; check the provider and base URL")
	}
	if out.Model == "" {
		out.Model = c.ModelOrDefault()
	}
	return clipText(out.Model, 128), nil
}

func (p *openAIProvider) complete(ctx context.Context, req completion, onText func(string) bool) (reply, error) {
	body := oaRequest{Model: req.Model, Messages: toOpenAI(req.System, req.Messages), Stream: true, StreamOptions: &oaStreamOpts{IncludeUsage: true}, MaxTokens: req.MaxTokens, ReasoningEffort: req.Effort}
	for _, t := range req.Tools {
		schema, err := json.Marshal(t.Schema)
		if err != nil {
			return reply{}, err
		}
		tool := oaTool{Type: "function"}
		tool.Function.Name, tool.Function.Description, tool.Function.Parameters = t.Name, t.Description, schema
		body.Tools = append(body.Tools, tool)
	}
	resp, err := p.post(ctx, body)
	if err != nil {
		return reply{}, err
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		// Some servers ignore stream=true; read one completion instead.
		var out oaChunk
		if err := json.NewDecoder(io.LimitReader(resp.Body, MaxResponseBytes)).Decode(&out); err != nil || len(out.Choices) == 0 {
			return reply{}, errors.New("model endpoint: the reply was not a chat completion; check the provider and base URL")
		}
		choice := out.Choices[0]
		r := reply{StopReason: finishReason(choice.FinishReason, len(choice.Message.ToolCalls) > 0)}
		if choice.Message.Content != nil {
			r.Text = *choice.Message.Content
			if r.Text != "" {
				onText(r.Text)
			}
		}
		r.Calls = convertCalls(choice.Message.ToolCalls)
		if out.Usage != nil {
			r.InputTokens, r.OutputTokens = out.Usage.PromptTokens, out.Usage.CompletionTokens
		}
		return r, nil
	}
	var r reply
	var textBuf strings.Builder
	calls := map[int]*oaToolCall{}
	var order []int
	var finish *string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	argBytes := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk oaChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			r.InputTokens, r.OutputTokens = chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil && *choice.Delta.Content != "" {
				if textBuf.Len()+len(*choice.Delta.Content) > MaxReplyText {
					return reply{}, ErrReplyTooLong
				}
				textBuf.WriteString(*choice.Delta.Content)
				if !onText(*choice.Delta.Content) {
					return reply{}, errStopped{}
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				index := len(order)
				if tc.Index != nil {
					index = *tc.Index
				} else if tc.ID == "" && len(order) > 0 {
					index = order[len(order)-1] // a continuation without index or id
				} else if tc.ID != "" {
					for _, known := range order {
						if calls[known].ID == tc.ID {
							index = known
						}
					}
				}
				argBytes += len(tc.Function.Arguments)
				if argBytes > MaxCallArguments {
					return reply{}, ErrReplyTooLong
				}
				current, ok := calls[index]
				if !ok {
					if len(order) >= MaxCallsPerReply {
						return reply{}, ErrTooManyCalls
					}
					copy := tc
					calls[index] = &copy
					order = append(order, index)
					continue
				}
				if tc.ID != "" {
					current.ID = tc.ID
				}
				if tc.Function.Name != "" {
					current.Function.Name = tc.Function.Name
				}
				current.Function.Arguments += tc.Function.Arguments
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finish = choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, ErrResponseTooLarge) {
			return reply{}, ErrResponseTooLarge
		}
		return reply{}, endpointError(err)
	}
	var ordered []oaToolCall
	for _, index := range order {
		ordered = append(ordered, *calls[index])
	}
	r.Text = textBuf.String()
	r.Calls = convertCalls(ordered)
	r.StopReason = finishReason(finish, len(r.Calls) > 0)
	return r, nil
}

func convertCalls(calls []oaToolCall) []toolCall {
	var out []toolCall
	for i, tc := range calls {
		id := tc.ID
		if id == "" {
			id = newToolCallID(i)
		}
		args := strings.TrimSpace(tc.Function.Arguments)
		if args == "" || !json.Valid([]byte(args)) {
			args = "{}"
		}
		out = append(out, toolCall{ID: id, Name: tc.Function.Name, Input: json.RawMessage(args)})
	}
	return out
}

func finishReason(reason *string, hasCalls bool) string {
	if hasCalls {
		return "tool_use"
	}
	if reason == nil {
		return "end_turn"
	}
	switch *reason {
	case "length":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	case "tool_calls", "function_call":
		return "tool_use"
	}
	return "end_turn"
}
