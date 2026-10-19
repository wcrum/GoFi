package filter

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/wcrum/gofi/message"
)

// Evaluating a program must not copy the raw frame: the activation resolves
// raw lazily and Message.RawString caches the one conversion per message.
func BenchmarkEvalCEL(b *testing.B) {
	prog, err := CompilePredicate(`fields["type"] == "a-f-G" && double(fields["lat"]) > 30.0`)
	if err != nil {
		b.Fatal(err)
	}
	for _, size := range []int{128, 16 << 10, 256 << 10} {
		b.Run(fmt.Sprintf("raw=%d", size), func(b *testing.B) {
			msg := message.New(bytes.Repeat([]byte("x"), size), "bench", "r")
			msg.Fields["type"] = "a-f-G"
			msg.Fields["lat"] = 38.9
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := EvalCELBool(prog, msg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Two programs reading raw share one string conversion per message.
func TestRawStringSharedAcrossPrograms(t *testing.T) {
	first, _ := CompilePredicate(`raw.startsWith("x")`)
	second, _ := CompilePredicate(`raw.size() > 10`)
	msg := message.New(bytes.Repeat([]byte("x"), 64<<10), "t", "r")
	// Warm: the first evaluation converts raw once.
	if ok, err := EvalCELBool(first, msg); err != nil || !ok {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(50, func() {
		EvalCELBool(first, msg)
		EvalCELBool(second, msg)
	})
	// Neither evaluation may allocate the 64 KiB string again; the CEL
	// machinery itself allocates a few small objects per eval.
	if allocs > 60 {
		t.Fatalf("%.0f allocs for two evaluations; raw is being copied per eval", allocs)
	}
	if raceEnabled {
		return // byte accounting differs under the race detector
	}
	var m runtime_MemStats
	before := readTotalAlloc(&m)
	for i := 0; i < 100; i++ {
		EvalCELBool(first, msg)
		EvalCELBool(second, msg)
	}
	perPair := float64(readTotalAlloc(&m)-before) / 100
	if perPair > 16<<10 {
		t.Fatalf("%.0f bytes per pair of evaluations on a 64 KiB frame; raw is being copied", perPair)
	}
}
