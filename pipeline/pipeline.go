// Package pipeline chains a Framer → Parser → Stage chain → Sink(s) for one
// connection. It also maintains a Tap: a broadcast channel subscribers can
// attach to for live traffic inspection through ConnectRPC streaming.
package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"cel.dev/cel-go/cel"

	"github.com/wcrum/gofi/codec"
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

// broadcast returns how many subscribers missed the event.
func (t *Tap) broadcast(ev TapEvent) (dropped int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for ch := range t.subs {
		select {
		case ch <- ev:
		default: // slow subscriber — drop the event rather than block
			dropped++
		}
	}
	return dropped
}

// sinkTarget pairs a Sink with its optional compiled `when` condition. A nil
// when means "always send". The pairing between sinks and their SinkCfg.When
// is by INDEX: sinks[i] corresponds to def.Sinks[i] in New, since both are
// built from the same def.Sinks slice in the same order (listener.New builds
// sinks from def.Sinks, and New here zips them back up with def.Sinks[i].When
// by that same index) — keep this invariant in mind if either slice is ever
// reordered independently of the other.
type sinkTarget struct {
	sink     sink.Sink
	kind     string
	route    string               // observability route ID, matching the connectivity path
	health   *observability.Route // cached recorder for route
	when     cel.Program          // nil = always send
	encoding *spec.Encoding       // nil = the sink's default payload
	async    bool                 // the sink records its own delivery outcomes (sink.Async)
}

// Routing is a flow's compiled sink and dead-letter `when` conditions.
// listener.New compiles them once (CompileRouting) and every pipeline of the
// listener shares them; compiling is tens of microseconds and hundreds of
// allocations, far more than a datagram's whole run.
type Routing struct {
	SinkWhen       []cel.Program // by sink index; nil = always send
	DeadLetterWhen cel.Program
}

// CompileRouting compiles def's sink and dead-letter conditions.
func CompileRouting(def spec.FlowDefinition) (*Routing, error) {
	r := &Routing{SinkWhen: make([]cel.Program, len(def.Sinks))}
	for i, sc := range def.Sinks {
		if sc.When == "" {
			continue
		}
		prog, err := filter.CompileCEL(sc.When)
		if err != nil {
			return nil, err
		}
		r.SinkWhen[i] = prog
	}
	if def.DeadLetterSink != nil && def.DeadLetterSink.When != "" {
		prog, err := filter.CompileCEL(def.DeadLetterSink.When)
		if err != nil {
			return nil, err
		}
		r.DeadLetterWhen = prog
	}
	return r, nil
}

// Pipeline runs the full processing chain. One Pipeline serves every stream
// of a listener: Run processes the framer New attached; RunReader frames a
// reader (a datagram, a pulled message) and ProcessFrame handles one frame.
// All of them may run concurrently on the same Pipeline: its parser, chain,
// sinks, and metric handles are shared and safe for concurrent use.
type Pipeline struct {
	name           string
	framing        spec.Framing
	framer         framing.Framer // only set by New (one stream)
	parser         parser.Parser
	parserConfig   spec.ParserCfg
	chain          *filter.Chain
	sinks          []sinkTarget
	deadLetterSink sink.Sink
	deadLetterType string
	deadLetterWhen cel.Program
	deadLetterEnc  *spec.Encoding
	deadLetterRt   *observability.Route
	metrics        *observability.Flow
	tap            *Tap
}

// New builds a Pipeline for one already-opened reader (a TCP connection):
// NewShared plus the stream's framer. sinks, deadLetterSink, and chain are
// passed in because they are owned by the listener (shared across every
// connection of that listener) — see listener.New, which builds them exactly
// once per listener.
func New(def spec.FlowDefinition, r io.Reader, sinks []sink.Sink, deadLetterSink sink.Sink, chain *filter.Chain, tap *Tap) (*Pipeline, error) {
	p, err := NewShared(def, sinks, deadLetterSink, chain, tap, nil)
	if err != nil {
		return nil, err
	}
	if p.framer, err = framing.New(def.Framing, r); err != nil {
		return nil, err
	}
	return p, nil
}

// NewShared builds a Pipeline with no stream attached, to be reused for
// every connection, datagram, or pulled message of a listener through
// RunReader and ProcessFrame. routing may be nil, in which case the sink
// conditions are compiled here.
func NewShared(def spec.FlowDefinition, sinks []sink.Sink, deadLetterSink sink.Sink, chain *filter.Chain, tap *Tap, routing *Routing) (*Pipeline, error) {
	pr, err := parser.ForFlow(def)
	if err != nil {
		return nil, err
	}
	if routing == nil {
		if routing, err = CompileRouting(def); err != nil {
			return nil, err
		}
	}

	stageIDs := make([]string, 0, len(def.Stages))
	for _, st := range def.Stages {
		stageIDs = append(stageIDs, st.ID)
	}
	sinkTypes := make([]string, 0, len(def.Sinks)+1)
	for _, sc := range def.Sinks {
		sinkTypes = append(sinkTypes, sc.Type)
	}
	metrics := observability.NewFlow(def.Name, stageIDs, sinkTypes)

	targets := make([]sinkTarget, len(sinks))
	for i, sk := range sinks {
		st := sinkTarget{sink: sk, route: observability.RouteSink(i)}
		st.health = metrics.Route(st.route)
		st.async = isAsync(sk)
		if i < len(def.Sinks) {
			st.kind = def.Sinks[i].Type
			st.encoding = def.Sinks[i].Encoding
		}
		if i < len(routing.SinkWhen) {
			st.when = routing.SinkWhen[i]
		}
		targets[i] = st
	}

	deadLetterType := ""
	if def.DeadLetterSink != nil {
		deadLetterType = def.DeadLetterSink.Type
	}
	return &Pipeline{
		name:           def.Name,
		framing:        def.Framing,
		parser:         pr,
		parserConfig:   def.Parser,
		chain:          chain,
		sinks:          targets,
		deadLetterSink: deadLetterSink,
		deadLetterType: deadLetterType,
		deadLetterWhen: routing.DeadLetterWhen,
		deadLetterEnc:  deadLetterEncoding(def),
		deadLetterRt:   metrics.Route(observability.RouteDeadLetter),
		metrics:        metrics,
		tap:            tap,
	}, nil
}

// Run processes frames from the reader given to New until it is exhausted or
// ctx is cancelled.
func (p *Pipeline) Run(ctx context.Context, remoteAddr string) {
	p.run(ctx, p.framer, remoteAddr)
}

// RunReader frames r with the flow's framing and processes every frame: one
// datagram or pulled message is one stream. Safe to call concurrently.
func (p *Pipeline) RunReader(ctx context.Context, r io.Reader, remoteAddr string) {
	fr, err := framing.New(p.framing, r)
	if err != nil {
		// Validated when the listener was built; unreachable in practice.
		slog.Error("framer build error", "listener", p.name, "error", err)
		return
	}
	p.run(ctx, fr, remoteAddr)
}

func (p *Pipeline) run(ctx context.Context, fr framing.Framer, remoteAddr string) {
	for {
		if ctx.Err() != nil {
			return
		}

		raw, err := fr.ReadFrame()
		if err != nil {
			if errors.Is(err, framing.ErrFramer) {
				// The framer itself failed, so the rest of the connection is dropped.
				slog.Warn("framer error; connection dropped", "listener", p.name, "remote", remoteAddr, "error", err)
				observability.Report(p.name, observability.SeverityWarning, observability.ComponentFramer, "connection dropped: "+err.Error())
			} else if err != io.EOF && ctx.Err() == nil {
				slog.Debug("frame read error", "listener", p.name, "remote", remoteAddr, "error", err)
			}
			return
		}
		p.ProcessFrame(ctx, raw, remoteAddr)
	}
}

// ProcessFrame runs one frame through parse, stages, tap, and sinks. raw is
// owned by the message from here on (the framers hand out copies).
func (p *Pipeline) ProcessFrame(ctx context.Context, raw []byte, remoteAddr string) {
	p.metrics.Frame(len(raw))
	msg := message.New(raw, p.name, remoteAddr)
	for k, v := range message.SourceMeta(ctx) {
		msg.Meta[k] = v
	}

	if err := p.parser.Parse(msg); err != nil {
		if !errors.Is(err, parser.ErrRejected) {
			p.metrics.ParseError()
			slog.Warn("parse error", "listener", p.name, "error", err)
			observability.Report(p.name, observability.SeverityWarning, observability.ComponentParser, err.Error())
			msg.Meta["parseError"] = err.Error()
		}
		if codec.IsBudgetExceeded(err) {
			// Decoding this frame would exceed the per-message budget:
			// dropped whatever the parser's error policy says.
			observability.DecodeBudgetExceeded(p.name)
			p.reject(msg, "decode")
			return
		}
		if parser.ShouldDrop(p.parserConfig, err) {
			p.reject(msg, "parser")
			return
		}
	}

	pass, droppedBy, err := p.chain.Pass(msg)
	if err != nil {
		slog.Warn("stage eval error", "listener", p.name, "error", err)
		if stageErr := (*filter.StageError)(nil); errors.As(err, &stageErr) {
			observability.StageError(p.name, stageErr.ID)
		}
		if codec.IsBudgetExceeded(err) {
			observability.DecodeBudgetExceeded(p.name)
		}
		observability.Report(p.name, observability.SeverityWarning, stageComponent(err), err.Error())
	}
	if !pass {
		p.reject(msg, droppedBy)
		return
	}

	p.metrics.TapDropped(p.tap.broadcast(TapEvent{Msg: msg, Dropped: false}))

	for i := range p.sinks {
		st := &p.sinks[i]
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
				observability.Report(p.name, observability.SeverityWarning, observability.RouteComponent(st.route), "when condition: "+err.Error())
				continue
			}
			if !ok {
				continue
			}
		}
		started := time.Now()
		out, err := Encode(ctx, st.encoding, msg)
		if err == nil {
			err = st.sink.Send(out)
			if st.async {
				// Enqueued: the queue worker records delivery. An error here
				// is a queue-full drop, counted by the queue's own metrics.
				continue
			}
			st.health.Result(err) // a failure is reported by RouteResult
		} else {
			observability.Report(p.name, observability.SeverityError, observability.RouteComponent(st.route), "encoding: "+err.Error())
		}
		p.metrics.SinkDispatch(st.kind, time.Since(started), err)
		if err != nil {
			slog.Warn("sink error", "listener", p.name, "error", err)
		}
	}
}

// isAsync reports whether s completes sends after Send returns.
func isAsync(s sink.Sink) bool {
	a, ok := s.(sink.Async)
	return ok && a.Async()
}

func (p *Pipeline) reject(msg *message.Message, droppedBy string) {
	p.metrics.Rejected(droppedBy)
	// Populate metadata before publishing the message to concurrent readers.
	msg.Meta["droppedBy"] = droppedBy
	p.metrics.TapDropped(p.tap.broadcast(TapEvent{Msg: msg, Dropped: true, DroppedBy: droppedBy}))
	if p.deadLetterSink != nil {
		if p.deadLetterWhen != nil {
			pass, err := filter.EvalCELBool(p.deadLetterWhen, msg)
			if err != nil || !pass {
				return
			}
		}
		started := time.Now()
		out, err := Encode(context.Background(), p.deadLetterEnc, msg)
		if err == nil {
			err = p.deadLetterSink.Send(out)
			if isAsync(p.deadLetterSink) {
				return
			}
			p.deadLetterRt.Result(err)
		}
		p.metrics.SinkDispatch(p.deadLetterType, time.Since(started), err)
		if err != nil {
			slog.Warn("dead-letter sink error", "listener", p.name, "error", err)
		}
	}
}

// stageComponent names the stage a chain error came from ("stage \"id\": …").
func stageComponent(err error) string {
	if stageErr := (*filter.StageError)(nil); errors.As(err, &stageErr) {
		return observability.ComponentStage(stageErr.ID)
	}
	text := err.Error()
	if rest, ok := strings.CutPrefix(text, "stage "); ok {
		if id, err := strconv.QuotedPrefix(rest); err == nil {
			if unquoted, err := strconv.Unquote(id); err == nil {
				return observability.ComponentStage(unquoted)
			}
		}
	}
	return "stages"
}
