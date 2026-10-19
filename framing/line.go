package framing

import (
	"bufio"
	"fmt"
	"io"
)

type lineFramer struct {
	s *bufio.Scanner
}

// newLine splits r on newlines. The scanner starts with a small buffer and
// grows it on demand up to maxBytes, so an idle connection costs kilobytes,
// not the frame size cap.
func newLine(r io.Reader, maxBytes int) Framer {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, initialBufferSize(r, maxBytes)), maxBytes)
	return &lineFramer{s: s}
}

func (f *lineFramer) ReadFrame() ([]byte, error) {
	for {
		if !f.s.Scan() {
			if err := f.s.Err(); err != nil {
				if err == bufio.ErrTooLong {
					return nil, fmt.Errorf("%w: line exceeded max size", ErrFramer)
				}
				return nil, err
			}
			return nil, io.EOF
		}
		b := f.s.Bytes()
		if len(b) == 0 {
			continue // skip blank lines
		}
		out := make([]byte, len(b))
		copy(out, b)
		return out, nil
	}
}

func (f *lineFramer) String() string { return "line" }
