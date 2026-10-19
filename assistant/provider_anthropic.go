package assistant

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// anthropicProvider speaks the Anthropic Messages API through the official
// SDK: api.anthropic.com or any gateway that implements it.
type anthropicProvider struct {
	client anthropic.Client
}

func (p anthropicProvider) check(ctx context.Context, c Configuration, _ string) (string, error) {
	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.ModelOrDefault()),
		MaxTokens: 16,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("Reply with the single word OK."))},
	})
	if err != nil {
		return "", endpointError(err)
	}
	return clipText(string(resp.Model), 128), nil
}

func toAnthropic(messages []message) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "user":
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Text)))
		case "assistant":
			var blocks []anthropic.ContentBlockParamUnion
			if strings.TrimSpace(m.Text) != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Text))
			}
			for _, call := range m.Calls {
				var input any = map[string]any{}
				if len(call.Input) > 0 {
					_ = json.Unmarshal(call.Input, &input)
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(call.ID, input, call.Name))
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
		case "tool":
			var blocks []anthropic.ContentBlockParamUnion
			for _, r := range m.Results {
				blocks = append(blocks, anthropic.NewToolResultBlock(r.ID, r.Output, r.IsError))
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.NewUserMessage(blocks...))
			}
		}
	}
	return out
}

func (p anthropicProvider) complete(ctx context.Context, req completion, onText func(string) bool) (reply, error) {
	var tools []anthropic.ToolUnionParam
	for _, t := range req.Tools {
		tools = append(tools, t.param())
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		System:    []anthropic.TextBlockParam{{Text: req.System, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		Messages:  toAnthropic(req.Messages),
		Tools:     tools,
	}
	if req.Effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(req.Effort)}
	}
	stream := p.client.Messages.NewStreaming(ctx, params)
	var msg anthropic.Message
	stopped := false
	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			slog.Debug("assistant stream accumulate", "error", err)
		}
		if delta, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if td, ok := delta.Delta.AsAny().(anthropic.TextDelta); ok && td.Text != "" && !onText(td.Text) {
				stopped = true
				break
			}
		}
	}
	if stopped {
		return reply{}, errStopped{}
	}
	if err := stream.Err(); err != nil {
		return reply{}, endpointError(err)
	}
	out := reply{InputTokens: msg.Usage.InputTokens, OutputTokens: msg.Usage.OutputTokens, StopReason: string(msg.StopReason)}
	if out.StopReason == "" {
		out.StopReason = "end_turn"
	}
	var text strings.Builder
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			text.WriteString(b.Text)
		case anthropic.ToolUseBlock:
			out.Calls = append(out.Calls, toolCall{ID: b.ID, Name: b.Name, Input: json.RawMessage(b.Input)})
		}
	}
	out.Text = text.String()
	if len(out.Calls) > MaxCallsPerReply {
		return reply{}, ErrTooManyCalls
	}
	return out, nil
}
