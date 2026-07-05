package filter

import (
	"fmt"

	"github.com/google/cel-go/cel"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// transformStage mutates a message's Fields in place. It never drops a
// message (Process always returns pass=true), except that a "set" op's
// ValueExpr eval error is still surfaced as a non-nil error (fail-open: the
// message still passes, unmutated for this stage).
type transformStage struct {
	op        string
	field     string
	newField  string
	valueExpr string
	program   cel.Program // compiled ValueExpr, only set for op=="set"
}

func newTransform(cfg spec.StageSpec) (Stage, error) {
	switch cfg.TransformOp {
	case "set", "rename", "remove":
	default:
		return nil, fmt.Errorf("transform: unknown transformOp %q", cfg.TransformOp)
	}
	if cfg.Field == "" {
		return nil, fmt.Errorf("transform: field is required")
	}

	t := &transformStage{op: cfg.TransformOp, field: cfg.Field, newField: cfg.NewField, valueExpr: cfg.ValueExpr}

	switch cfg.TransformOp {
	case "set":
		if cfg.ValueExpr == "" {
			return nil, fmt.Errorf("transform: valueExpr is required for op %q", cfg.TransformOp)
		}
		prog, err := CompileCEL(cfg.ValueExpr)
		if err != nil {
			return nil, fmt.Errorf("transform: compile valueExpr %q: %w", cfg.ValueExpr, err)
		}
		t.program = prog
	case "rename":
		if cfg.NewField == "" {
			return nil, fmt.Errorf("transform: newField is required for op %q", cfg.TransformOp)
		}
	}

	return t, nil
}

func (t *transformStage) Process(msg *message.Message) (bool, error) {
	switch t.op {
	case "set":
		val, err := EvalCEL(t.program, msg)
		if err != nil {
			return true, fmt.Errorf("transform: eval valueExpr: %w", err)
		}
		msg.Fields[t.field] = val
	case "rename":
		if v, ok := msg.Fields[t.field]; ok {
			msg.Fields[t.newField] = v
			delete(msg.Fields, t.field)
		}
	case "remove":
		delete(msg.Fields, t.field)
	}
	return true, nil
}
