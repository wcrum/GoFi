// Package pipeline chains a Framer → Parser → Stage chain → Sink(s) for one
// connection. It also maintains a Tap: a broadcast channel subscribers can
// attach to for live traffic inspection through ConnectRPC streaming.
package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"cel.dev/cel-go/cel"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/framing"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
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
	kind string
	when cel.Program // nil = always send
}

// Pipeline runs the full processing chain for one connection stream.
type Pipeline struct {
	name           string
	framer         framing.Framer
	parser         parser.Parser
	parserConfig   spec.ParserCfg
	chain          *filter.Chain
	sinks          []sinkTarget
	deadLetterSink sink.Sink
	deadLetterType string
	deadLetterWhen cel.Program
	sourceHook     *spec.PythonRef
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
		if i < len(def.Sinks) {
			st.kind = def.Sinks[i].Type
		}
		if i < len(def.Sinks) && def.Sinks[i].When != "" {
			prog, err := filter.CompileCEL(def.Sinks[i].When)
			if err != nil {
				return nil, err
			}
			st.when = prog
		}
		targets[i] = st
	}

	deadLetterType := ""
	if def.DeadLetterSink != nil {
		deadLetterType = def.DeadLetterSink.Type
	}
	var deadLetterWhen cel.Program
	if def.DeadLetterSink != nil && def.DeadLetterSink.When != "" {
		deadLetterWhen, err = filter.CompileCEL(def.DeadLetterSink.When)
		if err != nil {
			return nil, err
		}
	}
	return &Pipeline{
		name:           def.Name,
		framer:         fr,
		parser:         pr,
		parserConfig:   def.Parser,
		chain:          chain,
		sinks:          targets,
		deadLetterSink: deadLetterSink,
		deadLetterType: deadLetterType,
		deadLetterWhen: deadLetterWhen,
		sourceHook:     def.Source.Python,
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

		observability.Frame(p.name, len(raw))
		msg := message.New(raw, p.name, remoteAddr)
		if p.sourceHook != nil {
			drop, err := applySourceHook(ctx, p.sourceHook, msg)
			if err != nil {
				msg.Meta["sourceError"] = err.Error()
				p.reject(msg, "listener")
				continue
			}
			if drop {
				p.reject(msg, "listener")
				continue
			}
		}

		if err := p.parser.Parse(msg); err != nil {
			if !errors.Is(err, parser.ErrRejected) {
				observability.ParseError(p.name)
				slog.Warn("parse error", "listener", p.name, "error", err)
				msg.Meta["parseError"] = err.Error()
			}
			if parser.ShouldDrop(p.parserConfig, err) {
				p.reject(msg, "parser")
				continue
			}
		}

		pass, droppedBy, err := p.chain.Pass(msg)
		if err != nil {
			slog.Warn("stage eval error", "listener", p.name, "error", err)
		}
		if !pass {
			p.reject(msg, droppedBy)
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
			started := time.Now()
			err := st.sink.Send(msg)
			observability.SinkDispatch(p.name, st.kind, time.Since(started), err)
			if err != nil {
				slog.Warn("sink error", "listener", p.name, "error", err)
			}
		}
	}
}

func (p *Pipeline) reject(msg *message.Message, droppedBy string) {
	observability.Rejected(p.name, droppedBy)
	// Populate metadata before publishing the message to concurrent readers.
	msg.Meta["droppedBy"] = droppedBy
	p.tap.broadcast(TapEvent{Msg: msg, Dropped: true, DroppedBy: droppedBy})
	if p.deadLetterSink != nil {
		if p.deadLetterWhen != nil {
			pass, err := filter.EvalCELBool(p.deadLetterWhen, msg)
			if err != nil || !pass {
				return
			}
		}
		started := time.Now()
		err := p.deadLetterSink.Send(msg)
		observability.SinkDispatch(p.name, p.deadLetterType, time.Since(started), err)
		if err != nil {
			slog.Warn("dead-letter sink error", "listener", p.name, "error", err)
		}
	}
}
