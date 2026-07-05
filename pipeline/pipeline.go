// Package pipeline chains a Framer → Parser → Stage chain → Sink(s) for one
// connection. It also maintains a Tap: a broadcast channel subscribers can
// attach to for live traffic inspection (used by the SSE endpoint in the API).
package pipeline

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/google/cel-go/cel"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/framing"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/parser"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// TapEvent is a message observed on the tap. It carries the processed message
// plus a Dropped flag if a stage in the chain rejected it.
type TapEvent struct {
	Msg       *message.Message
	Dropped   bool
	DroppedBy string
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

// sinkTarget pairs a Sink with its optional compiled `when` condition. A nil
// when means "always send". The pairing between sinks and their SinkCfg.When
// is by INDEX: sinks[i] corresponds to def.Sinks[i] in New, since both are
// built from the same def.Sinks slice in the same order (listener.New builds
// sinks from def.Sinks, and New here zips them back up with def.Sinks[i].When
// by that same index) — keep this invariant in mind if either slice is ever
// reordered independently of the other.
type sinkTarget struct {
	sink sink.Sink
	when cel.Program // nil = always send
}

// Pipeline runs the full processing chain for one connection stream.
type Pipeline struct {
	name           string
	framer         framing.Framer
	parser         parser.Parser
	chain          *filter.Chain
	sinks          []sinkTarget
	deadLetterSink sink.Sink
	tap            *Tap
}

// New builds a Pipeline from a ListenerDefinition and an already-opened
// reader. sinks, deadLetterSink, and chain are passed in because they are
// owned by the listener (shared across every connection of that listener) —
// see listener.New, which builds them exactly once per listener.
func New(def spec.ListenerDefinition, r io.Reader, sinks []sink.Sink, deadLetterSink sink.Sink, chain *filter.Chain, tap *Tap) (*Pipeline, error) {
	fr, err := framing.New(def.Framing, r)
	if err != nil {
		return nil, err
	}
	pr, err := parser.New(def.Parser)
	if err != nil {
		return nil, err
	}

	targets := make([]sinkTarget, len(sinks))
	for i, sk := range sinks {
		st := sinkTarget{sink: sk}
		if i < len(def.Sinks) && def.Sinks[i].When != "" {
			prog, err := filter.CompileCEL(def.Sinks[i].When)
			if err != nil {
				return nil, err
			}
			st.when = prog
		}
		targets[i] = st
	}

	return &Pipeline{
		name:           def.Name,
		framer:         fr,
		parser:         pr,
		chain:          chain,
		sinks:          targets,
		deadLetterSink: deadLetterSink,
		tap:            tap,
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

		pass, droppedBy, err := p.chain.Pass(msg)
		if err != nil {
			slog.Warn("stage eval error", "listener", p.name, "error", err)
		}
		if !pass {
			p.tap.broadcast(TapEvent{Msg: msg, Dropped: true, DroppedBy: droppedBy})
			if p.deadLetterSink != nil {
				msg.Meta["droppedBy"] = droppedBy
				if err := p.deadLetterSink.Send(msg); err != nil {
					slog.Warn("dead-letter sink error", "listener", p.name, "error", err)
				}
			}
			continue
		}

		p.tap.broadcast(TapEvent{Msg: msg, Dropped: false})

		for _, st := range p.sinks {
			if st.when != nil {
				ok, err := filter.EvalCELBool(st.when, msg)
				if err != nil {
					// Fail CLOSED for routing conditions specifically: an
					// unevaluable `when` shouldn't silently fan out to a
					// sink it wasn't meant for — a routing mistake sending
					// to the WRONG destination is worse than a message
					// simply not reaching an ambiguous one. This is the
					// deliberate opposite of the stage chain's fail-open
					// behavior above.
					slog.Warn("sink 'when' eval error", "listener", p.name, "error", err)
					continue
				}
				if !ok {
					continue
				}
			}
			if err := st.sink.Send(msg); err != nil {
				slog.Warn("sink error", "listener", p.name, "error", err)
			}
		}
	}
}
