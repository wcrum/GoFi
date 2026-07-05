package pipeline

import (
	"fmt"

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
// ListenerDefinition's parser + stage chain (+ sink `when` evaluation).
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
func Simulate(def spec.ListenerDefinition, raw []byte) (*SimulateResult, error) {
	// Validate the chain up front (dup/empty IDs, unknown kinds, bad CEL)
	// via filter.NewChain — the built Chain itself is discarded; we drive
	// stages individually below via filter.New for per-stage tracing, since
	// Chain's internals are unexported.
	if _, err := filter.NewChain(def.Stages); err != nil {
		return nil, err
	}

	pr, err := parser.New(def.Parser)
	if err != nil {
		return nil, fmt.Errorf("simulate: parser: %w", err)
	}

	msg := message.New(raw, def.Name, "mock")
	result := &SimulateResult{}
	if perr := pr.Parse(msg); perr != nil {
		// Matches Pipeline.Run: a parse error is logged/reported but
		// processing continues with whatever Fields got populated.
		result.ParseError = perr.Error()
	}
	result.ParsedFields = cloneFields(msg.Fields)

	for _, s := range def.Stages {
		st, err := filter.New(s)
		if err != nil {
			return nil, fmt.Errorf("simulate: build stage %q: %w", s.ID, err)
		}
		pass, perr := st.Process(msg)
		trace := StageTrace{ID: s.ID, Kind: s.Kind, Type: s.Type, Pass: pass, Fields: cloneFields(msg.Fields)}
		if perr != nil {
			trace.Error = perr.Error()
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
			result.DeadLetter = def.DeadLetterSink != nil
			return result, nil
		}
	}

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
	}
	return result, nil
}

func cloneFields(f map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(f))
	for k, v := range f {
		out[k] = v
	}
	return out
}
