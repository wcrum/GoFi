// Package expressions stores the CEL expression library and resolves the
// library references (spec.ExpressionRef) that stages and sinks link to.
// Entries are linked, never copied: the controller resolves references to
// expression text whenever it builds a listener, so an edit reaches every
// listener that follows the latest generation, and pinned listeners keep the
// revision they pinned.
package expressions

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/spec"
)

// Expression is one library entry. Generation is server-managed.
type Expression struct {
	Name        string     `json:"name,omitempty"`
	Description string     `json:"description,omitempty"`
	Expression  string     `json:"expression"`
	Tags        []string   `json:"tags,omitempty"`
	ResultType  string     `json:"resultType,omitempty"`
	Generation  int64      `json:"generation,omitempty"`
	Revisions   []Revision `json:"revisions,omitempty"`
}

// Revision is a previous generation retained because a listener pins it.
type Revision struct {
	Generation int64  `json:"generation"`
	Expression string `json:"expression"`
}

// Store persists library entries (file in binary/Docker mode, CRDs in
// Kubernetes), mirroring sandbox.Store.
type Store interface {
	Load(context.Context) ([]Expression, error)
	Save(context.Context, Expression) error
	Delete(context.Context, string) error
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// Normalize validates the stored shape and applies defaults. It does not
// compile the expression; Validate does.
func Normalize(e Expression) (Expression, error) {
	if !namePattern.MatchString(e.Name) {
		return e, fmt.Errorf("expression name must be 1-63 lowercase letters, numbers, or hyphens, starting with a letter")
	}
	if strings.TrimSpace(e.Expression) == "" {
		return e, fmt.Errorf("expression is required")
	}
	if len(e.Expression) > filter.MaxExpressionBytes {
		return e, fmt.Errorf("expression exceeds %d bytes", filter.MaxExpressionBytes)
	}
	if len(e.Description) > 1024 {
		return e, fmt.Errorf("description exceeds 1024 characters")
	}
	if e.ResultType == "" {
		e.ResultType = filter.ResultTypeBool
	}
	if !filter.ValidResultType(e.ResultType) {
		return e, fmt.Errorf("resultType must be %q or %q", filter.ResultTypeBool, filter.ResultTypeAny)
	}
	seen := map[string]bool{}
	tags := []string{}
	for _, tag := range e.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		if len(tag) > 64 {
			return e, fmt.Errorf("tag %q exceeds 64 characters", tag)
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	if len(tags) > 32 {
		return e, fmt.Errorf("at most 32 tags are allowed")
	}
	sort.Strings(tags)
	e.Tags = tags
	return e, nil
}

// Validate normalizes e and compiles it for its result type.
func Validate(e Expression) (Expression, error) {
	e, err := Normalize(e)
	if err != nil {
		return e, err
	}
	if _, err := filter.CompileFor(e.Expression, e.ResultType); err != nil {
		return e, fmt.Errorf("expression %q: %w", e.Name, err)
	}
	return e, nil
}

// Library is an immutable-by-convention snapshot keyed by name.
type Library map[string]Expression

// Lookup returns the text for ref: generation 0 (or the current generation)
// resolves to the current text; any other generation must be retained.
func (l Library) Lookup(ref spec.ExpressionRef) (text, resultType string, err error) {
	e, ok := l[ref.Name]
	if !ok {
		return "", "", fmt.Errorf("CEL library expression %q does not exist", ref.Name)
	}
	if ref.Generation == 0 || ref.Generation == e.Generation {
		return e.Expression, e.ResultType, nil
	}
	for _, r := range e.Revisions {
		if r.Generation == ref.Generation {
			return r.Expression, e.ResultType, nil
		}
	}
	return "", "", fmt.Errorf("CEL library expression %q has no generation %d", ref.Name, ref.Generation)
}

// With returns a copy of l with e set (used to test an Apply before saving).
func (l Library) With(e Expression) Library {
	out := make(Library, len(l)+1)
	for k, v := range l {
		out[k] = v
	}
	out[e.Name] = e
	return out
}

// Reference is one library link inside a listener definition.
type Reference struct {
	spec.ExpressionRef
	Where string
}

// References lists every library link in def.
func References(def spec.FlowDefinition) []Reference {
	var out []Reference
	for _, s := range def.Stages {
		if s.ExpressionRef != nil {
			out = append(out, Reference{*s.ExpressionRef, "stage " + s.ID})
		}
	}
	for i, s := range def.Sinks {
		if s.WhenRef != nil {
			out = append(out, Reference{*s.WhenRef, fmt.Sprintf("sink %d", i)})
		}
	}
	if def.DeadLetterSink != nil && def.DeadLetterSink.WhenRef != nil {
		out = append(out, Reference{*def.DeadLetterSink.WhenRef, "dead-letter sink"})
	}
	return out
}

// Uses reports whether def references name, and whether any reference
// follows the latest generation (generation 0 or the given current one).
func Uses(def spec.FlowDefinition, name string, current int64) (uses, follows bool) {
	for _, r := range References(def) {
		if r.Name != name {
			continue
		}
		uses = true
		if r.Generation == 0 || r.Generation == current {
			follows = true
		}
	}
	return uses, follows
}

// Resolve returns a copy of def in which every library reference has been
// replaced by expression text. A literal expression on the same stage or
// sink takes precedence and the reference is left unresolved. References
// stay in place so the resolved definition still records its links.
func Resolve(def spec.FlowDefinition, lib Library) (spec.FlowDefinition, error) {
	if len(References(def)) == 0 {
		return def, nil
	}
	out := def
	out.Stages = append([]spec.StageSpec(nil), def.Stages...)
	for i := range out.Stages {
		s := &out.Stages[i]
		if s.ExpressionRef == nil {
			continue
		}
		var target *string
		needBool := false
		switch {
		case s.Kind == "filter" && s.Type == "cel":
			target, needBool = &s.Expression, true
		case s.Kind == "transform" && s.TransformOp == "set":
			target = &s.ValueExpr
		case s.Kind == "dedup":
			target = &s.KeyExpr
		default:
			return def, fmt.Errorf("stage %q: expressionRef applies only to CEL filter, transform set, and dedup stages", s.ID)
		}
		if *target != "" {
			continue
		}
		text, resultType, err := lib.Lookup(*s.ExpressionRef)
		if err != nil {
			return def, fmt.Errorf("stage %q: %w", s.ID, err)
		}
		if needBool && resultType != filter.ResultTypeBool {
			return def, fmt.Errorf("stage %q: CEL filter requires a bool library expression, but %q returns %s", s.ID, s.ExpressionRef.Name, resultType)
		}
		*target = text
	}
	out.Sinks = append([]spec.SinkCfg(nil), def.Sinks...)
	for i := range out.Sinks {
		if err := resolveWhen(&out.Sinks[i], lib); err != nil {
			return def, fmt.Errorf("sink %d: %w", i, err)
		}
	}
	if def.DeadLetterSink != nil {
		dl := *def.DeadLetterSink
		if err := resolveWhen(&dl, lib); err != nil {
			return def, fmt.Errorf("dead-letter sink: %w", err)
		}
		out.DeadLetterSink = &dl
	}
	return out, nil
}

func resolveWhen(s *spec.SinkCfg, lib Library) error {
	if s.WhenRef == nil || s.When != "" {
		return nil
	}
	text, resultType, err := lib.Lookup(*s.WhenRef)
	if err != nil {
		return err
	}
	if resultType != filter.ResultTypeBool {
		return fmt.Errorf("sink conditions require a bool library expression, but %q returns %s", s.WhenRef.Name, resultType)
	}
	s.When = text
	return nil
}

// Manager caches the last successfully loaded library. Snapshot reloads the
// store so declarative edits (file or CRD) are picked up on reconcile.
type Manager struct {
	store Store
	mu    sync.RWMutex
	cache Library
	// Ops serializes read-modify-write operations (Apply/Delete).
	Ops sync.Mutex
}

func NewManager(store Store) *Manager { return &Manager{store: store, cache: Library{}} }

// Snapshot loads the store. On failure the last good snapshot is returned
// with the error so callers can keep running listeners unchanged.
func (m *Manager) Snapshot(ctx context.Context) (Library, error) {
	defs, err := m.store.Load(ctx)
	if err != nil {
		return m.Cached(), err
	}
	lib := make(Library, len(defs))
	for _, d := range defs {
		lib[d.Name] = d
	}
	m.mu.Lock()
	m.cache = lib
	m.mu.Unlock()
	return lib, nil
}

// Cached returns the most recent snapshot without touching the store.
func (m *Manager) Cached() Library {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cache
}

func (m *Manager) Save(ctx context.Context, e Expression) error {
	if err := m.store.Save(ctx, e); err != nil {
		return err
	}
	m.mu.Lock()
	m.cache = m.cache.With(e)
	m.mu.Unlock()
	return nil
}

func (m *Manager) Delete(ctx context.Context, name string) error {
	if err := m.store.Delete(ctx, name); err != nil {
		return err
	}
	m.mu.Lock()
	next := make(Library, len(m.cache))
	for k, v := range m.cache {
		if k != name {
			next[k] = v
		}
	}
	m.cache = next
	m.mu.Unlock()
	return nil
}
