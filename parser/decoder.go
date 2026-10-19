package parser

import (
	"context"
	"fmt"

	"github.com/wcrum/gofi/codec"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

type decodingParser struct {
	parser Parser
	steps  []spec.CodecStep
	max    int
}

// ValidateDecode checks a decode step configuration.
func ValidateDecode(cfg spec.DecodeCfg) error {
	if cfg.MaxDecodedBytes < 0 {
		return fmt.Errorf("decode: maxDecodedBytes must not be negative")
	}
	for _, step := range cfg.Decoders {
		if err := step.Validate("decoder"); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
	}
	return nil
}

// WithDecode runs cfg's decoders before p parses each frame.
func WithDecode(p Parser, cfg spec.DecodeCfg) (Parser, error) {
	if err := ValidateDecode(cfg); err != nil {
		return nil, err
	}
	if len(cfg.Decoders) == 0 {
		return p, nil
	}
	return &decodingParser{parser: p, steps: append([]spec.CodecStep(nil), cfg.Decoders...), max: cfg.MaxDecodedBytes}, nil
}

// Parse uses a temporary message so parsers see the decoded payload, while
// taps, routing expressions' raw variable, and sinks retain the source bytes.
// Failed decoding never exposes partially decoded fields.
func (p *decodingParser) Parse(msg *message.Message) error {
	decoded, err := codec.Decode(context.Background(), p.steps, msg.Raw, p.max, msg)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	parsed := *msg
	parsed.Raw = decoded
	err = p.parser.Parse(&parsed)
	// Parsers may replace the fields map (the Python parser does) rather than
	// fill it in place, so carry the result back to the original message.
	msg.Fields = parsed.Fields
	return err
}

// DecodeFrame applies cfg's decoder steps to raw exactly as the flow's
// decode step would, returning the payload the parser receives.
func DecodeFrame(cfg spec.DecodeCfg, raw []byte) ([]byte, error) {
	return DecodeBytes(context.Background(), cfg.Decoders, cfg.MaxDecodedBytes, raw, &message.Message{Raw: raw, Fields: map[string]any{}, Meta: map[string]string{}})
}

// DecodeBytes runs decoder steps on data (msg is the message Python decoders
// see). Each step's output is capped at max bytes (0 = 1 MiB).
func DecodeBytes(ctx context.Context, steps []spec.CodecStep, max int, data []byte, msg *message.Message) ([]byte, error) {
	if len(steps) == 0 {
		return data, nil
	}
	decoded, err := codec.Decode(ctx, steps, data, max, msg)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return decoded, nil
}
