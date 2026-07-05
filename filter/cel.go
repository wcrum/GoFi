// Package filter evaluates CEL expressions against parsed messages.
// The CEL environment exposes three variables:
//
//	fields  — map(string, dyn)    — parser-populated key/value pairs
//	meta    — map(string, string) — pipeline metadata (name, source, ts)
//	raw     — string              — the raw frame bytes
//
// Example expressions:
//
//	fields["type"] == "a-f-G"
//	double(fields["lat"]) > 30.0
//	meta["source"].startsWith("10.")
//
// This same environment/compile/eval machinery is reused by the "filter"
// stage (bool result), the "transform" stage's ValueExpr (any result), the
// "dedup" stage's KeyExpr (any result, formatted to a string), and sink
// `when` conditions in the pipeline package (bool result) — see CompileCEL,
// EvalCEL, and EvalCELBool below, which are the shared building blocks.
package filter

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"

	"github.com/wcrum/gofi/message"
)

var sharedEnv *cel.Env
var envOnce sync.Once
var envErr error

// Env returns the shared CEL environment (built once, lazily).
func Env() (*cel.Env, error) {
	envOnce.Do(func() {
		sharedEnv, envErr = cel.NewEnv(
			cel.Variable("fields", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("meta", cel.MapType(cel.StringType, cel.StringType)),
			cel.Variable("raw", cel.StringType),
		)
	})
	return sharedEnv, envErr
}

// CompileCEL compiles a CEL expression against the shared Env and returns a
// ready-to-evaluate Program. Callers should compile once at construction
// time and reuse the returned Program across many Eval calls.
func CompileCEL(expression string) (cel.Program, error) {
	env, err := Env()
	if err != nil {
		return nil, fmt.Errorf("cel: env init: %w", err)
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	prog, err := env.Program(ast)
	if err != nil {
		return nil, err
	}
	return prog, nil
}

// EvalCEL evaluates a compiled program against msg's fields/meta/raw and
// returns the raw result value (nil if the expression evaluated to CEL's
// null). Callers interpret the result themselves (e.g. as a bool, or via
// fmt.Sprint for a dedup key).
func EvalCEL(program cel.Program, msg *message.Message) (interface{}, error) {
	activation := map[string]interface{}{
		"fields": msg.Fields,
		"meta":   msg.Meta,
		"raw":    string(msg.Raw),
	}
	out, _, err := program.Eval(activation)
	if err != nil {
		return nil, fmt.Errorf("cel: eval: %w", err)
	}
	if out == nil {
		return nil, nil
	}
	return out.Value(), nil
}

// EvalCELBool evaluates a compiled program and requires a bool result (nil
// output is treated as false-not-an-error is left to the caller — here nil
// is reported as an error since bool-typed callers need an explicit true/false).
func EvalCELBool(program cel.Program, msg *message.Message) (bool, error) {
	val, err := EvalCEL(program, msg)
	if err != nil {
		return false, err
	}
	if val == nil {
		return false, fmt.Errorf("cel: expression did not return bool (got nil)")
	}
	result, ok := val.(bool)
	if !ok {
		return false, fmt.Errorf("cel: expression did not return bool")
	}
	return result, nil
}

// celFilter compiles and caches a CEL program for a single expression.
type celFilter struct {
	expr    string
	mu      sync.RWMutex
	program cel.Program
}

// newCEL creates a celFilter for the given CEL expression and validates it compiles.
func newCEL(expression string) (*celFilter, error) {
	f := &celFilter{expr: expression}
	if _, err := f.compile(); err != nil {
		return nil, fmt.Errorf("cel: compile %q: %w", expression, err)
	}
	return f, nil
}

func (f *celFilter) compile() (cel.Program, error) {
	f.mu.RLock()
	if f.program != nil {
		p := f.program
		f.mu.RUnlock()
		return p, nil
	}
	f.mu.RUnlock()

	prog, err := CompileCEL(f.expr)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.program = prog
	f.mu.Unlock()
	return prog, nil
}

// Process returns true if the message passes the filter (expression evaluates to true).
// On evaluation error, the message is passed and the error is returned for logging.
func (f *celFilter) Process(msg *message.Message) (bool, error) {
	prog, err := f.compile()
	if err != nil {
		return true, err
	}

	val, err := EvalCEL(prog, msg)
	if err != nil {
		return true, err
	}
	if val == nil {
		return true, nil
	}
	result, ok := val.(bool)
	if !ok {
		return true, fmt.Errorf("cel: expression did not return bool")
	}
	return result, nil
}
