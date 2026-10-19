package tapstream

import (
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/pipeline"
)

// LayerRuntime labels this hub's drops and subscriptions in metrics: it runs
// in the process that owns the pipeline (a listener Pod, a container, or the
// controller itself for in-process flows).
const LayerRuntime = "runtime"

// DefaultQueue is the per-subscriber queue length when a caller passes 0.
const DefaultQueue = 256

// MaxBytesEnv bounds raw and every string in fields/meta per event before it
// leaves the runtime; 0 disables truncation.
const MaxBytesEnv = "GOFI_TAP_MAX_BYTES"

// DefaultMaxBytes is the default per-event string limit.
const DefaultMaxBytes = 4096

// MaxBytes reads the configured per-event string limit.
func MaxBytes() int {
	if raw := os.Getenv(MaxBytesEnv); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			return n
		}
	}
	return DefaultMaxBytes
}

// Hub attaches to a pipeline tap once, converts each observed message to its
// RPC form exactly once, and fans the immutable result out to any number of
// subscribers over bounded queues. A slow subscriber loses events (counted
// on its Subscription and in gofi_tap_dropped_total{layer="runtime"}); it
// never slows the pipeline or another subscriber. The tap subscription opens
// with the first subscriber and closes with the last.
type Hub struct {
	tap      *pipeline.Tap
	maxBytes int
	onIdle   func()

	mu   sync.Mutex
	subs map[*Subscription]struct{}
	stop chan struct{}
	done chan struct{}
}

// NewHub wraps tap. maxBytes bounds strings per event (see MaxBytes).
func NewHub(tap *pipeline.Tap, maxBytes int) *Hub {
	return &Hub{tap: tap, maxBytes: maxBytes, subs: map[*Subscription]struct{}{}}
}

// Subscription receives converted events from a Hub.
type Subscription struct {
	hub     *Hub
	ch      chan *gofiv1.TrafficEvent
	dropped atomic.Uint64
	closed  atomic.Bool
}

// Events delivers converted events. The channel is never closed; stop
// reading when the caller's context ends and call Close.
func (s *Subscription) Events() <-chan *gofiv1.TrafficEvent { return s.ch }

// TakeDropped returns how many events this subscriber missed since the last
// call and resets the count.
func (s *Subscription) TakeDropped() uint64 { return s.dropped.Swap(0) }

// Close detaches the subscriber; the hub leaves the tap with its last one.
func (s *Subscription) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.hub.remove(s)
}

// Subscribe attaches a subscriber with a queue of the given length (0 means
// DefaultQueue).
func (h *Hub) Subscribe(queue int) *Subscription {
	if queue <= 0 {
		queue = DefaultQueue
	}
	s := &Subscription{hub: h, ch: make(chan *gofiv1.TrafficEvent, queue)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	if h.stop == nil {
		h.stop, h.done = make(chan struct{}), make(chan struct{})
		go h.pump(h.tap.Subscribe(), h.stop, h.done)
	}
	h.mu.Unlock()
	observability.TapSubscribed(LayerRuntime, 1)
	return s
}

// Subscribers reports how many subscriptions are open.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *Hub) remove(s *Subscription) {
	h.mu.Lock()
	delete(h.subs, s)
	var stop, done chan struct{}
	idle := len(h.subs) == 0 && h.stop != nil
	if idle {
		stop, done, h.stop, h.done = h.stop, h.done, nil, nil
	}
	h.mu.Unlock()
	observability.TapSubscribed(LayerRuntime, -1)
	if idle {
		close(stop)
		<-done
		if h.onIdle != nil {
			h.onIdle()
		}
	}
}

func (h *Hub) pump(ch chan pipeline.TapEvent, stop, done chan struct{}) {
	defer close(done)
	defer h.tap.Unsubscribe(ch)
	for {
		select {
		case <-stop:
			return
		case raw := <-ch:
			event, err := EventLimited(raw, h.maxBytes)
			if err != nil || event == nil {
				continue
			}
			h.broadcast(event)
		}
	}
}

func (h *Hub) broadcast(event *gofiv1.TrafficEvent) {
	var dropped uint64
	h.mu.Lock()
	for s := range h.subs {
		select {
		case s.ch <- event:
		default:
			s.dropped.Add(1)
			dropped++
		}
	}
	h.mu.Unlock()
	observability.TapDroppedAt(LayerRuntime, dropped)
}

// Shared hubs keyed by tap, so every viewer of an in-process flow shares one
// conversion; a hub leaves the registry when its last subscriber closes.
var shared = struct {
	sync.Mutex
	hubs map[*pipeline.Tap]*Hub
}{hubs: map[*pipeline.Tap]*Hub{}}

// Shared returns the process-wide hub for tap, creating it on first use.
func Shared(tap *pipeline.Tap, maxBytes int) *Hub {
	shared.Lock()
	defer shared.Unlock()
	if hub, ok := shared.hubs[tap]; ok {
		return hub
	}
	hub := NewHub(tap, maxBytes)
	hub.onIdle = func() {
		shared.Lock()
		if shared.hubs[tap] == hub && hub.Subscribers() == 0 {
			delete(shared.hubs, tap)
		}
		shared.Unlock()
	}
	shared.hubs[tap] = hub
	return hub
}
