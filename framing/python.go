package framing

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/wcrum/gofi/spec"
)

// PythonRunner calls a @gofi.framer function with the bytes buffered so far.
// It returns complete frames and the unconsumed remainder. `final` is set
// once the stream has ended so the framer can flush a trailing frame.
type PythonRunner func(ctx context.Context, ref *spec.PythonRef, buffer []byte, final bool) (frames [][]byte, rest []byte, err error)

var (
	runnerMu sync.RWMutex
	runner   PythonRunner
)

// ErrFramer marks failures of the framing logic itself (a Python framer
// error, an oversized frame) as opposed to the connection failing.
var ErrFramer = errors.New("framing")

// SetPythonRunner installs the runner used by "python" framing.
func SetPythonRunner(r PythonRunner) { runnerMu.Lock(); runner = r; runnerMu.Unlock() }

// DecodePythonFrames reads a framer's result: {"frames": [base64, ...],
// "rest": base64}. Bytes travel base64-encoded inside the result fields.
func DecodePythonFrames(fields map[string]any) ([][]byte, []byte, error) {
	var frames [][]byte
	if list, ok := fields["frames"].([]any); ok {
		for i, item := range list {
			text, _ := item.(string)
			frame, err := base64.StdEncoding.DecodeString(text)
			if err != nil {
				return nil, nil, fmt.Errorf("framer frame %d: %w", i, err)
			}
			frames = append(frames, frame)
		}
	}
	var rest []byte
	if text, ok := fields["rest"].(string); ok && text != "" {
		var err error
		if rest, err = base64.StdEncoding.DecodeString(text); err != nil {
			return nil, nil, fmt.Errorf("framer rest: %w", err)
		}
	}
	return frames, rest, nil
}

const pythonReadChunk = 32 << 10

// pythonFramer buffers reads and lets a Python function decide where frames
// end. The buffer is capped at maxBytes so a framer that never splits cannot
// grow memory without bound.
type pythonFramer struct {
	r      io.Reader
	ref    *spec.PythonRef
	max    int
	buf    []byte
	queue  [][]byte
	ended  bool
	closed bool
}

func newPython(r io.Reader, ref *spec.PythonRef, max int) *pythonFramer {
	return &pythonFramer{r: r, ref: ref, max: max}
}

func (f *pythonFramer) ReadFrame() ([]byte, error) {
	for {
		if len(f.queue) > 0 {
			frame := f.queue[0]
			f.queue = f.queue[1:]
			return frame, nil
		}
		if f.closed {
			return nil, io.EOF
		}
		if f.ended {
			f.closed = true
			if err := f.call(true); err != nil {
				return nil, err
			}
			continue
		}
		chunk := make([]byte, pythonReadChunk)
		n, err := f.r.Read(chunk)
		if n > 0 {
			f.buf = append(f.buf, chunk[:n]...)
			if len(f.buf) > f.max {
				return nil, fmt.Errorf("%w: python framer buffered more than %d bytes without producing a frame", ErrFramer, f.max)
			}
			if callErr := f.call(false); callErr != nil {
				return nil, callErr
			}
		}
		if errors.Is(err, io.EOF) {
			f.ended = true
		} else if err != nil {
			return nil, err
		}
	}
}

func (f *pythonFramer) call(final bool) error {
	runnerMu.RLock()
	r := runner
	runnerMu.RUnlock()
	if r == nil {
		return fmt.Errorf("%w: no Python runtime is available for %s.%s", ErrFramer, f.ref.Library, f.ref.Function)
	}
	frames, rest, err := r(context.Background(), f.ref, f.buf, final)
	if err != nil {
		return fmt.Errorf("%w: %s.%s: %w", ErrFramer, f.ref.Library, f.ref.Function, err)
	}
	for _, frame := range frames {
		if len(frame) > f.max {
			return fmt.Errorf("%w: python frame exceeds %d bytes", ErrFramer, f.max)
		}
	}
	f.queue = append(f.queue, frames...)
	f.buf = rest
	return nil
}
