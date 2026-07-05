package filter

import (
	"strings"
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// These tests exercise the base CEL filter (newCEL/celFilter.Process) and the
// shared CompileCEL/EvalCEL/EvalCELBool helpers directly, rather than only
// through Chain (see chain_test.go) or the other stage kinds that build on
// top of these same helpers (transform.go, dedup.go).

func TestCELFilter_SimpleBooleanTrueAndFalse(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "c1", Kind: "filter", Type: "cel", Expression: `fields["a"] == "x"`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	msgTrue := message.New(nil, "l", "r")
	msgTrue.Fields["a"] = "x"
	pass, err := st.Process(msgTrue)
	if err != nil || !pass {
		t.Fatalf("matching value: Process = (%v, %v), want (true, nil)", pass, err)
	}

	msgFalse := message.New(nil, "l", "r")
	msgFalse.Fields["a"] = "y"
	pass, err = st.Process(msgFalse)
	if err != nil || pass {
		t.Fatalf("non-matching value: Process = (%v, %v), want (false, nil)", pass, err)
	}
}

func TestCELFilter_NumericComparison(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "c1", Kind: "filter", Type: "cel", Expression: `fields["n"] > 10`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	msg := message.New(nil, "l", "r")
	msg.Fields["n"] = float64(20)
	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("20 > 10: Process = (%v, %v), want (true, nil)", pass, err)
	}

	msg2 := message.New(nil, "l", "r")
	msg2.Fields["n"] = float64(5)
	pass, err = st.Process(msg2)
	if err != nil || pass {
		t.Fatalf("5 > 10: Process = (%v, %v), want (false, nil)", pass, err)
	}
}

func TestCELFilter_CompoundExpressions(t *testing.T) {
	tests := []struct {
		name string
		expr string
		a    string
		n    float64
		want bool
	}{
		{"and both true", `fields["a"] == "x" && fields["n"] > 10`, "x", 20, true},
		{"and one false", `fields["a"] == "x" && fields["n"] > 10`, "x", 5, false},
		{"or one true", `fields["a"] == "x" || fields["n"] > 10`, "z", 20, true},
		{"or both false", `fields["a"] == "x" || fields["n"] > 10`, "z", 5, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, err := New(spec.StageSpec{ID: "c1", Kind: "filter", Type: "cel", Expression: tc.expr})
			if err != nil {
				t.Fatalf("New error = %v", err)
			}
			msg := message.New(nil, "l", "r")
			msg.Fields["a"] = tc.a
			msg.Fields["n"] = tc.n
			pass, err := st.Process(msg)
			if err != nil {
				t.Fatalf("Process error = %v", err)
			}
			if pass != tc.want {
				t.Fatalf("Process = %v, want %v", pass, tc.want)
			}
		})
	}
}

func TestCELFilter_FieldsMapAccess(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "c1", Kind: "filter", Type: "cel", Expression: `fields["a"] == "hello"`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["a"] = "hello"
	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("Process = (%v, %v), want (true, nil)", pass, err)
	}
}

func TestCELFilter_MetaMapAccess(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "c1", Kind: "filter", Type: "cel", Expression: `meta["name"] == "test-listener"`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "test-listener", "127.0.0.1:1")
	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("Process = (%v, %v), want (true, nil)", pass, err)
	}
}

func TestCELFilter_RawStringAccess(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "c1", Kind: "filter", Type: "cel", Expression: `raw == "hello"`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New([]byte("hello"), "l", "r")
	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("Process = (%v, %v), want (true, nil)", pass, err)
	}

	msg2 := message.New([]byte("goodbye"), "l", "r")
	pass, err = st.Process(msg2)
	if err != nil || pass {
		t.Fatalf("Process = (%v, %v), want (false, nil)", pass, err)
	}
}

func TestCELFilter_AllThreeVariablesTogether(t *testing.T) {
	st, err := New(spec.StageSpec{
		ID: "c1", Kind: "filter", Type: "cel",
		Expression: `fields["a"] == "x" && meta["name"] == "l1" && raw == "raw-bytes"`,
	})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New([]byte("raw-bytes"), "l1", "127.0.0.1:1")
	msg.Fields["a"] = "x"
	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("Process = (%v, %v), want (true, nil)", pass, err)
	}
}

// TestCELFilter_JSONFloat64Coercion confirms CEL's dynamic typing correctly
// compares a Go float64 (as produced by parser/json.go for every JSON
// number) against an int literal in the expression.
func TestCELFilter_JSONFloat64Coercion(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "c1", Kind: "filter", Type: "cel", Expression: `fields["n"] > 5`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["n"] = float64(10) // as parser/json.go would produce
	pass, err := st.Process(msg)
	if err != nil {
		t.Fatalf("Process error = %v, want nil (float64 vs int literal should compare cleanly)", err)
	}
	if !pass {
		t.Fatal("expected pass=true: float64(10) > 5")
	}
}

// TestCELFilter_MissingFieldIsEvalError confirms that indexing a
// nonexistent key in the `fields` map is an evaluation-time error (this CEL
// env's map type does not treat a missing key as CEL null), and that
// celFilter.Process fails open: pass=true with a non-nil error, per the
// convention documented on Chain.Pass.
func TestCELFilter_MissingFieldIsEvalError(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "c1", Kind: "filter", Type: "cel", Expression: `fields["nonexistent"] == "x"`})
	if err != nil {
		t.Fatalf("New error = %v, want nil (construction does not evaluate the expression)", err)
	}
	msg := message.New(nil, "l", "r")
	pass, err := st.Process(msg)
	if !pass {
		t.Fatal("expected pass=true (fail-open) on missing-field eval error")
	}
	if err == nil {
		t.Fatal("expected non-nil error for a missing map key")
	}
	if !strings.Contains(err.Error(), "no such key") {
		t.Fatalf("err = %v, want it to mention the missing key", err)
	}
}

// TestCELFilter_NonBoolResult_CompilesButErrorsAtEval confirms that a filter
// expression whose result type isn't bool is NOT rejected at construction
// time (this codebase's CompileCEL does not check env.Compile's inferred
// output type) — the error only surfaces at Process/eval time, and again
// fails open.
func TestCELFilter_NonBoolResult_CompilesButErrorsAtEval(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "c1", Kind: "filter", Type: "cel", Expression: `fields["a"] + fields["b"]`})
	if err != nil {
		t.Fatalf("New error = %v, want nil: non-bool result is not caught at construction time", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["a"] = "x"
	msg.Fields["b"] = "y"
	pass, err := st.Process(msg)
	if !pass {
		t.Fatal("expected pass=true (fail-open) when expression result isn't bool")
	}
	if err == nil {
		t.Fatal("expected non-nil error when expression result isn't bool")
	}
	if !strings.Contains(err.Error(), "did not return bool") {
		t.Fatalf("err = %v, want it to mention the non-bool result", err)
	}
}

// TestNewCEL_CompileTimeErrorRejected confirms a syntactically invalid CEL
// expression is rejected by New (via newCEL) at construction time with an
// error, not a panic.
func TestNewCEL_CompileTimeErrorRejected(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "c1", Kind: "filter", Type: "cel", Expression: `fields[ this is not valid cel (((`})
	if err == nil {
		t.Fatal("expected construction error for syntactically invalid CEL expression, got nil")
	}
}

// --- exported helper coverage: CompileCEL / EvalCEL / EvalCELBool called directly ---

func TestCompileCEL_ValidAndInvalid(t *testing.T) {
	if _, err := CompileCEL(`fields["a"] == "x"`); err != nil {
		t.Fatalf("CompileCEL valid expr error = %v", err)
	}
	if _, err := CompileCEL(`not valid [`); err == nil {
		t.Fatal("expected CompileCEL error for invalid expression, got nil")
	}
}

func TestEvalCEL_ReturnsRawValue(t *testing.T) {
	prog, err := CompileCEL(`fields["n"]`)
	if err != nil {
		t.Fatalf("CompileCEL error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["n"] = float64(42)
	val, err := EvalCEL(prog, msg)
	if err != nil {
		t.Fatalf("EvalCEL error = %v", err)
	}
	if val != float64(42) {
		t.Fatalf("EvalCEL value = %#v, want float64(42)", val)
	}
}

func TestEvalCEL_EvalErrorPropagates(t *testing.T) {
	prog, err := CompileCEL(`fields["missing"]`)
	if err != nil {
		t.Fatalf("CompileCEL error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	_, err = EvalCEL(prog, msg)
	if err == nil {
		t.Fatal("expected EvalCEL error for missing key, got nil")
	}
}

func TestEvalCELBool_TrueFalseAndErrors(t *testing.T) {
	msg := message.New(nil, "l", "r")
	msg.Fields["a"] = "x"

	progTrue, err := CompileCEL(`fields["a"] == "x"`)
	if err != nil {
		t.Fatalf("CompileCEL error = %v", err)
	}
	result, err := EvalCELBool(progTrue, msg)
	if err != nil || !result {
		t.Fatalf("EvalCELBool = (%v, %v), want (true, nil)", result, err)
	}

	progFalse, err := CompileCEL(`fields["a"] == "y"`)
	if err != nil {
		t.Fatalf("CompileCEL error = %v", err)
	}
	result, err = EvalCELBool(progFalse, msg)
	if err != nil || result {
		t.Fatalf("EvalCELBool = (%v, %v), want (false, nil)", result, err)
	}

	// Non-bool result is an error from EvalCELBool.
	progNonBool, err := CompileCEL(`fields["a"]`)
	if err != nil {
		t.Fatalf("CompileCEL error = %v", err)
	}
	_, err = EvalCELBool(progNonBool, msg)
	if err == nil {
		t.Fatal("expected EvalCELBool error for a non-bool result, got nil")
	}

	// Eval error (missing key) surfaces as an error, not a panic.
	progErr, err := CompileCEL(`fields["missing"] == "x"`)
	if err != nil {
		t.Fatalf("CompileCEL error = %v", err)
	}
	_, err = EvalCELBool(progErr, msg)
	if err == nil {
		t.Fatal("expected EvalCELBool error for missing-key eval failure, got nil")
	}
}

func TestEnv_ReturnsSameInstanceEachCall(t *testing.T) {
	env1, err := Env()
	if err != nil {
		t.Fatalf("Env error = %v", err)
	}
	env2, err := Env()
	if err != nil {
		t.Fatalf("Env error = %v", err)
	}
	if env1 != env2 {
		t.Fatal("Env() should return the same lazily-initialized shared instance")
	}
}
