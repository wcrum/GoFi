package filter

import (
	"context"
	"strings"
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestCELCostLimitStopsRunawayEvaluation(t *testing.T) {
	prog, err := CompilePredicate(`fields.items.all(x, fields.items.all(y, x == y || true))`)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]interface{}, 2000)
	for i := range items {
		items[i] = float64(i)
	}
	msg := message.New(nil, "t", "test")
	msg.Fields["items"] = items
	// Whichever bound trips first (cost budget or deadline) stops it.
	_, err = EvalCELBool(prog, msg)
	if err == nil || !(strings.Contains(err.Error(), "cost") || strings.Contains(err.Error(), "interrupted")) {
		t.Fatalf("expected cost or deadline interruption, got %v", err)
	}
}

func TestCELCostLimitAppliesToLargeInputs(t *testing.T) {
	// String operations are costed by input size; a huge untrusted frame
	// pushed through repeated scans exceeds the budget deterministically.
	prog, err := CompilePredicate(`[1,2,3,4,5,6,7,8,9,10].all(i, raw.contains("needle") || raw.size() > 0)`)
	if err != nil {
		t.Fatal(err)
	}
	msg := message.New([]byte(strings.Repeat("a", 2_000_000)), "t", "test")
	_, err = EvalCELBool(prog, msg)
	if err == nil || !strings.Contains(err.Error(), "cost") {
		t.Fatalf("expected cost limit error, got %v", err)
	}
}

func TestCELExpressionSizeLimit(t *testing.T) {
	if _, err := CompileCEL(strings.Repeat("1 + ", MaxExpressionBytes) + "1"); err == nil {
		t.Fatal("expected oversized expression to be rejected")
	}
}

func TestEvalCELDetailedReportsCost(t *testing.T) {
	prog, err := CompileCEL(`fields["n"] + 1.0`)
	if err != nil {
		t.Fatal(err)
	}
	msg := message.New(nil, "t", "test")
	msg.Fields["n"] = 1.0
	val, cost, err := EvalCELDetailed(context.Background(), prog, msg)
	if err != nil || val != 2.0 || cost == 0 {
		t.Fatalf("got val=%v cost=%d err=%v", val, cost, err)
	}
}

func TestOnErrorDropTurnsEvaluationErrorsIntoDrops(t *testing.T) {
	msg := message.New(nil, "t", "test") // fields["missing"] raises "no such key"
	base := spec.StageSpec{ID: "f", Kind: "filter", Type: "cel", Expression: `fields["missing"] == "x"`}
	pass, err := mustStage(t, base).Process(msg)
	if !pass || err == nil {
		t.Fatalf("default pass: got pass=%v err=%v", pass, err)
	}
	base.OnError = "drop"
	pass, err = mustStage(t, base).Process(msg)
	if pass || err == nil {
		t.Fatalf("drop: got pass=%v err=%v", pass, err)
	}
	dedup := spec.StageSpec{ID: "d", Kind: "dedup", KeyExpr: `fields["missing"]`, OnError: "drop"}
	if pass, _ = mustStage(t, dedup).Process(msg); pass {
		t.Fatal("dedup drop: expected drop on eval error")
	}
	set := spec.StageSpec{ID: "s", Kind: "transform", TransformOp: "set", Field: "y", ValueExpr: `fields["missing"]`, OnError: "drop"}
	if pass, _ = mustStage(t, set).Process(msg); pass {
		t.Fatal("transform drop: expected drop on eval error")
	}
	ok := spec.StageSpec{ID: "ok", Kind: "filter", Type: "cel", Expression: `true`, OnError: "drop"}
	if pass, err = mustStage(t, ok).Process(msg); !pass || err != nil {
		t.Fatalf("drop wrapper must not change successful results: pass=%v err=%v", pass, err)
	}
}

func TestOnErrorValidation(t *testing.T) {
	if _, err := New(spec.StageSpec{ID: "f", Kind: "filter", Type: "cel", Expression: "true", OnError: "explode"}); err == nil {
		t.Fatal("expected unknown onError to be rejected")
	}
	if _, err := New(spec.StageSpec{ID: "s", Kind: "schema", RequiredFields: []string{"a"}, OnError: "drop"}); err == nil {
		t.Fatal("expected onError drop on a non-CEL stage to be rejected")
	}
	if _, err := New(spec.StageSpec{ID: "s", Kind: "schema", RequiredFields: []string{"a"}, OnError: "pass"}); err != nil {
		t.Fatalf("pass is always allowed: %v", err)
	}
}

func TestCheckReportsLocatedDiagnosticsAndTypes(t *testing.T) {
	out, diags := Check(`fields["a"] ==`, ResultTypeBool)
	if len(diags) == 0 || diags[0].Line != 1 || diags[0].Column < 1 {
		t.Fatalf("expected located syntax diagnostic, got %+v (%s)", diags, out)
	}
	if _, diags = Check(`"text"`, ResultTypeBool); len(diags) == 0 {
		t.Fatal("expected bool result type mismatch")
	}
	if out, diags = Check(`"text"`, ResultTypeAny); len(diags) != 0 || out != "string" {
		t.Fatalf("got %q %+v", out, diags)
	}
}

func TestDescribeListsVariablesAndNamedFunctions(t *testing.T) {
	vars, fns, err := Describe()
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 3 || vars[0].Name != "fields" || vars[0].Description == "" {
		t.Fatalf("variables: %+v", vars)
	}
	found := false
	for _, fn := range fns {
		if strings.HasPrefix(fn.Name, "_") {
			t.Fatalf("operator leaked into functions: %s", fn.Name)
		}
		if fn.Name == "startsWith" {
			found = len(fn.Signatures) > 0 && strings.Contains(fn.Signatures[0], "string.startsWith(")
		}
	}
	if !found {
		t.Fatal("startsWith with a member signature not described")
	}
}

func mustStage(t *testing.T, cfg spec.StageSpec) Stage {
	t.Helper()
	st, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
