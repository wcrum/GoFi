// Package filter evaluates a chain of stages against parsed messages,
// matching the registry pattern used by parser/framing/sink.
package filter

import (
	"fmt"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// Stage processes one message: it may drop it (return pass=false) and/or
// mutate it in place (transforms do this; filters/rate-limit/dedup/schema
// never mutate, only drop).
type Stage interface {
	Process(msg *message.Message) (pass bool, err error)
}

// New builds a Stage from a spec.StageSpec, dispatching on Kind (and Type
// for Kind=="filter").
func New(cfg spec.StageSpec) (Stage, error) {
	switch cfg.Kind {
	case "python":
		if err:=cfg.Python.Validate();err!=nil{return nil,err}
		return &pythonStage{ref:cfg.Python},nil
	case "filter":
		switch cfg.Type {
		case "cel":
			return newCEL(cfg.Expression)
		case "regex":
			return newRegex(cfg)
		default:
			return nil, fmt.Errorf("filter: unknown filter type %q", cfg.Type)
		}
	case "transform":
		return newTransform(cfg)
	case "rate-limit":
		return newRateLimit(cfg)
	case "dedup":
		return newDedup(cfg)
	case "schema":
		return newSchema(cfg)
	default:
		return nil, fmt.Errorf("filter: unknown stage kind %q", cfg.Kind)
	}
}

// chainMember pairs a stage with the ID it was declared under, so Chain.Pass
// can attribute a drop to a specific stage.
type chainMember struct {
	id string
	s  Stage
}

// Chain evaluates an ordered sequence of stages with AND semantics: the
// first stage to reject a message short-circuits the rest of the chain.
type Chain struct {
	members []chainMember
}

// NewChain validates the given specs (non-empty, unique IDs) and builds a
// Chain from them. A nil or empty specs slice yields a valid Chain whose
// Pass always returns (true, "", nil).
func NewChain(specs []spec.StageSpec) (*Chain, error) {
	seen := make(map[string]struct{}, len(specs))
	members := make([]chainMember, 0, len(specs))
	for i, s := range specs {
		if s.ID == "" {
			return nil, fmt.Errorf("filter: spec at index %d has empty id", i)
		}
		if _, dup := seen[s.ID]; dup {
			return nil, fmt.Errorf("filter: duplicate stage id %q at index %d", s.ID, i)
		}
		seen[s.ID] = struct{}{}

		st, err := New(s)
		if err != nil {
			return nil, fmt.Errorf("filter: id %q: %w", s.ID, err)
		}
		members = append(members, chainMember{id: s.ID, s: st})
	}
	return &Chain{members: members}, nil
}

// Pass evaluates the chain in order. If a member returns pass=false, Pass
// short-circuits and returns (false, member.id, err) immediately. Errors
// from members do not stop the chain (fail-open, matching each stage's own
// behavior) — the first error encountered is remembered and returned
// wrapped with the offending member's id, even if the chain ultimately
// passes.
func (c *Chain) Pass(msg *message.Message) (pass bool, droppedBy string, err error) {
	var firstErr error
	for _, m := range c.members {
		ok, perr := m.s.Process(msg)
		if perr != nil && firstErr == nil {
			firstErr = fmt.Errorf("stage %q: %w", m.id, perr)
		}
		if !ok {
			return false, m.id, firstErr
		}
	}
	return true, "", firstErr
}
