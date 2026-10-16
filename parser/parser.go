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

// New returns a Parser for the given config.
func New(cfg spec.ParserCfg) (Parser, error) {
	if cfg.MaxDecodedBytes < 0 {
		return nil, fmt.Errorf("parser: maxDecodedBytes must not be negative")
	}
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
	case "none", "":
		p = &passthroughParser{}
	default:
		return nil, fmt.Errorf("parser: unknown format %q", cfg.Format)
	}
	return withDecoders(p, cfg)
}
