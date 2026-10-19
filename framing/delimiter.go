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
	buf      []byte // reused across frames; each frame is copied out
}

func newDelimiter(r io.Reader, sentinel []byte, maxBytes int) Framer {
	return &delimiterFramer{
		r:        bufio.NewReaderSize(r, initialBufferSize(r, maxBytes)),
		sentinel: sentinel,
		maxBytes: maxBytes,
	}
}

// ReadFrame accumulates bytes until the sentinel sequence appears at the end
// of the buffer, then returns the full frame including the sentinel.
//
// It reads up to each occurrence of the sentinel's last byte in one call
// rather than byte by byte, and keeps one growing buffer per connection.
func (f *delimiterFramer) ReadFrame() ([]byte, error) {
	f.buf = f.buf[:0]
	last := f.sentinel[len(f.sentinel)-1]
	for {
		chunk, err := f.r.ReadSlice(last)
		if len(f.buf)+len(chunk) > f.maxBytes {
			return nil, fmt.Errorf("%w: frame exceeded max size %d", ErrFramer, f.maxBytes)
		}
		f.buf = append(f.buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(f.buf) > 0 {
				return nil, fmt.Errorf("%w: unexpected EOF before sentinel", ErrFramer)
			}
			return nil, err
		}
		if bytes.HasSuffix(f.buf, f.sentinel) {
			out := make([]byte, len(f.buf))
			copy(out, f.buf)
			return out, nil
		}
	}
}
