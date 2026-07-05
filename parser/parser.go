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
	switch cfg.Format {
	case "xml":
		return &xmlParser{}, nil
	case "json":
		return &jsonParser{}, nil
	case "none", "":
		return &passthroughParser{}, nil
	default:
		return nil, fmt.Errorf("parser: unknown format %q", cfg.Format)
	}
}
