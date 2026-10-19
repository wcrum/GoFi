// Package parser decodes raw frame bytes into Message.Fields.
package parser

import (
	"fmt"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// Parser decodes a raw frame into the message's Fields map.
type Parser interface {
	Parse(msg *message.Message) error
}

// ForFlow returns the parser a flow runs on each frame: its decode step
// (decode.decoders), then its parser.
func ForFlow(def spec.FlowDefinition) (Parser, error) {
	if err := def.Normalize(); err != nil {
		return nil, err
	}
	p, err := New(def.Parser)
	if err != nil {
		return nil, err
	}
	return WithDecode(p, def.DecodeSteps())
}

// New returns a Parser for the given config. It does not decode: see
// ForFlow and WithDecode for the flow's decode step.
func New(cfg spec.ParserCfg) (Parser, error) {
	if cfg.ErrorPolicy != "" && cfg.ErrorPolicy != "continue" && cfg.ErrorPolicy != "drop" {
		return nil, fmt.Errorf("parser: unknown errorPolicy %q", cfg.ErrorPolicy)
	}
	var p Parser
	switch cfg.Format {
	case "python":
		if err := cfg.Python.Validate(); err != nil {
			return nil, err
		}
		p = &pythonParser{ref: cfg.Python}
	case "xml":
		p = &xmlParser{}
	case "json":
		p = &jsonParser{}
	case "none", "text", "":
		// text is the parse stage's name for the same thing: the decoded
		// frame as a string in fields.raw.
		p = &passthroughParser{}
	default:
		return nil, fmt.Errorf("parser: unknown format %q", cfg.Format)
	}
	return p, nil
}
