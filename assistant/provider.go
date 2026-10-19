package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Providers speak the two wire protocols a configured endpoint may offer.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)

// ProviderOrDefault is the protocol the endpoint speaks.
func (c Configuration) ProviderOrDefault() string {
	if c.Provider == "" {
		return ProviderAnthropic
	}
	return c.Provider
}

// message is one entry of a conversation in provider-neutral form: a user
// text, an assistant reply (text and/or tool calls), or the results of the
// previous assistant's tool calls.
type message struct {
	Role    string // "user", "assistant", or "tool"
	Text    string
	Calls   []toolCall
	Results []toolResult
}

type toolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type toolResult struct {
	ID      string
	Name    string
	Output  string
	IsError bool
}

// completion is one model request.
type completion struct {
	Model     string
	System    string
	Messages  []message
	Tools     []Tool
	MaxTokens int
	Effort    string
}

// reply is what the model answered: streamed text (already delivered
// through onText), the tool calls to run, and why it stopped
// ("end_turn", "tool_use", "max_tokens", "refusal").
type reply struct {
	Text         string
	Calls        []toolCall
	StopReason   string
	InputTokens  int64
	OutputTokens int64
}

// provider turns a completion into a streamed reply. onText returns false
// when the caller cannot take more (the client went away).
type provider interface {
	complete(ctx context.Context, req completion, onText func(string) bool) (reply, error)
	// check sends one tiny request and returns the model that answered.
	check(ctx context.Context, c Configuration, key string) (string, error)
}

func providerFor(c Configuration, key string) (provider, error) {
	switch c.ProviderOrDefault() {
	case ProviderAnthropic:
		return anthropicProvider{client: newClient(c, key)}, nil
	case ProviderOpenAI:
		return &openAIProvider{config: Effective(c), key: key}, nil
	}
	return nil, fmt.Errorf("unknown provider %q", c.Provider)
}

// Bounds on what one reply may carry, whatever the endpoint sends.
const (
	MaxCallsPerReply = 32
	MaxReplyText     = 256 << 10
	MaxCallArguments = 256 << 10
)

// ErrTooManyCalls rejects a reply with more tool calls than a sane model
// produces; an endpoint that floods calls would otherwise fill the history.
var ErrTooManyCalls = errors.New("model endpoint: the reply carried too many tool calls")

// ErrReplyTooLong rejects streamed text or arguments beyond the bounds.
var ErrReplyTooLong = errors.New("model endpoint: the reply exceeds the size limit")

// errStopped is returned by a provider when onText asked it to stop.
type errStopped struct{}

func (errStopped) Error() string { return "stream stopped by the caller" }

// hint explains the HTTP statuses a misconfigured endpoint produces.
// EndpointError is a model endpoint's HTTP error, without its body (which an
// arbitrary endpoint controls). The chat loop reacts to Status.
type EndpointError struct {
	Status    int
	RequestID string
}

func (e *EndpointError) Error() string {
	msg := fmt.Sprintf("model endpoint answered HTTP %d%s", e.Status, hint(e.Status))
	if e.RequestID != "" {
		msg += " (request " + e.RequestID + ")"
	}
	return msg
}

func hint(status int) string {
	switch status {
	case 401, 403:
		return "; check the API key"
	case 404:
		return "; check the provider, the base URL, and the model id"
	case 413:
		return "; the request exceeds what the endpoint accepts per call (tokens or bytes): start a new conversation, or ask for a smaller section"
	case 429:
		return "; the endpoint is rate limiting this key (tokens or requests per minute); wait a moment or use a smaller model or a paid tier"
	}
	return ""
}

// newToolCallID gives tool calls an id when a provider did not.
func newToolCallID(index int) string { return fmt.Sprintf("call_%d", index) }

// userText is the first user message's text, used by trim.
func (m message) isUserText() bool { return m.Role == "user" && strings.TrimSpace(m.Text) != "" }
