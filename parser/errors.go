package parser

import (
	"errors"

	"github.com/wcrum/gofi/spec"
)

// ErrRejected is an explicit parser decision to discard a message, distinct
// from a decoding/runtime failure that might be allowed by errorPolicy.
var ErrRejected = errors.New("parser: message explicitly rejected")

// ShouldDrop keeps custom Python parser failures closed by default, while
// allowing an explicit continue policy for errors. Explicit rejection always
// stops processing, even when a parser's general error policy is continue.
func ShouldDrop(cfg spec.ParserCfg, err error) bool {
	if errors.Is(err, ErrRejected) {
		return true
	}
	return cfg.ErrorPolicy == "drop" || (cfg.Format == "python" && cfg.ErrorPolicy != "continue")
}
