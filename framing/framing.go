// Package framing splits a raw byte stream into discrete frames.
package framing

import (
	"fmt"
	"io"

	"github.com/cnak-us/gogo/spec"
)

// Framer reads one complete frame per call.
type Framer interface {
	ReadFrame() ([]byte, error)
}

const defaultMaxBytes = 1 << 20 // 1 MiB

// New returns a Framer for the given config, reading from r.
func New(cfg spec.Framing, r io.Reader) (Framer, error) {
	max := cfg.MaxBytes
	if max <= 0 {
		max = defaultMaxBytes
	}
	switch cfg.Mode {
	case "line":
		return newLine(r, max), nil
	case "delimited":
		if cfg.Delimiter == "" {
			return nil, fmt.Errorf("framing: mode 'delimited' requires a delimiter")
		}
		return newDelimiter(r, []byte(cfg.Delimiter), max), nil
	case "length-prefix":
		pl := cfg.PrefixLen
		if pl != 2 && pl != 4 {
			pl = 4
		}
		return newLengthPrefix(r, pl, max), nil
	case "raw", "":
		return newRaw(r, max), nil
	default:
		return nil, fmt.Errorf("framing: unknown mode %q", cfg.Mode)
	}
}
