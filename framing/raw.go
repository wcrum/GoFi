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
//
// The buffer grows with the data read (from the reader's known length when
// it has one), so a small datagram or connection never allocates the cap.
func (f *rawFramer) ReadFrame() ([]byte, error) {
	if f.done {
		return nil, io.EOF
	}
	f.done = true
	limit := f.maxBytes + 1
	buf := make([]byte, 0, initialBufferSize(f.r, limit))
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		room := buf[len(buf):cap(buf)]
		if rest := limit - len(buf); len(room) > rest {
			room = room[:rest]
		}
		n, err := f.r.Read(room)
		buf = buf[:len(buf)+n]
		if len(buf) > f.maxBytes {
			return nil, fmt.Errorf("%w: raw frame exceeded max size %d", ErrFramer, f.maxBytes)
		}
		if err == io.EOF {
			if len(buf) == 0 {
				return nil, io.EOF
			}
			return buf, nil
		}
		if err != nil {
			// A reset or deadline mid-frame drops the partial frame, as
			// io.ReadFull did: only a clean close completes a raw frame.
			return nil, err
		}
	}
}
