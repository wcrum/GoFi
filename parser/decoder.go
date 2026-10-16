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

func withDecoders(p Parser, cfg spec.ParserCfg) (Parser, error) {
	if len(cfg.Decoders) == 0 {
		return p, nil
	}
	for _, step := range cfg.Decoders {
		if err := step.Validate("decoder"); err != nil {
			return nil, fmt.Errorf("parser: %w", err)
		}
	}
	return &decodingParser{parser: p, steps: append([]spec.CodecStep(nil), cfg.Decoders...), max: cfg.MaxDecodedBytes}, nil
}

// Parse uses a temporary message so parsers see the decoded payload, while
// taps, routing expressions' raw variable, and sinks retain the source bytes.
// Failed decoding never exposes partially decoded fields.
func (p *decodingParser) Parse(msg *message.Message) error {
	decoded, err := codec.Decode(context.Background(), p.steps, msg.Raw, p.max, msg)
	if err != nil {
		return fmt.Errorf("parser: %w", err)
	}
	parsed := *msg
	parsed.Raw = decoded
	err = p.parser.Parse(&parsed)
	// Parsers may replace the fields map (the Python parser does) rather than
	// fill it in place, so carry the result back to the original message.
	msg.Fields = parsed.Fields
	return err
}

// DecodeFrame applies cfg's decoder steps to raw exactly as the parser
// would, returning the payload a Python parser receives.
func DecodeFrame(cfg spec.ParserCfg, raw []byte) ([]byte, error) {
	decoded, err := codec.Decode(context.Background(), cfg.Decoders, raw, cfg.MaxDecodedBytes, &message.Message{Raw: raw, Fields: map[string]any{}, Meta: map[string]string{}})
	if err != nil {
		return nil, fmt.Errorf("parser: %w", err)
	}
	return decoded, nil
}
