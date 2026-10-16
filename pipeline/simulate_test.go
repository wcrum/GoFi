package pipeline

import (
	"strings"
	"testing"

	"github.com/wcrum/gofi/spec"
)

func TestSimulate_JSONParsePopulatesFields(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
	}
	res, err := Simulate(def, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if res.ParseError != "" {
		t.Fatalf("unexpected ParseError: %q", res.ParseError)
	}
	if got := res.ParsedFields["a"]; got != "x" {
		t.Fatalf("ParsedFields[a] = %v, want x", got)
	}
	if res.Dropped {
		t.Fatal("Dropped = true, want false")
	}
}

func TestSimulate_ParseErrorFailsOpen(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
	}
	res, err := Simulate(def, []byte(`{not valid json`))
	if err != nil {
		t.Fatalf("Simulate should not return an error on parse failure: %v", err)
	}
	if res.ParseError == "" {
		t.Fatal("expected a ParseError, got none")
	}
	if res == nil {
		t.Fatal("expected a result even on parse error")
	}
}

func TestSimulate_AllPassChain_TransformVisibleToLaterStage(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "t1", Kind: "transform", TransformOp: "set", Field: "added", ValueExpr: `"yes"`},
			{ID: "f1", Kind: "filter", Type: "cel", Expression: `fields["added"] == "yes"`},
		},
	}
	res, err := Simulate(def, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if res.Dropped {
		t.Fatalf("Dropped = true (DroppedBy=%q), want false", res.DroppedBy)
	}
	if len(res.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(res.Stages))
	}
	// The transform's mutation must be visible in the LATER filter stage's snapshot.
	if got := res.Stages[1].Fields["added"]; got != "yes" {
		t.Fatalf("later stage snapshot fields[added] = %v, want yes", got)
	}
	if !res.Stages[1].Pass {
		t.Fatal("filter stage should pass")
	}
}

func TestSimulate_DropMidChain_ShortCircuits(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "f1", Kind: "filter", Type: "cel", Expression: `true`},
			{ID: "f2", Kind: "filter", Type: "cel", Expression: `false`},
			{ID: "f3", Kind: "filter", Type: "cel", Expression: `true`},
		},
	}
	res, err := Simulate(def, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if !res.Dropped {
		t.Fatal("Dropped = false, want true")
	}
	if res.DroppedBy != "f2" {
		t.Fatalf("DroppedBy = %q, want f2", res.DroppedBy)
	}
	// f3 must be absent (short-circuit).
	if len(res.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2 (f3 should be absent)", len(res.Stages))
	}
}

func TestSimulate_DeadLetterFlag(t *testing.T) {
	dropStages := []spec.StageSpec{
		{ID: "f1", Kind: "filter", Type: "cel", Expression: `false`},
	}

	// Dropped + dead-letter configured → DeadLetter true.
	withDL := spec.FlowDefinition{
		Name:           "sim",
		Parser:         spec.ParserCfg{Format: "json"},
		Stages:         dropStages,
		DeadLetterSink: &spec.SinkCfg{Type: "stdout"},
	}
	res, err := Simulate(withDL, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if !res.Dropped || !res.DeadLetter {
		t.Fatalf("Dropped=%v DeadLetter=%v, want true/true", res.Dropped, res.DeadLetter)
	}

	// Dropped but no dead-letter → DeadLetter false.
	noDL := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
		Stages: dropStages,
	}
	res, err = Simulate(noDL, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if !res.Dropped || res.DeadLetter {
		t.Fatalf("Dropped=%v DeadLetter=%v, want true/false", res.Dropped, res.DeadLetter)
	}

	// Not dropped → DeadLetter false (zero value).
	passDef := spec.FlowDefinition{
		Name:           "sim",
		Parser:         spec.ParserCfg{Format: "json"},
		Stages:         []spec.StageSpec{{ID: "f1", Kind: "filter", Type: "cel", Expression: `true`}},
		DeadLetterSink: &spec.SinkCfg{Type: "stdout"},
	}
	res, err = Simulate(passDef, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if res.Dropped || res.DeadLetter {
		t.Fatalf("Dropped=%v DeadLetter=%v, want false/false", res.Dropped, res.DeadLetter)
	}
}

func TestSimulate_SinkWhenSemantics_NoDispatch(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
		Sinks: []spec.SinkCfg{
			// When true → sent.
			{Type: "stdout", When: `fields["a"] == "x"`},
			// When false → not sent, reason "when: false".
			{Type: "stdout", When: `fields["a"] == "nope"`},
			// When references a missing field → eval error.
			{Type: "stdout", When: `fields["missing"] == "y"`},
			// Unreachable NATS URL: if Simulate ever called Send, this would
			// hang/error. It never does, so this must be a clean sent=true.
			{Type: "nats", URL: "nats://192.0.2.1:4222"},
		},
	}
	res, err := Simulate(def, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if len(res.Sinks) != 4 {
		t.Fatalf("len(Sinks) = %d, want 4", len(res.Sinks))
	}
	if !res.Sinks[0].Sent {
		t.Fatalf("sink[0] Sent = false, want true (reason=%q)", res.Sinks[0].Reason)
	}
	if res.Sinks[1].Sent || res.Sinks[1].Reason != "when: false" {
		t.Fatalf("sink[1] Sent=%v Reason=%q, want false/\"when: false\"", res.Sinks[1].Sent, res.Sinks[1].Reason)
	}
	if res.Sinks[2].Sent || !strings.HasPrefix(res.Sinks[2].Reason, "when eval error:") {
		t.Fatalf("sink[2] Sent=%v Reason=%q, want false + eval error prefix", res.Sinks[2].Sent, res.Sinks[2].Reason)
	}
	if !res.Sinks[3].Sent {
		t.Fatalf("sink[3] (unreachable nats) Sent = false, want true (no dispatch); reason=%q", res.Sinks[3].Reason)
	}
}

func TestSimulate_InvalidChain_ReturnsError(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "f1", Kind: "filter", Type: "cel", Expression: `true`},
			{ID: "f1", Kind: "filter", Type: "cel", Expression: `false`},
		},
	}
	if _, err := Simulate(def, []byte(`{"a":"x"}`)); err == nil {
		t.Fatal("expected error for duplicate stage IDs, got nil")
	}
}

func TestSimulate_InvalidParser_ReturnsError(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "bogus"},
	}
	if _, err := Simulate(def, []byte(`{"a":"x"}`)); err == nil {
		t.Fatal("expected error for unknown parser format, got nil")
	}
}

func TestSimulate_RateLimitStage_AlwaysPassesWithNote(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "rl1", Kind: "rate-limit", MaxPerSecond: 1},
		},
	}
	res, err := Simulate(def, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if len(res.Stages) != 1 {
		t.Fatalf("len(Stages) = %d, want 1", len(res.Stages))
	}
	if !res.Stages[0].Pass {
		t.Fatal("rate-limit stage Pass = false, want true (fresh counter can't drop a lone message)")
	}
	if res.Stages[0].Note == "" {
		t.Fatal("rate-limit stage Note is empty, want a caveat about shared listener-wide state")
	}
}

func TestSimulate_DedupStage_AlwaysPassesWithNote(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "d1", Kind: "dedup", KeyExpr: `fields["a"]`},
		},
	}
	res, err := Simulate(def, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if len(res.Stages) != 1 {
		t.Fatalf("len(Stages) = %d, want 1", len(res.Stages))
	}
	if !res.Stages[0].Pass {
		t.Fatal("dedup stage Pass = false, want true (fresh seen-map can't drop a lone message)")
	}
	if res.Stages[0].Note == "" {
		t.Fatal("dedup stage Note is empty, want a caveat about shared listener-wide state")
	}
}

func TestSimulate_FilterTransformSchema_NoteEmpty(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
		Stages: []spec.StageSpec{
			{ID: "f1", Kind: "filter", Type: "cel", Expression: `true`},
			{ID: "t1", Kind: "transform", TransformOp: "set", Field: "added", ValueExpr: `"yes"`},
			{ID: "s1", Kind: "schema", RequiredFields: []string{"a"}},
		},
	}
	res, err := Simulate(def, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if len(res.Stages) != 3 {
		t.Fatalf("len(Stages) = %d, want 3", len(res.Stages))
	}
	for _, st := range res.Stages {
		if st.Note != "" {
			t.Fatalf("stage %q (kind=%q) Note = %q, want empty", st.ID, st.Kind, st.Note)
		}
	}
}

func TestSimulate_EmptyStagesAndSinks(t *testing.T) {
	def := spec.FlowDefinition{
		Name:   "sim",
		Parser: spec.ParserCfg{Format: "json"},
	}
	res, err := Simulate(def, []byte(`{"a":"x"}`))
	if err != nil {
		t.Fatalf("Simulate error: %v", err)
	}
	if res.Dropped {
		t.Fatal("Dropped = true, want false")
	}
	if len(res.Stages) != 0 {
		t.Fatalf("len(Stages) = %d, want 0", len(res.Stages))
	}
	if len(res.Sinks) != 0 {
		t.Fatalf("len(Sinks) = %d, want 0", len(res.Sinks))
	}
}
