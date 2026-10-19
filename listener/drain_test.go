package listener

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// slowSink takes delay per Send, records what it got, and tracks flush/close
// ordering so tests can check sinks flush after the last send.
type slowSink struct {
	delay   time.Duration
	block   chan struct{} // when non-nil, Send waits on it
	mu      sync.Mutex
	got     []string
	flushed atomic.Bool
	closed  atomic.Bool
	late    atomic.Bool // a Send after Close
}

func (s *slowSink) Send(msg *message.Message) error {
	if s.closed.Load() {
		s.late.Store(true)
	}
	if s.block != nil {
		<-s.block
	}
	time.Sleep(s.delay)
	s.mu.Lock()
	s.got = append(s.got, string(msg.Raw))
	s.mu.Unlock()
	return nil
}
func (s *slowSink) Flush(context.Context) error { s.flushed.Store(true); return nil }
func (s *slowSink) Close() error                { s.closed.Store(true); return nil }
func (s *slowSink) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

var _ sink.Flusher = (*slowSink)(nil)

func drainListener(t *testing.T, network string, out *slowSink) *Listener {
	t.Helper()
	l, err := New(spec.FlowDefinition{Name: "drain-" + network, Source: spec.FlowSource{Listen: &spec.ListenSource{Network: network}}, Framing: spec.Framing{Mode: "line"}, Parser: spec.ParserCfg{Format: "none"}, Sinks: []spec.SinkCfg{{Type: "stdout"}}})
	if err != nil {
		t.Fatal(err)
	}
	l.sinks = []sink.Sink{out}
	// The shared pipeline captured the sinks New built; rebuild it on ours.
	l.pipe, err = pipeline.NewShared(l.def, l.sinks, l.deadLetterSink, l.chain, l.Tap, nil)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Frames already received finish the pipeline and reach the sink before
// Shutdown returns; the sink is flushed and closed afterwards; the port no
// longer accepts connections.
func TestShutdownDrainsInFlightFramesThenFlushesSinks(t *testing.T) {
	out := &slowSink{delay: 50 * time.Millisecond}
	l := drainListener(t, "tcp", out)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan struct{})
	go func() { defer close(runDone); l.Run(ctx) }()
	addr := l.ln.Addr().String()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The connection stays open (a long-lived sender), so only the drain ends it.
	if _, err := conn.Write([]byte("a\nb\nc\nd\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(out.received()) >= 1 })

	drainCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := l.Shutdown(drainCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := out.received(); len(got) != 4 {
		t.Fatalf("in-flight frames were not drained: got %v", got)
	}
	if !out.flushed.Load() || !out.closed.Load() || out.late.Load() {
		t.Fatalf("flushed=%v closed=%v sendAfterClose=%v", out.flushed.Load(), out.closed.Load(), out.late.Load())
	}
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the drain")
	}
	if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("drained listener still accepts connections")
	}
}

// A sink that never finishes cannot hold a stop forever: past the deadline
// the listener stops hard and Shutdown reports the deadline.
func TestShutdownStopsHardAfterDeadline(t *testing.T) {
	out := &slowSink{block: make(chan struct{})}
	defer close(out.block)
	l := drainListener(t, "tcp", out)
	go l.Run(context.Background())
	conn, err := net.Dial("tcp", l.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("stuck\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the frame is inside Send

	ctx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	started := time.Now()
	err = l.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond+hardStopGrace+time.Second {
		t.Fatalf("Shutdown took %v", elapsed)
	}
	if !out.closed.Load() {
		t.Fatal("sinks not closed after a hard stop")
	}
}

// Datagrams are processed in order; the one in progress when the drain
// starts still reaches the sink.
func TestShutdownFinishesDatagramInProgress(t *testing.T) {
	out := &slowSink{delay: 150 * time.Millisecond}
	l := drainListener(t, "udp", out)
	go l.Run(context.Background())
	conn, err := net.Dial("udp", l.packet.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("datagram\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // inside Send
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := l.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if got := out.received(); len(got) != 1 || got[0] != "datagram" {
		t.Fatalf("got %v", got)
	}
}

// Shutdown before Run (or without it) still releases the port and sinks.
func TestShutdownWithoutRun(t *testing.T) {
	out := &slowSink{}
	l := drainListener(t, "tcp", out)
	if err := l.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !out.closed.Load() {
		t.Fatal("sinks not closed")
	}
	l.Close() // idempotent
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
