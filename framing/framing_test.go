package framing

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/wcrum/gofi/spec"
)

func readAll(t *testing.T, f Framer) [][]byte {
	t.Helper()
	var out [][]byte
	for {
		frame, err := f.ReadFrame()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		out = append(out, frame)
	}
}

func TestLineFramerGrowsBeyondInitialBuffer(t *testing.T) {
	long := strings.Repeat("x", 100<<10)
	f, err := New(spec.Framing{Mode: "line"}, strings.NewReader("a\n\n"+long+"\nb\n"))
	if err != nil {
		t.Fatal(err)
	}
	frames := readAll(t, f)
	if len(frames) != 3 || string(frames[0]) != "a" || len(frames[1]) != len(long) || string(frames[2]) != "b" {
		t.Fatalf("got %d frames", len(frames))
	}
}

func TestLineFramerRejectsOversizedLineAsFramerError(t *testing.T) {
	f, _ := New(spec.Framing{Mode: "line", MaxBytes: 64}, strings.NewReader(strings.Repeat("x", 65)+"\n"))
	_, err := f.ReadFrame()
	if !errors.Is(err, ErrFramer) {
		t.Fatalf("err = %v", err)
	}
}

func TestRawFramerReadsWholeStreamWithoutPreallocatingCap(t *testing.T) {
	data := bytes.Repeat([]byte("r"), 10_000)
	f, _ := New(spec.Framing{Mode: "raw", MaxBytes: 64 << 20}, bytes.NewReader(data))
	frame, err := f.ReadFrame()
	if err != nil || !bytes.Equal(frame, data) {
		t.Fatalf("frame %d bytes, err %v", len(frame), err)
	}
	if cap(frame) > 2*len(data)+minBufferSize {
		t.Fatalf("raw frame capacity %d for %d bytes of data", cap(frame), len(data))
	}
	if _, err := f.ReadFrame(); err != io.EOF {
		t.Fatalf("second read err = %v", err)
	}
	// Streams of unknown length (a connection) grow from the default size.
	f, _ = New(spec.Framing{Mode: "raw", MaxBytes: 64 << 20}, iotest(data))
	frame, err = f.ReadFrame()
	if err != nil || !bytes.Equal(frame, data) {
		t.Fatalf("frame %d bytes, err %v", len(frame), err)
	}
}

// iotest hides the reader's length so sizing falls back to the default.
func iotest(data []byte) io.Reader { return struct{ io.Reader }{bytes.NewReader(data)} }

func TestRawFramerEnforcesMax(t *testing.T) {
	f, _ := New(spec.Framing{Mode: "raw", MaxBytes: 16}, iotest(bytes.Repeat([]byte("r"), 17)))
	if _, err := f.ReadFrame(); !errors.Is(err, ErrFramer) {
		t.Fatalf("err = %v", err)
	}
	f, _ = New(spec.Framing{Mode: "raw", MaxBytes: 16}, iotest(bytes.Repeat([]byte("r"), 16)))
	if frame, err := f.ReadFrame(); err != nil || len(frame) != 16 {
		t.Fatalf("frame %d err %v", len(frame), err)
	}
	f, _ = New(spec.Framing{Mode: "raw"}, strings.NewReader(""))
	if _, err := f.ReadFrame(); err != io.EOF {
		t.Fatalf("empty stream err = %v", err)
	}
}

func TestDelimiterFramerSplitsOnMultiByteSentinel(t *testing.T) {
	// Sentinel bytes that also appear inside frames, and frames larger than
	// the initial buffer.
	big := strings.Repeat("y", 20<<10)
	input := "<a>1</a>\r\n<b>\r</b>\r\n" + big + "\r\n"
	f, err := New(spec.Framing{Mode: "delimited", Delimiter: "\r\n"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	frames := readAll(t, f)
	want := []string{"<a>1</a>\r\n", "<b>\r</b>\r\n", big + "\r\n"}
	if len(frames) != len(want) {
		t.Fatalf("got %d frames, want %d", len(frames), len(want))
	}
	for i := range want {
		if string(frames[i]) != want[i] {
			t.Fatalf("frame %d = %q", i, frames[i][:min(len(frames[i]), 40)])
		}
	}
}

func TestDelimiterFramerErrors(t *testing.T) {
	f, _ := New(spec.Framing{Mode: "delimited", Delimiter: "|", MaxBytes: 8}, strings.NewReader("123456789|"))
	if _, err := f.ReadFrame(); !errors.Is(err, ErrFramer) {
		t.Fatalf("oversized: err = %v", err)
	}
	f, _ = New(spec.Framing{Mode: "delimited", Delimiter: "|"}, strings.NewReader("partial"))
	if _, err := f.ReadFrame(); !errors.Is(err, ErrFramer) {
		t.Fatalf("truncated: err = %v", err)
	}
	f, _ = New(spec.Framing{Mode: "delimited", Delimiter: "|"}, strings.NewReader("a|"))
	readAll(t, f)
}

func TestInitialBufferSizeIsSmall(t *testing.T) {
	if got := initialBufferSize(iotest(nil), 1<<20); got != defaultBufferSize() {
		t.Fatalf("unknown length: %d", got)
	}
	if got := initialBufferSize(bytes.NewReader(make([]byte, 100)), 1<<20); got != minBufferSize {
		t.Fatalf("small datagram: %d", got)
	}
	if got := initialBufferSize(bytes.NewReader(make([]byte, 100_000)), 1<<20); got != 100_001 {
		t.Fatalf("large datagram: %d", got)
	}
	if got := initialBufferSize(iotest(nil), 64); got != 64 {
		t.Fatalf("tiny cap: %d", got)
	}
}

func BenchmarkFramerNew(b *testing.B) {
	line := []byte(`{"type":"a-f-G","lat":38.9}`)
	for _, m := range []string{"line", "raw", "delimited", "length-prefix"} {
		b.Run(m, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				f, _ := New(spec.Framing{Mode: m, Delimiter: "\n"}, bytes.NewReader(line))
				_, _ = f.ReadFrame()
			}
		})
	}
}
