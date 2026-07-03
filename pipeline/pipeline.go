// Package pipeline chains a Framer → Parser → Filter → Sink for one connection.
// It also maintains a Tap: a broadcast channel subscribers can attach to for
// live traffic inspection (used by the SSE endpoint in the API).
package pipeline

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/cnak-us/gogo/filter"
	"github.com/cnak-us/gogo/framing"
	"github.com/cnak-us/gogo/message"
	"github.com/cnak-us/gogo/parser"
	"github.com/cnak-us/gogo/sink"
	"github.com/cnak-us/gogo/spec"
)

// TapEvent is a message observed on the tap. It carries the processed message
// plus a Dropped flag if the CEL filter rejected it.
type TapEvent struct {
	Msg     *message.Message
	Dropped bool
}

// Tap is a fan-out broadcast to live UI subscribers. Each subscriber receives
// a channel; the pipeline sends to all of them non-blocking.
type Tap struct {
	mu   sync.RWMutex
	subs map[chan TapEvent]struct{}
}

func NewTap() *Tap {
	return &Tap{subs: make(map[chan TapEvent]struct{})}
}

// Subscribe returns a buffered channel that receives TapEvents. Call Unsubscribe
// with the same channel to stop receiving and allow cleanup.
func (t *Tap) Subscribe() chan TapEvent {
	ch := make(chan TapEvent, 64)
	t.mu.Lock()
	t.subs[ch] = struct{}{}
	t.mu.Unlock()
	return ch
}

func (t *Tap) Unsubscribe(ch chan TapEvent) {
	t.mu.Lock()
	delete(t.subs, ch)
	t.mu.Unlock()
}

func (t *Tap) broadcast(ev TapEvent) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for ch := range t.subs {
		select {
		case ch <- ev:
		default: // slow subscriber — drop the event rather than block
		}
	}
}

// Pipeline runs the full processing chain for one connection stream.
type Pipeline struct {
	name   string
	framer framing.Framer
	parser parser.Parser
	filter *filter.Filter // nil if no filter configured
	sink   sink.Sink
	tap    *Tap
}

// New builds a Pipeline from a ListenerDefinition and an already-opened reader.
// The sink is passed in because it is owned by the listener (shared across connections).
func New(def spec.ListenerDefinition, r io.Reader, s sink.Sink, tap *Tap) (*Pipeline, error) {
	fr, err := framing.New(def.Framing, r)
	if err != nil {
		return nil, err
	}
	pr, err := parser.New(def.Parser)
	if err != nil {
		return nil, err
	}
	var f *filter.Filter
	if def.Filter != nil && def.Filter.Expression != "" {
		f, err = filter.New(def.Filter.Expression)
		if err != nil {
			return nil, err
		}
	}
	return &Pipeline{
		name:   def.Name,
		framer: fr,
		parser: pr,
		filter: f,
		sink:   s,
		tap:    tap,
	}, nil
}

// Run processes frames until the reader is exhausted or ctx is cancelled.
func (p *Pipeline) Run(ctx context.Context, remoteAddr string) {
	for {
		if ctx.Err() != nil {
			return
		}

		raw, err := p.framer.ReadFrame()
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				slog.Debug("frame read error", "listener", p.name, "remote", remoteAddr, "error", err)
			}
			return
		}

		msg := message.New(raw, p.name, remoteAddr)

		if err := p.parser.Parse(msg); err != nil {
			slog.Warn("parse error", "listener", p.name, "error", err)
		}

		if p.filter != nil {
			pass, err := p.filter.Pass(msg)
			if err != nil {
				slog.Warn("filter eval error", "listener", p.name, "error", err)
			}
			if !pass {
				p.tap.broadcast(TapEvent{Msg: msg, Dropped: true})
				continue
			}
		}

		p.tap.broadcast(TapEvent{Msg: msg, Dropped: false})

		if err := p.sink.Send(msg); err != nil {
			slog.Warn("sink error", "listener", p.name, "error", err)
		}
	}
}
