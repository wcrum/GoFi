package listener

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/wcrum/gofi/spec"
)

// TestListener_StatefulStageSharedAcrossConnections is a regression test for
// the bug where the stage chain (and its stateful stages, like rate-limit)
// used to be rebuilt fresh inside pipeline.New on every accepted connection.
// That made a rate-limit stage nearly useless: each new TCP connection reset
// the counter. Here we bind a real listener with a rate-limit of 2/sec,
// dial it twice (two separate connections), and confirm the SECOND
// connection's messages are still subject to the SAME shared counter — i.e.
// the limit is listener-wide, not per-connection.
func TestListener_StatefulStageSharedAcrossConnections(t *testing.T) {
	def := spec.ListenerDefinition{
		Name:    "ratelimit-regression",
		Source:  spec.Source{Port: 0, Network: "tcp"},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "r1", Kind: "rate-limit", MaxPerSecond: 2},
		},
		Sinks: []spec.SinkCfg{{Type: "stdout"}},
	}

	l, err := New(def)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	addr := l.ln.Addr().String()

	sub := l.Tap.Subscribe()
	defer l.Tap.Unsubscribe(sub)

	// Connection 1: send 2 messages (fills the rate-limit window).
	conn1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	conn1.Write([]byte("{\"a\":1}\n{\"a\":2}\n"))
	conn1.Close()

	// Connection 2 (a fresh TCP connection, which used to reset any
	// per-connection state): send 1 more message immediately. If the chain
	// is correctly shared listener-wide, this message should still be
	// counted against the same window and get dropped.
	conn2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	conn2.Write([]byte("{\"a\":3}\n"))
	conn2.Close()

	var dropped int
	deadline := time.After(3 * time.Second)
	received := 0
	for received < 3 {
		select {
		case ev := <-sub:
			received++
			if ev.Dropped {
				dropped++
			}
		case <-deadline:
			t.Fatalf("timed out waiting for tap events; got %d of 3, dropped so far %d", received, dropped)
		}
	}

	if dropped != 1 {
		t.Fatalf("expected exactly 1 message dropped by the shared rate-limit across connections, got %d", dropped)
	}
}
