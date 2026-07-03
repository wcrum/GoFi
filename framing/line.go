package framing

import (
	"bufio"
	"fmt"
	"io"
)

type lineFramer struct {
	s *bufio.Scanner
}

func newLine(r io.Reader, maxBytes int) Framer {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, maxBytes), maxBytes)
	return &lineFramer{s: s}
}

func (f *lineFramer) ReadFrame() ([]byte, error) {
	if !f.s.Scan() {
		if err := f.s.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	b := f.s.Bytes()
	if len(b) == 0 {
		return f.ReadFrame() // skip blank lines
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

func (f *lineFramer) String() string { return fmt.Sprintf("line") }
