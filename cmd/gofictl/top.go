package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/internal/testkit/gofiapi"
)

// Per-flow counters read from the controller's /metrics. The label is
// "listener" for historical reasons; its value is the flow name.
const (
	metricFrames   = "gofi_frames_received_total"
	metricBytes    = "gofi_source_bytes_total"
	metricRejected = "gofi_messages_rejected_total"
	metricDispatch = "gofi_sink_dispatch_total"
	metricRSS      = "process_resident_memory_bytes"
	metricRoutines = "go_goroutines"
)

// snapshot is one scrape: cumulative counters per flow plus process gauges.
type snapshot struct {
	at         time.Time
	counters   map[string]*counters // by flow name
	rss        float64
	goroutines float64
	statuses   []*gofiv1.FlowStatus
	err        error
}

type counters struct{ frames, bytes, sinkOK, sinkErr, rejected float64 }

// rates is the per-second change between two snapshots.
type rates struct{ frames, bytes, sinkOK, sinkErr, rejected float64 }

func collect(ctx context.Context, client *gofiapi.Client) snapshot {
	snap := snapshot{at: time.Now(), counters: map[string]*counters{}}
	statuses, err := client.ListStatus(ctx)
	if err != nil {
		snap.err = fmt.Errorf("list flow status: %s", describe(err))
		return snap
	}
	snap.statuses = statuses
	samples, err := client.Metrics(ctx)
	if err != nil {
		snap.err = fmt.Errorf("scrape metrics: %v", err)
		return snap
	}
	get := func(name string) *counters {
		c := snap.counters[name]
		if c == nil {
			c = &counters{}
			snap.counters[name] = c
		}
		return c
	}
	samples.Each(metricFrames, func(labels map[string]string, v float64) { get(labels["listener"]).frames += v })
	samples.Each(metricBytes, func(labels map[string]string, v float64) { get(labels["listener"]).bytes += v })
	samples.Each(metricRejected, func(labels map[string]string, v float64) { get(labels["listener"]).rejected += v })
	samples.Each(metricDispatch, func(labels map[string]string, v float64) {
		if labels["outcome"] == "error" {
			get(labels["listener"]).sinkErr += v
		} else {
			get(labels["listener"]).sinkOK += v
		}
	})
	snap.rss = samples.Value(metricRSS, nil)
	snap.goroutines = samples.Value(metricRoutines, nil)
	return snap
}

// delta computes rates from prev to cur; a counter that went down (runtime
// restarted) reads as zero for this tick.
func delta(prev, cur snapshot, name string) rates {
	a, b := prev.counters[name], cur.counters[name]
	if a == nil || b == nil || prev.at.IsZero() {
		return rates{}
	}
	dt := cur.at.Sub(prev.at).Seconds()
	if dt <= 0 {
		return rates{}
	}
	rate := func(x, y float64) float64 {
		if y < x {
			return 0
		}
		return (y - x) / dt
	}
	return rates{frames: rate(a.frames, b.frames), bytes: rate(a.bytes, b.bytes), sinkOK: rate(a.sinkOK, b.sinkOK), sinkErr: rate(a.sinkErr, b.sinkErr), rejected: rate(a.rejected, b.rejected)}
}

// runTop refreshes a throughput table every interval: a full-screen view on a
// terminal, one line per flow per tick otherwise (or with --no-tui).
func runTop(ctx context.Context, g *globals, args []string) error {
	fs := newFlagSet("top", g)
	interval := fs.Duration("interval", time.Second, "refresh interval")
	noTUI := fs.Bool("no-tui", false, "print one line per flow per tick instead of a full-screen table")
	count := fs.Int("count", 0, "exit after this many ticks (0: run until q or Ctrl-C)")
	rest, err := parseInterspersed(fs, args, 0)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("top takes no arguments (got %q)", rest[0])
	}
	if *interval < 100*time.Millisecond {
		return fmt.Errorf("interval must be at least 100ms")
	}
	client := g.client()
	mode := ""
	if m, err := client.Mode(ctx); err == nil {
		mode = m.Mode
		if m.DriverKind != "" {
			mode += "/" + m.DriverKind
		}
	}
	if !*noTUI && isTerminal(os.Stdout) {
		return runTUI(ctx, client, g.server, mode, *interval)
	}
	var prev snapshot
	ticks := 0
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		cur := collect(ctx, client)
		ticks++
		fmt.Fprint(os.Stdout, renderLines(prev, cur))
		if cur.err == nil {
			prev = cur
		}
		if *count > 0 && ticks >= *count {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// flowRow is one rendered row's numbers.
type flowRow struct {
	name, phase string
	r           rates
	c           counters
}

func rows(prev, cur snapshot) ([]flowRow, flowRow, int) {
	out := make([]flowRow, 0, len(cur.statuses))
	total := flowRow{name: "TOTAL"}
	running := 0
	for _, status := range cur.statuses {
		row := flowRow{name: status.Name, phase: dash(status.Phase), r: delta(prev, cur, status.Name)}
		if c := cur.counters[status.Name]; c != nil {
			row.c = *c
		}
		if status.Phase == "running" {
			running++
		}
		total.r.frames += row.r.frames
		total.r.bytes += row.r.bytes
		total.r.sinkOK += row.r.sinkOK
		total.r.sinkErr += row.r.sinkErr
		total.r.rejected += row.r.rejected
		total.c.frames += row.c.frames
		total.c.bytes += row.c.bytes
		total.c.sinkOK += row.c.sinkOK
		total.c.sinkErr += row.c.sinkErr
		total.c.rejected += row.c.rejected
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, total, running
}

// cells renders one row: name, phase, five rates, then totals.
func cells(row flowRow) []string {
	return []string{row.name, row.phase, rate(row.r.frames), bytesRate(row.r.bytes), rate(row.r.sinkOK), rate(row.r.sinkErr), rate(row.r.rejected), fmt.Sprintf("%.0f", row.c.frames), humanBytes(row.c.bytes), fmt.Sprintf("%.0f", row.c.sinkErr)}
}

// renderLines is the plain output: one line per flow and a total line per
// tick (the first tick has no rates yet).
func renderLines(prev, cur snapshot) string {
	stamp := cur.at.Format("15:04:05")
	if cur.err != nil {
		return fmt.Sprintf("%s controller unavailable: %s\n", stamp, cur.err)
	}
	list, total, running := rows(prev, cur)
	var b strings.Builder
	for _, row := range list {
		fmt.Fprintf(&b, "%s flow=%s phase=%s frames/s=%s bytes/s=%s sink_ok/s=%s sink_err/s=%s rejected/s=%s frames=%.0f bytes=%.0f sink_err=%.0f\n",
			stamp, row.name, row.phase, rate(row.r.frames), bytesRate(row.r.bytes), rate(row.r.sinkOK), rate(row.r.sinkErr), rate(row.r.rejected), row.c.frames, row.c.bytes, row.c.sinkErr)
	}
	fmt.Fprintf(&b, "%s total flows=%d running=%d frames/s=%s bytes/s=%s sink_ok/s=%s sink_err/s=%s rejected/s=%s rss=%s goroutines=%.0f\n",
		stamp, len(list), running, rate(total.r.frames), bytesRate(total.r.bytes), rate(total.r.sinkOK), rate(total.r.sinkErr), rate(total.r.rejected), humanBytes(cur.rss), cur.goroutines)
	return b.String()
}

func rate(v float64) string {
	switch {
	case v == 0:
		return "0"
	case v < 10:
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.0f", v)
}

func bytesRate(v float64) string { return humanBytes(v) + "/s" }

func humanBytes(v float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
