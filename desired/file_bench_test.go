package desired

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/wcrum/gofi/spec"
)

// BenchmarkFileSeed measures seeding N flows into an empty flow file, one
// Save per flow, as the integration harness and `gofi apply` do. It reports
// the mean cost of one durable save (ms/save) at that size.
//
//	GOWORK=off go test ./desired -run xxx -bench FileSeed -benchtime=1x
func BenchmarkFileSeed(b *testing.B) {
	for _, n := range []int{50, 500} {
		b.Run(fmt.Sprintf("%d-flows", n), func(b *testing.B) {
			ctx := context.Background()
			var total time.Duration
			for i := 0; i < b.N; i++ {
				store := &File{Path: filepath.Join(b.TempDir(), "listeners.yaml")}
				start := time.Now()
				for j := 0; j < n; j++ {
					def := spec.FlowDefinition{
						Name:    fmt.Sprintf("flow-%04d", j),
						Source:  spec.FlowSource{Listen: &spec.ListenSource{Port: 20000 + j, Network: "tcp"}},
						Framing: spec.Framing{Mode: "line"},
						Parser:  spec.ParserCfg{Format: "json"},
						Sinks:   []spec.SinkCfg{{Type: "stdout"}},
					}
					if err := store.Save(ctx, def); err != nil {
						b.Fatal(err)
					}
				}
				total += time.Since(start)
			}
			b.ReportMetric(float64(total.Milliseconds())/float64(b.N*n), "ms/save")
			b.ReportMetric(total.Seconds()/float64(b.N), "s/seed")
		})
	}
}
