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
	"context"
	"fmt"
	"sync"
	"time"

	"cel.dev/cel-go/cel"

	"github.com/wcrum/gofi/message"
)

// Runtime bounds for every CEL program. Ingress data is untrusted, so each
// evaluation is limited by an estimated-cost budget, interrupted on deadline,
// and expressions themselves have a maximum size.
const (
	// CostLimit matches the per-expression budget Kubernetes applies to CEL
	// validation rules; a typical predicate costs well under 100.
	CostLimit uint64 = 1_000_000
	// MaxExpressionBytes bounds expression source size.
	MaxExpressionBytes = 8192
	// EvalTimeout bounds wall-clock time for one evaluation.
	EvalTimeout = 100 * time.Millisecond
	// interruptCheckFrequency is how many comprehension iterations run
	// between deadline checks.
	interruptCheckFrequency = 100
)

var sharedEnv *cel.Env
var envOnce sync.Once
var envErr error

// Env returns the shared CEL environment (built once, lazily). Every stage,
// sink condition, library check, and library evaluation uses it.
func Env() (*cel.Env, error) {
	envOnce.Do(func() {
		sharedEnv, envErr = cel.NewEnv(
			cel.Variable("fields", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("meta", cel.MapType(cel.StringType, cel.StringType)),
			cel.Variable("raw", cel.StringType),
			cel.ParserExpressionSizeLimit(MaxExpressionBytes),
		)
	})
	return sharedEnv, envErr
}

// CompileCEL compiles a CEL expression against the shared Env and returns a
// ready-to-evaluate Program. Callers should compile once at construction
// time and reuse the returned Program across many Eval calls.
func CompileCEL(expression string) (cel.Program, error) {
	return compileCEL(expression, false)
}

// CompilePredicate rejects known non-boolean expressions during configuration.
// Dynamically typed field lookups remain valid and are checked per message.
func CompilePredicate(expression string) (cel.Program, error) { return compileCEL(expression, true) }

func compileCEL(expression string, predicate bool) (cel.Program, error) {
	if len(expression) > MaxExpressionBytes {
		return nil, fmt.Errorf("cel: expression exceeds %d bytes", MaxExpressionBytes)
	}
	env, err := Env()
	if err != nil {
		return nil, fmt.Errorf("cel: env init: %w", err)
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	if predicate && !ast.OutputType().IsExactType(cel.BoolType) && !ast.OutputType().IsExactType(cel.DynType) {
		return nil, fmt.Errorf("cel: condition must return a bool")
	}
	prog, err := env.Program(ast, cel.CostLimit(CostLimit), cel.InterruptCheckFrequency(interruptCheckFrequency))
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
	val, _, err := EvalCELDetailed(context.Background(), program, msg)
	return val, err
}

// EvalCELDetailed evaluates under the shared deadline and also reports the
// actual evaluation cost.
func EvalCELDetailed(ctx context.Context, program cel.Program, msg *message.Message) (interface{}, uint64, error) {
	activation := map[string]interface{}{
		"fields": msg.Fields,
		"meta":   msg.Meta,
		"raw":    string(msg.Raw),
	}
	ctx, cancel := context.WithTimeout(ctx, EvalTimeout)
	defer cancel()
	out, details, err := program.ContextEval(ctx, activation)
	var cost uint64
	if details != nil && details.ActualCost() != nil {
		cost = *details.ActualCost()
	}
	if err != nil {
		return nil, cost, fmt.Errorf("cel: eval: %w", err)
	}
	if out == nil {
		return nil, cost, nil
	}
	return out.Value(), cost, nil
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

	prog, err := CompilePredicate(f.expr)
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
