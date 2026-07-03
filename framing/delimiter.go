package framing

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

type delimiterFramer struct {
	r        *bufio.Reader
	sentinel []byte
	maxBytes int
}

func newDelimiter(r io.Reader, sentinel []byte, maxBytes int) Framer {
	return &delimiterFramer{
		r:        bufio.NewReaderSize(r, 64*1024),
		sentinel: sentinel,
		maxBytes: maxBytes,
	}
}

// ReadFrame accumulates bytes until the sentinel sequence appears at the end
// of the buffer, then returns the full frame including the sentinel.
// Mirrors the pattern in gateway/protocol/xml_codec.go.
func (f *delimiterFramer) ReadFrame() ([]byte, error) {
	var buf bytes.Buffer
	for {
		b, err := f.r.ReadByte()
		if err != nil {
			if err == io.EOF && buf.Len() > 0 {
				return nil, fmt.Errorf("framing: unexpected EOF before sentinel")
			}
			return nil, err
		}
		buf.WriteByte(b)
		if buf.Len() > f.maxBytes {
			return nil, fmt.Errorf("framing: frame exceeded max size %d", f.maxBytes)
		}
		if buf.Len() >= len(f.sentinel) {
			tail := buf.Bytes()[buf.Len()-len(f.sentinel):]
			if bytes.Equal(tail, f.sentinel) {
				out := make([]byte, buf.Len())
				copy(out, buf.Bytes())
				return out, nil
			}
		}
	}
}
