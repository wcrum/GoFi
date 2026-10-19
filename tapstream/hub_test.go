package tapstream_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wcrum/gofi/filter"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/spec"
	"github.com/wcrum/gofi/tapstream"
)

func publish(t *testing.T, tap *pipeline.Tap, frames ...string) {
	t.Helper()
	chain, err := filter.NewChain(nil)
	if err != nil {
		t.Fatal(err)
	}
	def := spec.FlowDefinition{Name: "hub-test", Parser: spec.ParserCfg{Format: "json"}}
	for _, frame := range frames {
		p, err := pipeline.New(def, strings.NewReader(frame), nil, nil, chain, tap)
		if err != nil {
			t.Fatal(err)
		}
		p.Run(context.Background(), "test")
	}
}

// Two subscribers see the same converted event object (one conversion per
// event), a subscriber that does not read loses only its own events, and the
// hub leaves the tap with its last subscriber.
func TestHubConvertsOnceAndBoundsEachSubscriber(t *testing.T) {
	tap := pipeline.NewTap()
	hub := tapstream.NewHub(tap, 0)
	fast := hub.Subscribe(64)
	slow := hub.Subscribe(2)
	publish(t, tap, `{"n":1}`, `{"n":2}`, `{"n":3}`, `{"n":4}`, `{"n":5}`)
	var fastEvents []*gofiv1.TrafficEvent
	deadline := time.After(2 * time.Second)
	for len(fastEvents) < 5 { // the pump has delivered everything once fast has all five
		select {
		case event := <-fast.Events():
			fastEvents = append(fastEvents, event)
		case <-deadline:
			t.Fatalf("fast subscriber received %d/5", len(fastEvents))
		}
	}
	if fast.TakeDropped() != 0 {
		t.Fatal("fast subscriber dropped events")
	}
	if dropped := slow.TakeDropped(); dropped != 3 {
		t.Fatalf("slow subscriber dropped %d, want 3 (5 events, queue 2)", dropped)
	}
	for i := 0; i < 2; i++ {
		if same := <-slow.Events(); same != fastEvents[i] {
			t.Fatal("subscribers received different event objects for one message")
		}
	}
	if slow.TakeDropped() != 0 {
		t.Fatal("TakeDropped did not reset")
	}
	if hub.Subscribers() != 2 {
		t.Fatalf("subscribers = %d", hub.Subscribers())
	}
	fast.Close()
	slow.Close()
	slow.Close() // idempotent
	if hub.Subscribers() != 0 {
		t.Fatalf("subscribers after close = %d", hub.Subscribers())
	}
	// Re-attaching restarts the pump.
	again := hub.Subscribe(4)
	defer again.Close()
	publish(t, tap, `{"n":6}`)
	select {
	case <-again.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not resume after its last subscriber left and a new one joined")
	}
}

func TestSharedHubIsOnePerTap(t *testing.T) {
	tap := pipeline.NewTap()
	a := tapstream.Shared(tap, 0)
	if tapstream.Shared(tap, 0) != a {
		t.Fatal("Shared returned a second hub for one tap")
	}
	sub := a.Subscribe(1)
	sub.Close()
	// After the last subscriber the registry forgets the hub; a new one works.
	b := tapstream.Shared(tap, 0)
	s := b.Subscribe(4)
	defer s.Close()
	publish(t, tap, `{"n":1}`)
	select {
	case <-s.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("re-created shared hub delivered nothing")
	}
}

func TestEventLimitedTruncatesRawFieldsAndMeta(t *testing.T) {
	long := strings.Repeat("x", 100)
	msg := message.New([]byte(long+long), "flow", "127.0.0.1:1")
	msg.Fields = map[string]any{"short": "ok", "long": long, "nested": map[string]any{"deep": []any{long, 1.0}}}
	msg.Meta["note"] = long
	event, err := tapstream.EventLimited(pipeline.TapEvent{Msg: msg}, 16)
	if err != nil {
		t.Fatal(err)
	}
	if !event.Truncated {
		t.Fatal("truncated flag not set")
	}
	if event.Raw != strings.Repeat("x", 16)+"…" {
		t.Fatalf("raw = %q", event.Raw)
	}
	fields := event.Fields.AsMap()
	if fields["short"] != "ok" || fields["long"] != strings.Repeat("x", 16)+"…" {
		t.Fatalf("fields = %v", fields)
	}
	if deep := fields["nested"].(map[string]any)["deep"].([]any); deep[0] != strings.Repeat("x", 16)+"…" || deep[1] != 1.0 {
		t.Fatalf("nested = %v", deep)
	}
	if event.Meta["note"] != strings.Repeat("x", 16)+"…" {
		t.Fatalf("meta = %v", event.Meta)
	}
	// The message's own data is untouched.
	if msg.Fields["long"] != long || msg.Meta["note"] != long {
		t.Fatal("truncation modified the message")
	}
	// Multi-byte boundary stays valid UTF-8.
	msg.Raw = []byte(strings.Repeat("é", 20))
	event, _ = tapstream.EventLimited(pipeline.TapEvent{Msg: msg}, 5)
	if !strings.HasPrefix(event.Raw, "éé…") {
		t.Fatalf("utf-8 cut = %q", event.Raw)
	}
	// Unlimited leaves everything alone.
	event, _ = tapstream.EventLimited(pipeline.TapEvent{Msg: msg}, 0)
	if event.Truncated || event.Raw != strings.Repeat("é", 20) {
		t.Fatalf("unlimited = %+v", event)
	}
}
