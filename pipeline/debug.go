package pipeline

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// DebugTarget is a Python step of a flow together with the exact input a
// simulated frame delivered to it.
type DebugTarget struct {
	Step   string // "framer", "decoder:<i>", "parser", a stage id, "<parse stage id>/parser", "<parse stage id>/decoder:<i>", or "sink:<index>"
	Ref    spec.PythonRef
	Kind   string // worker function kind (spec.FunctionKinds)
	Raw    []byte
	Fields map[string]any
	Meta   map[string]string
}

// DecoderStep names the debug step of decoder i.
func DecoderStep(i int) string { return "decoder:" + strconv.Itoa(i) }

// ParseParserStep names the debug step of a parse stage's Python parser.
func ParseParserStep(stageID string) string { return stageID + "/parser" }

// ParseDecoderStep names the debug step of a parse stage's decoder i.
func ParseDecoderStep(stageID string, i int) string { return stageID + "/" + DecoderStep(i) }

// SinkStep names the debug step of sink i.
func SinkStep(i int) string { return "sink:" + strconv.Itoa(i) }

// ResolveDebugStep validates step (or picks the first Python step when it is
// empty) and reports the worker kind it runs as.
func ResolveDebugStep(def spec.FlowDefinition, step string) (string, string, error) {
	def, err := def.Prepared()
	if err != nil {
		return "", "", err
	}
	type candidate struct{ step, kind string }
	var steps []candidate
	if def.Framing.Mode == "python" && def.Framing.Python != nil {
		steps = append(steps, candidate{"framer", "framer"})
	}
	for i, d := range def.DecodeSteps().Decoders {
		if d.Type == "python" && d.Python != nil {
			steps = append(steps, candidate{DecoderStep(i), "decoder"})
		}
	}
	if def.Parser.Python != nil && def.Parser.Format == "python" {
		steps = append(steps, candidate{"parser", "parser"})
	}
	for _, s := range def.Stages {
		if s.Kind == "python" && s.Python != nil {
			steps = append(steps, candidate{s.ID, "transformer"})
		}
		if s.Kind == "parse" && s.Parse != nil {
			for i, d := range s.Parse.Decoders {
				if d.Type == "python" && d.Python != nil {
					steps = append(steps, candidate{ParseDecoderStep(s.ID, i), "decoder"})
				}
			}
			if s.Parse.Format == "python" && s.Parse.Python != nil {
				steps = append(steps, candidate{ParseParserStep(s.ID), "parser"})
			}
		}
	}
	for i, s := range def.Sinks {
		if s.Python != nil {
			steps = append(steps, candidate{SinkStep(i), "sink"})
		}
	}
	if len(steps) == 0 {
		return "", "", fmt.Errorf("flow %q has no Python steps to debug", def.Name)
	}
	if step == "" {
		return steps[0].step, steps[0].kind, nil
	}
	for _, c := range steps {
		if c.step == step {
			return c.step, c.kind, nil
		}
	}
	names := make([]string, len(steps))
	for i, c := range steps {
		names[i] = c.step
	}
	return "", "", fmt.Errorf("step %q is not a Python step of this flow (choose one of %s)", step, strings.Join(names, ", "))
}

// SimulateTo dry-runs raw through def (nothing is sent) up to the Python step,
// returning the simulation so far and the step's exact input. notReached
// explains why the frame never got there (dropped, parse error, condition).
func SimulateTo(def spec.FlowDefinition, raw []byte, step string) (result *SimulateResult, target *DebugTarget, notReached string, err error) {
	resolved, kind, err := ResolveDebugStep(def, step)
	if err != nil {
		return nil, nil, "", err
	}
	result, target, err = simulateGuarded(def, raw, resolved)
	if err != nil {
		return nil, nil, "", err
	}
	if target != nil {
		target.Kind = kind
		return result, target, "", nil
	}
	switch {
	case result.Dropped:
		notReached = fmt.Sprintf("the frame was dropped by %s before reaching %s", result.DroppedBy, resolved)
	case result.ParseError != "":
		notReached = fmt.Sprintf("the frame failed decoding/parsing before reaching %s: %s", resolved, result.ParseError)
	case strings.HasPrefix(resolved, "sink:"):
		notReached = fmt.Sprintf("%s's condition did not select this frame", resolved)
	default:
		notReached = fmt.Sprintf("the frame did not reach %s", resolved)
	}
	return result, nil, notReached, nil
}

func capture(step string, ref *spec.PythonRef, kind string, raw []byte, msg *message.Message) *DebugTarget {
	target := &DebugTarget{Step: step, Kind: kind, Raw: append([]byte(nil), raw...), Fields: cloneFields(msg.Fields), Meta: make(map[string]string, len(msg.Meta))}
	if ref != nil {
		target.Ref = *ref
	}
	for k, v := range msg.Meta {
		target.Meta[k] = v
	}
	return target
}

// captureParse returns the debug target of a parse stage step (a nil target
// when stop names no step of this stage): the exact bytes its Python
// decoder or parser receives. A parse function starts from empty fields.
func captureParse(s spec.StageSpec, stop string, msg *message.Message) (*DebugTarget, error) {
	empty := &message.Message{Raw: msg.Raw, Fields: map[string]any{}, Meta: msg.Meta}
	if stop == ParseParserStep(s.ID) {
		input, err := filter.ParseInput(s, msg, -1)
		if err != nil {
			return nil, err
		}
		return capture(stop, s.Parse.Python, "parser", input, empty), nil
	}
	for i, d := range s.Parse.Decoders {
		if stop == ParseDecoderStep(s.ID, i) {
			input, err := filter.ParseInput(s, msg, i)
			if err != nil {
				return nil, err
			}
			return capture(stop, d.Python, "decoder", input, msg), nil
		}
	}
	return nil, nil
}
