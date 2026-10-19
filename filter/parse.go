package filter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"

	"github.com/wcrum/gofi/codec"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/parser"
	"github.com/wcrum/gofi/spec"
)

// ParseFormats are the formats a parse stage accepts.
var ParseFormats = []string{"json", "xml", "text", "python"}

// MaxParseDecoders bounds one parse stage's decoder chain (the CRD schema
// enforces the same limit); with MaxStages it bounds the work one frame can
// cause.
const MaxParseDecoders = 16

// parseStage parses a value already on the message (the raw frame, or what a
// CEL expression yields) and adds the result to fields. Its `from` expression
// is compiled once when the stage is built.
//
// Semantics: the message's raw bytes and meta never change. The parser sees a
// scratch message whose raw is the (decoded) input, whose fields start empty,
// and whose meta is a copy of the message's; its fields are the parse result.
type parseStage struct {
	cfg    spec.ParseStage
	from   cel.Program // nil = the raw frame
	parser parser.Parser
	// Python parse functions report output for simulation traces.
	mu         sync.Mutex
	stdout     string
	durationUs int64
}

func newParse(cfg spec.StageSpec) (Stage, error) {
	if cfg.Parse == nil {
		return nil, fmt.Errorf("parse: the parse block is required")
	}
	p := *cfg.Parse
	if err := ValidateParse(p); err != nil {
		return nil, err
	}
	st := &parseStage{cfg: p}
	if !rawInput(p.From) {
		prog, err := CompileCEL(p.From)
		if err != nil {
			return nil, fmt.Errorf("parse: compile from %q: %w", p.From, err)
		}
		st.from = prog
	}
	if p.Format != "text" {
		pr, err := parser.New(spec.ParserCfg{Format: p.Format, Python: p.Python})
		if err != nil {
			return nil, fmt.Errorf("parse: %w", err)
		}
		st.parser = pr
	}
	return st, nil
}

// ValidateParse checks a parse stage's configuration without compiling it.
func ValidateParse(p spec.ParseStage) error {
	switch p.Format {
	case "json", "xml", "text":
		if p.Python != nil {
			return fmt.Errorf("parse: python is only valid with format python")
		}
	case "python":
		if err := p.Python.Validate(); err != nil {
			return fmt.Errorf("parse: %w", err)
		}
	case "":
		return fmt.Errorf("parse: format is required (%s)", strings.Join(ParseFormats, ", "))
	default:
		return fmt.Errorf("parse: unknown format %q (%s)", p.Format, strings.Join(ParseFormats, ", "))
	}
	switch p.Mode {
	case "", "merge":
		if p.Format == "text" && p.Into == "" {
			return fmt.Errorf("parse: format text requires into (the field that receives the text)")
		}
	case "replace":
		if p.Into != "" {
			return fmt.Errorf("parse: mode replace replaces all fields; into must be empty")
		}
		if p.Format == "text" {
			return fmt.Errorf("parse: format text yields a string; use mode merge with into")
		}
	default:
		return fmt.Errorf("parse: mode must be merge or replace, got %q", p.Mode)
	}
	switch p.OnError {
	case "", "continue", "drop":
	default:
		return fmt.Errorf("parse: onError must be continue or drop, got %q", p.OnError)
	}
	if len(p.Decoders) > MaxParseDecoders {
		return fmt.Errorf("parse: at most %d decoders per stage", MaxParseDecoders)
	}
	for _, step := range p.Decoders {
		if err := step.Validate("decoder"); err != nil {
			return fmt.Errorf("parse: %w", err)
		}
	}
	return nil
}

func rawInput(from string) bool {
	from = strings.TrimSpace(from)
	return from == "" || from == "raw"
}

func (p *parseStage) Observation() (string, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stdout, p.durationUs
}

func (p *parseStage) Process(msg *message.Message) (bool, error) {
	input, err := p.input(msg, len(p.cfg.Decoders))
	if err == nil {
		err = p.apply(msg, input)
	}
	if err == nil {
		return true, nil
	}
	// An explicit rejection by a Python parse function always drops, as it
	// does for the flow's parser. So does exceeding the per-message decoded
	// bytes budget: continuing would let a frame expand again in the next
	// stage.
	if errors.Is(err, parser.ErrRejected) || codec.IsBudgetExceeded(err) || p.cfg.OnError == "drop" {
		return false, err
	}
	return true, err // continue: the message passes unchanged
}

// input evaluates `from` and runs the first n decoders.
func (p *parseStage) input(msg *message.Message, n int) ([]byte, error) {
	data := msg.Raw
	if p.from != nil {
		val, err := EvalCEL(p.from, msg)
		if err != nil {
			return nil, fmt.Errorf("parse: from: %w", err)
		}
		switch v := val.(type) {
		case string:
			data = []byte(v)
		case []byte:
			data = v
		default:
			return nil, fmt.Errorf("parse: from must yield a string or bytes, got %T", val)
		}
	}
	// The flow's decode step bounds each decoder's output; bound the parser's
	// input the same way when a CEL expression builds it directly.
	if len(data) > codec.DefaultMaxBytes {
		return nil, fmt.Errorf("parse: input of %d bytes exceeds %d", len(data), codec.DefaultMaxBytes)
	}
	return parser.DecodeBytes(context.Background(), p.cfg.Decoders[:n], 0, data, msg)
}

func (p *parseStage) apply(msg *message.Message, input []byte) error {
	if p.cfg.Format == "text" {
		msg.Fields[p.cfg.Into] = string(input)
		return nil
	}
	scratch := &message.Message{ID: msg.ID, Raw: input, Fields: map[string]any{}, Meta: make(map[string]string, len(msg.Meta))}
	for k, v := range msg.Meta {
		scratch.Meta[k] = v
	}
	err := p.parser.Parse(scratch)
	if observed, ok := p.parser.(interface{ Observation() (string, int64) }); ok {
		p.mu.Lock()
		p.stdout, p.durationUs = observed.Observation()
		p.mu.Unlock()
	}
	if err != nil {
		return err
	}
	result := scratch.Fields
	switch {
	case p.cfg.Mode == "replace":
		msg.Fields = result
	case p.cfg.Into == "":
		for k, v := range result {
			msg.Fields[k] = v
		}
	default:
		if existing, ok := msg.Fields[p.cfg.Into].(map[string]any); ok {
			merged := make(map[string]any, len(existing)+len(result))
			for k, v := range existing {
				merged[k] = v
			}
			for k, v := range result {
				merged[k] = v
			}
			result = merged
		}
		msg.Fields[p.cfg.Into] = result
	}
	return nil
}

// ParseInput returns the bytes a parse stage's step receives for msg:
// with decoder < 0, the parser's input (after every decoder); otherwise the
// input of decoder number `decoder`. Debugging uses it to capture the exact
// input of a parse stage's Python function.
func ParseInput(cfg spec.StageSpec, msg *message.Message, decoder int) ([]byte, error) {
	built, err := newParse(cfg)
	if err != nil {
		return nil, err
	}
	p := built.(*parseStage)
	n := len(p.cfg.Decoders)
	if decoder >= 0 && decoder < n {
		n = decoder
	}
	return p.input(msg, n)
}
