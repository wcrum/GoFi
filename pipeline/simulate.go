package pipeline

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/parser"
	"github.com/wcrum/gofi/spec"
)

// StageTrace captures one stage's outcome during a Simulate run.
type StageTrace struct {
	ID     string                 `json:"id"`
	Kind   string                 `json:"kind"`
	Type   string                 `json:"type,omitempty"`
	Pass   bool                   `json:"pass"`
	Error  string                 `json:"error,omitempty"`
	Note   string                 `json:"note,omitempty"`
	Fields map[string]interface{} `json:"fields"` // snapshot of msg.Fields AFTER this stage ran
	// Python stages: captured print output and execution time.
	Stdout     string `json:"stdout,omitempty"`
	DurationUs int64  `json:"durationUs,omitempty"`
}

// SinkOutcome reports whether a sink WOULD have received the message.
// Simulate never calls Sink.Send — this is evaluation-only (the sink's
// `When` condition, same semantics as Pipeline.Run), a dry run.
type SinkOutcome struct {
	Index  int    `json:"index"`
	Type   string `json:"type"`
	Sent   bool   `json:"sent"`
	Reason string `json:"reason,omitempty"`
}

// SimulateResult is the full trace of one dry-run message through a
// FlowDefinition's parser + stage chain (+ sink `when` evaluation).
type SimulateResult struct {
	ParseError   string                 `json:"parseError,omitempty"`
	ParsedFields map[string]interface{} `json:"parsedFields"`
	Stages       []StageTrace           `json:"stages"`
	Dropped      bool                   `json:"dropped"`
	DroppedBy    string                 `json:"droppedBy,omitempty"`
	DeadLetter   bool                   `json:"deadLetter"`
	Sinks        []SinkOutcome          `json:"sinks,omitempty"`
}

// Simulate runs raw through def's parser and stage chain exactly like a real
// connection would (same filter.New/Stage.Process calls Pipeline.Run uses),
// but NEVER calls Sink.Send — sink outcomes are evaluated (When-only) and
// reported, not dispatched. Builds fresh stage instances per call, so
// rate-limit/dedup state is NOT shared across Simulate calls or with any
// live listener's chain — each simulation starts clean, which is correct
// for independent test-case playback (a test case shouldn't be affected by
// how many times it, or another test case, was played before).
func Simulate(def spec.FlowDefinition, raw []byte) (*SimulateResult, error) {
	result, _, err := simulateGuarded(def, raw, "")
	return result, err
}

// simulate runs the dry run; with a non-empty stop (a resolved DebugStep) it
// halts just before that Python step and returns the exact input it would
// receive. A nil target with a nil error means the frame never reached it.
func simulate(def spec.FlowDefinition, raw []byte, stop string) (*SimulateResult, *DebugTarget, error) {
	def, err := def.Prepared()
	if err != nil {
		return nil, nil, err
	}
	// Inline stages always run the source being simulated (possibly unsaved),
	// never a managed library that may still hold the previous version.
	def.Stages = append([]spec.StageSpec(nil), def.Stages...)
	for i := range def.Stages {
		if ref := def.Stages[i].Python; ref != nil && ref.InlineSource != "" {
			copied := *ref
			def.Stages[i].Python = &copied
		}
	}
	def.UnresolveInlinePython()
	// Validate the chain up front (dup/empty IDs, unknown kinds, bad CEL)
	// via filter.NewChain — the built Chain itself is discarded; we drive
	// stages individually below via filter.New for per-stage tracing, since
	// Chain's internals are unexported.
	if _, err := filter.NewChain(def.Stages); err != nil {
		return nil, nil, err
	}

	pr, err := parser.ForFlow(def)
	if err != nil {
		return nil, nil, fmt.Errorf("simulate: parser: %w", err)
	}

	msg := message.New(raw, def.Name, "mock")
	result := &SimulateResult{}
	if stop == "framer" {
		return result, capture(stop, def.Framing.Python, "framer", msg.Raw, msg), nil
	}
	// A Python decoder is debugged with the bytes the steps before it produce.
	decode := def.DecodeSteps()
	for i, d := range decode.Decoders {
		if stop != DecoderStep(i) {
			continue
		}
		before := decode
		before.Decoders = decode.Decoders[:i]
		input, derr := parser.DecodeFrame(before, msg.Raw)
		if derr != nil {
			result.ParseError = derr.Error()
			return result, nil, nil
		}
		return result, capture(stop, d.Python, "decoder", input, msg), nil
	}
	if stop == "parser" {
		decoded, derr := parser.DecodeFrame(decode, msg.Raw)
		if derr != nil {
			result.ParseError = derr.Error()
			return result, nil, nil
		}
		return result, capture(stop, def.Parser.Python, "parser", decoded, msg), nil
	}
	if perr := pr.Parse(msg); perr != nil {
		if !errors.Is(perr, parser.ErrRejected) {
			result.ParseError = perr.Error()
			msg.Meta["parseError"] = perr.Error()
		}
		if parser.ShouldDrop(def.Parser, perr) {
			result.ParsedFields = cloneFields(msg.Fields)
			result.Dropped = true
			result.DroppedBy = "parser"
			msg.Meta["droppedBy"] = result.DroppedBy
			result.DeadLetter = simulateDeadLetter(def.DeadLetterSink, msg)
			return result, nil, nil
		}
	}
	result.ParsedFields = cloneFields(msg.Fields)

	for _, s := range def.Stages {
		if stop != "" && s.ID == stop {
			return result, capture(stop, s.Python, "transformer", msg.Raw, msg), nil
		}
		if s.Kind == "parse" && s.Parse != nil && stop != "" && strings.HasPrefix(stop, s.ID+"/") {
			if target, terr := captureParse(s, stop, msg); terr != nil {
				result.Stages = append(result.Stages, StageTrace{ID: s.ID, Kind: s.Kind, Type: s.Parse.Format, Error: terr.Error(), Fields: cloneFields(msg.Fields)})
				return result, nil, nil
			} else if target != nil {
				return result, target, nil
			}
		}
		st, err := filter.New(s)
		if err != nil {
			return nil, nil, fmt.Errorf("simulate: build stage %q: %w", s.ID, err)
		}
		pass, perr := st.Process(msg)
		trace := StageTrace{ID: s.ID, Kind: s.Kind, Type: s.Type, Pass: pass, Fields: cloneFields(msg.Fields)}
		if s.Kind == "parse" && s.Parse != nil {
			trace.Type = s.Parse.Format
		}
		if perr != nil {
			trace.Error = perr.Error()
		}
		if observed, ok := st.(filter.Observed); ok {
			trace.Stdout, trace.DurationUs = observed.Observation()
		}
		if s.Kind == "rate-limit" || s.Kind == "dedup" {
			trace.Note = "This stage holds state shared across live traffic (a running per-second counter or dedup window) " +
				"that a single simulated message can't reproduce in isolation — it always evaluates to \"passed\" here " +
				"regardless of what would happen during real, ongoing traffic."
		}
		result.Stages = append(result.Stages, trace)
		if !pass {
			result.Dropped = true
			result.DroppedBy = s.ID
			msg.Meta["droppedBy"] = result.DroppedBy
			result.DeadLetter = simulateDeadLetter(def.DeadLetterSink, msg)
			return result, nil, nil
		}
	}

	var target *DebugTarget
	for i, sk := range def.Sinks {
		outcome := SinkOutcome{Index: i, Type: sk.Type, Sent: true}
		if sk.When != "" {
			prog, cerr := filter.CompileCEL(sk.When)
			switch {
			case cerr != nil:
				outcome.Sent, outcome.Reason = false, "when compile error: "+cerr.Error()
			default:
				ok, eerr := filter.EvalCELBool(prog, msg)
				switch {
				case eerr != nil:
					outcome.Sent, outcome.Reason = false, "when eval error: "+eerr.Error()
				case !ok:
					outcome.Sent, outcome.Reason = false, "when: false"
				}
			}
		}
		result.Sinks = append(result.Sinks, outcome)
		if stop == SinkStep(i) && outcome.Sent {
			target = capture(stop, sk.Python, "sink", msg.Raw, msg)
		}
	}
	return result, target, nil
}

func cloneFields(f map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(f))
	for k, v := range f {
		out[k] = v
	}
	return out
}

func simulateDeadLetter(cfg *spec.SinkCfg, msg *message.Message) bool {
	if cfg == nil {
		return false
	}
	if cfg.When == "" {
		return true
	}
	program, err := filter.CompileCEL(cfg.When)
	if err != nil {
		return false
	}
	send, err := filter.EvalCELBool(program, msg)
	return err == nil && send
}
