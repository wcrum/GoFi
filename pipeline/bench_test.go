package pipeline

import (
	"bytes"
	"context"
	"runtime"
	"testing"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// Hot-path benchmarks for the three ways a frame enters a pipeline: a TCP
// stream (one framer, many frames), a UDP datagram (one stream per
// datagram), and a pulled message (same shape as a datagram). Tests below
// gate allocations per message so a regression fails `go test`.

type countSink struct{ n int }

func (s *countSink) Send(*message.Message) error { s.n++; return nil }
func (s *countSink) Close() error                { return nil }

var benchLine = []byte(`{"type":"a-f-G","lat":38.9,"lon":-77.0,"id":"sensor-42","nested":{"a":1,"b":"x","c":[1,2,3]},"ts":"2026-10-17T00:00:00Z"}`)

func benchDef(mode string, when bool) spec.FlowDefinition {
	d := spec.FlowDefinition{
		Name:    "bench",
		Framing: spec.Framing{Mode: mode},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages:  []spec.StageSpec{{ID: "f", Kind: "filter", Type: "cel", Expression: `fields["type"] == "a-f-G" && double(fields["lat"]) > 30.0`}},
		Sinks:   []spec.SinkCfg{{Type: "stdout"}},
	}
	if mode == "delimited" {
		d.Framing.Delimiter = "\r\n"
	}
	if when {
		d.Sinks[0].When = `fields["type"] == "a-f-G"`
	}
	return d
}

// datagramRunner is one datagram or pulled message through the pipeline, the
// way the listener does it.
func datagramRunner(b testing.TB, d spec.FlowDefinition, sk sink.Sink) func(data []byte) {
	chain, err := filter.NewChain(d.Stages)
	if err != nil {
		b.Fatal(err)
	}
	p, err := NewShared(d, []sink.Sink{sk}, nil, chain, NewTap(), nil)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	return func(data []byte) { p.RunReader(ctx, bytes.NewReader(data), "1.2.3.4:5") }
}

func BenchmarkUDPDatagram(b *testing.B) {
	for _, tc := range []struct {
		name string
		mode string
		when bool
	}{{"raw", "raw", false}, {"line", "line", false}, {"line-sink-when", "line", true}} {
		b.Run(tc.name, func(b *testing.B) {
			run := datagramRunner(b, benchDef(tc.mode, tc.when), &countSink{})
			b.ReportAllocs()
			b.SetBytes(int64(len(benchLine)))
			for i := 0; i < b.N; i++ {
				run(benchLine)
			}
		})
	}
}

func BenchmarkPulledMessage(b *testing.B) {
	ctx := message.WithSourceMeta(context.Background(), map[string]string{"kafka.topic": "t", "kafka.partition": "0"})
	d := benchDef("raw", false)
	chain, _ := filter.NewChain(d.Stages)
	p, err := NewShared(d, []sink.Sink{&countSink{}}, nil, chain, NewTap(), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(benchLine)))
	for i := 0; i < b.N; i++ {
		p.RunReader(ctx, bytes.NewReader(benchLine), "kafka:t")
	}
}

func BenchmarkTCPFrame(b *testing.B) {
	for _, mode := range []string{"line", "delimited"} {
		b.Run(mode, func(b *testing.B) {
			d := benchDef(mode, false)
			sep := []byte("\n")
			if mode == "delimited" {
				sep = []byte("\r\n")
			}
			chain, _ := filter.NewChain(d.Stages)
			buf := bytes.Repeat(append(append([]byte{}, benchLine...), sep...), b.N)
			p, err := New(d, bytes.NewReader(buf), []sink.Sink{&countSink{}}, nil, chain, NewTap())
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(benchLine) + len(sep)))
			b.ResetTimer()
			p.Run(context.Background(), "1.2.3.4:5")
		})
	}
}

// Allocation gates. The bounds are generous (about 2x the measured value)
// so they catch a per-message regression such as a framer buffer or a CEL
// compile per datagram, not noise.
func TestAllocsPerMessage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  string
		when  bool
		bound float64
	}{
		{"udp-raw", "raw", false, 90},
		{"udp-line", "line", false, 100},
		{"udp-line-sink-when", "line", true, 130},
	} {
		run := datagramRunner(t, benchDef(tc.mode, tc.when), &countSink{})
		run(benchLine) // warm the shared environment
		got := testing.AllocsPerRun(200, func() { run(benchLine) })
		if got > tc.bound {
			t.Errorf("%s: %.0f allocs per datagram, bound %.0f", tc.name, got, tc.bound)
		}
	}
}

// A datagram pipeline must not allocate anything proportional to the frame
// size limit: the 1 MiB framing cap used to be preallocated per datagram.
func TestDatagramDoesNotAllocateFramingCap(t *testing.T) {
	d := benchDef("line", false)
	d.Framing.MaxBytes = 8 << 20
	run := datagramRunner(t, d, &countSink{})
	run(benchLine)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < 100; i++ {
		run(benchLine)
	}
	runtime.ReadMemStats(&after)
	perRun := float64(after.TotalAlloc-before.TotalAlloc) / 100
	if perRun > 64<<10 {
		t.Fatalf("%.0f bytes allocated per datagram; the framing cap (%d) leaks into the hot path", perRun, d.Framing.MaxBytes)
	}
}
