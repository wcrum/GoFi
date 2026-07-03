package framing

import (
	"fmt"
	"io"
)

type rawFramer struct {
	r        io.Reader
	maxBytes int
	done     bool
}

func newRaw(r io.Reader, maxBytes int) Framer {
	return &rawFramer{r: r, maxBytes: maxBytes}
}

// ReadFrame reads the entire connection into a single frame.
// Subsequent calls return io.EOF.
func (f *rawFramer) ReadFrame() ([]byte, error) {
	if f.done {
		return nil, io.EOF
	}
	buf := make([]byte, f.maxBytes+1)
	n, err := io.ReadFull(f.r, buf)
	if err == io.ErrUnexpectedEOF {
		f.done = true
		out := make([]byte, n)
		copy(out, buf[:n])
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("framing: raw frame exceeded max size %d", f.maxBytes)
}
