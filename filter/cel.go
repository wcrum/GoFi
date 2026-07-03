// Package filter evaluates CEL expressions against parsed messages.
// The CEL environment exposes two variables:
//
//	fields  — map(string, dyn)  — parser-populated key/value pairs
//	meta    — map(string, string) — pipeline metadata (name, source, ts)
//
// Example expressions:
//
//	fields["type"] == "a-f-G"
//	double(fields["lat"]) > 30.0
//	meta["source"].startsWith("10.")
package filter

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"

	"github.com/cnak-us/gogo/message"
)

// Filter compiles and caches CEL programs for a single expression.
type Filter struct {
	expr    string
	env     *cel.Env
	mu      sync.RWMutex
	program cel.Program
}

var sharedEnv *cel.Env
var envOnce sync.Once
var envErr error

func getEnv() (*cel.Env, error) {
	envOnce.Do(func() {
		sharedEnv, envErr = cel.NewEnv(
			cel.Variable("fields", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("meta", cel.MapType(cel.StringType, cel.StringType)),
			cel.Variable("raw", cel.StringType),
		)
	})
	return sharedEnv, envErr
}

// New creates a Filter for the given CEL expression and validates it compiles.
func New(expression string) (*Filter, error) {
	env, err := getEnv()
	if err != nil {
		return nil, fmt.Errorf("cel: env init: %w", err)
	}
	f := &Filter{expr: expression, env: env}
	if _, err := f.compile(); err != nil {
		return nil, fmt.Errorf("cel: compile %q: %w", expression, err)
	}
	return f, nil
}

func (f *Filter) compile() (cel.Program, error) {
	f.mu.RLock()
	if f.program != nil {
		p := f.program
		f.mu.RUnlock()
		return p, nil
	}
	f.mu.RUnlock()

	ast, issues := f.env.Compile(f.expr)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	prog, err := f.env.Program(ast)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.program = prog
	f.mu.Unlock()
	return prog, nil
}

// Pass returns true if the message passes the filter (expression evaluates to true).
// On evaluation error, the message is passed and the error is returned for logging.
func (f *Filter) Pass(msg *message.Message) (bool, error) {
	prog, err := f.compile()
	if err != nil {
		return true, err
	}

	activation := map[string]interface{}{
		"fields": msg.Fields,
		"meta":   msg.Meta,
		"raw":    string(msg.Raw),
	}

	out, _, err := prog.Eval(activation)
	if err != nil {
		return true, fmt.Errorf("cel: eval: %w", err)
	}
	if out == nil {
		return true, nil
	}
	result, ok := out.Value().(bool)
	if !ok {
		return true, fmt.Errorf("cel: expression did not return bool")
	}
	return result, nil
}
