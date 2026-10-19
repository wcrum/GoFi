// Package framing splits a raw byte stream into discrete frames.
package framing

import (
	"fmt"
	"io"
	"sync"

	"github.com/wcrum/gofi/config"
	"github.com/wcrum/gofi/spec"
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
	case "python":
		if err := cfg.Python.Validate(); err != nil {
			return nil, fmt.Errorf("framing: mode 'python': %w", err)
		}
		return newPython(r, cfg.Python, max), nil
	default:
		return nil, fmt.Errorf("framing: unknown mode %q", cfg.Mode)
	}
}

// Buffer sizing. Framers start small and grow toward the frame size cap as
// data arrives: a connection or datagram costs what it carries, not the cap.
// A reader that knows its remaining length (bytes.Reader, a datagram) gets a
// buffer of exactly that size; a stream starts at
// config.Limits.FramerInitialBuffer (GOFI_FRAMER_INITIAL_BUFFER).
const minBufferSize = 512

var defaultBufferSize = sync.OnceValue(func() int {
	if n := config.Get().Limits.FramerInitialBuffer; n >= minBufferSize {
		return n
	}
	return 4 << 10
})

func initialBufferSize(r io.Reader, maxBytes int) int {
	size := defaultBufferSize()
	if sized, ok := r.(interface{ Len() int }); ok {
		size = sized.Len() + 1 // +1 lets a raw read observe EOF without growing
		if size < minBufferSize {
			size = minBufferSize
		}
	}
	if size > maxBytes {
		size = maxBytes
	}
	if size < 16 {
		size = 16
	}
	return size
}
