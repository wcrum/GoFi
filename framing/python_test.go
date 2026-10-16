package framing

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/wcrum/gofi/spec"
)

// A framer that splits on "|" and flushes the remainder at end of stream.
func pipeFramer(_ context.Context, _ *spec.PythonRef, buffer []byte, final bool) ([][]byte, []byte, error) {
	parts := bytes.Split(buffer, []byte("|"))
	rest := parts[len(parts)-1]
	frames := parts[:len(parts)-1]
	if final && len(rest) > 0 {
		frames, rest = append(frames, rest), nil
	}
	return frames, rest, nil
}

// slowReader returns one byte per Read so frames span many reads.
type slowReader struct{ r io.Reader }

func (s slowReader) Read(p []byte) (int, error) { return s.r.Read(p[:1]) }

func TestPythonFramerSplitsAcrossReadsAndFlushesAtEOF(t *testing.T) {
	SetPythonRunner(pipeFramer)
	t.Cleanup(func() { SetPythonRunner(nil) })
	f, err := New(spec.Framing{Mode: "python", Python: &spec.PythonRef{Library: "l", Function: "split"}}, slowReader{strings.NewReader("ab|cd||tail")})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for {
		frame, err := f.ReadFrame()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(frame))
	}
	if strings.Join(got, ",") != "ab,cd,,tail" {
		t.Fatalf("frames %q", got)
	}
}

func TestPythonFramerBufferIsBounded(t *testing.T) {
	SetPythonRunner(func(_ context.Context, _ *spec.PythonRef, buffer []byte, _ bool) ([][]byte, []byte, error) {
		return nil, buffer, nil
	})
	t.Cleanup(func() { SetPythonRunner(nil) })
	f, _ := New(spec.Framing{Mode: "python", MaxBytes: 16, Python: &spec.PythonRef{Library: "l", Function: "never"}}, strings.NewReader(strings.Repeat("x", 64)))
	if _, err := f.ReadFrame(); err == nil || !strings.Contains(err.Error(), "without producing a frame") {
		t.Fatalf("expected the buffer cap, got %v", err)
	}
}
