package pipeline

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// fakeSink records every message it receives, in order.
type fakeSink struct {
	mu   sync.Mutex
	msgs []*message.Message
}

func (f *fakeSink) Send(msg *message.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, msg)
	return nil
}

func (f *fakeSink) Close() error { return nil }

func (f *fakeSink) received() []*message.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*message.Message, len(f.msgs))
	copy(out, f.msgs)
	return out
}

func TestPipeline_ChainAttributionAndSink(t *testing.T) {
	def := spec.FlowDefinition{
		Name:    "test-listener",
		Source:  spec.FlowSource{Listen: &spec.ListenSource{Port: 0, Network: "tcp"}},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "f1", Kind: "filter", Type: "cel", Expression: `fields["type"] == "a"`},
			{ID: "f2", Kind: "filter", Type: "cel", Expression: `double(fields["val"]) > 10.0`},
		},
		Sinks: []spec.SinkCfg{{Type: "stdout"}},
	}

	// Three lines:
	//   1. type=a, val=20 -> passes both filters, reaches sink
	//   2. type=b, val=20 -> dropped by f1
	//   3. type=a, val=5  -> passes f1, dropped by f2
	input := strings.NewReader(
		"{\"type\":\"a\",\"val\":20}\n" +
			"{\"type\":\"b\",\"val\":20}\n" +
			"{\"type\":\"a\",\"val\":5}\n",
	)

	fs := &fakeSink{}
	tap := NewTap()

	chain, err := filter.NewChain(def.Stages)
	if err != nil {
		t.Fatalf("NewChain error = %v", err)
	}

	p, err := New(def, input, []sink.Sink{fs}, nil, chain, tap)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	sub := tap.Subscribe()
	defer tap.Unsubscribe(sub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		p.Run(ctx, "127.0.0.1:9999")
		close(done)
	}()

	var events []TapEvent
	for len(events) < 3 {
		select {
		case ev := <-sub:
			events = append(events, ev)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for tap events, got %d so far", len(events))
		}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline.Run did not finish after reader exhausted")
	}

	if events[0].Dropped {
		t.Fatalf("event 0: expected pass, got dropped by %q", events[0].DroppedBy)
	}
	if !events[1].Dropped || events[1].DroppedBy != "f1" {
		t.Fatalf("event 1: expected dropped by f1, got dropped=%v droppedBy=%q", events[1].Dropped, events[1].DroppedBy)
	}
	if !events[2].Dropped || events[2].DroppedBy != "f2" {
		t.Fatalf("event 2: expected dropped by f2, got dropped=%v droppedBy=%q", events[2].Dropped, events[2].DroppedBy)
	}

	received := fs.received()
	if len(received) != 1 {
		t.Fatalf("expected exactly 1 message to reach the sink, got %d", len(received))
	}
	if received[0].Fields["type"] != "a" {
		t.Fatalf("sink message has unexpected fields: %#v", received[0].Fields)
	}
}

func TestPipeline_EmptyChainAlwaysPasses(t *testing.T) {
	def := spec.FlowDefinition{
		Name:    "no-filters",
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
	}
	input := strings.NewReader("{\"a\":1}\n")
	fs := &fakeSink{}
	tap := NewTap()

	chain, err := filter.NewChain(def.Stages)
	if err != nil {
		t.Fatalf("NewChain error = %v", err)
	}

	p, err := New(def, input, []sink.Sink{fs}, nil, chain, tap)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	sub := tap.Subscribe()
	defer tap.Unsubscribe(sub)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.Run(ctx, "127.0.0.1:1111")

	select {
	case ev := <-sub:
		if ev.Dropped {
			t.Fatalf("expected message to pass with no filters configured, got dropped=%v", ev.Dropped)
		}
	default:
		t.Fatal("expected a tap event, got none")
	}

	if len(fs.received()) != 1 {
		t.Fatalf("expected 1 message at sink, got %d", len(fs.received()))
	}
}

func TestPipeline_MultiSinkFanOutWithWhen(t *testing.T) {
	def := spec.FlowDefinition{
		Name:    "multi-sink",
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Sinks: []spec.SinkCfg{
			{Type: "stdout"}, // unconditional
			{Type: "stdout", When: `fields["type"] == "a"`}, // conditional
		},
	}

	input := strings.NewReader(
		"{\"type\":\"a\"}\n" +
			"{\"type\":\"b\"}\n",
	)

	unconditional := &fakeSink{}
	conditional := &fakeSink{}
	tap := NewTap()

	chain, err := filter.NewChain(def.Stages)
	if err != nil {
		t.Fatalf("NewChain error = %v", err)
	}

	p, err := New(def, input, []sink.Sink{unconditional, conditional}, nil, chain, tap)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.Run(ctx, "127.0.0.1:2222")

	if got := len(unconditional.received()); got != 2 {
		t.Fatalf("unconditional sink received %d messages, want 2", got)
	}
	condReceived := conditional.received()
	if got := len(condReceived); got != 1 {
		t.Fatalf("conditional sink received %d messages, want 1", got)
	}
	if condReceived[0].Fields["type"] != "a" {
		t.Fatalf("conditional sink received wrong message: %#v", condReceived[0].Fields)
	}
}

func TestPipeline_DeadLetterSink(t *testing.T) {
	def := spec.FlowDefinition{
		Name:    "dead-letter",
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "s1", Kind: "schema", RequiredFields: []string{"type"}},
		},
		Sinks: []spec.SinkCfg{{Type: "stdout"}},
	}

	input := strings.NewReader(
		"{\"type\":\"a\"}\n" + // passes schema
			"{\"other\":1}\n" + // missing "type" -> dropped
			"{\"type\":\"b\"}\n", // passes schema
	)

	primary := &fakeSink{}
	deadLetter := &fakeSink{}
	tap := NewTap()

	chain, err := filter.NewChain(def.Stages)
	if err != nil {
		t.Fatalf("NewChain error = %v", err)
	}

	p, err := New(def, input, []sink.Sink{primary}, deadLetter, chain, tap)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.Run(ctx, "127.0.0.1:3333")

	primaryMsgs := primary.received()
	if len(primaryMsgs) != 2 {
		t.Fatalf("primary sink received %d messages, want 2", len(primaryMsgs))
	}

	dlMsgs := deadLetter.received()
	if len(dlMsgs) != 1 {
		t.Fatalf("dead-letter sink received %d messages, want 1", len(dlMsgs))
	}
	if dlMsgs[0].Meta["droppedBy"] != "s1" {
		t.Fatalf("dead-letter message droppedBy = %q, want %q", dlMsgs[0].Meta["droppedBy"], "s1")
	}
}
