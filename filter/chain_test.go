package filter

import (
	"errors"
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func newMsg() *message.Message {
	return message.New([]byte(`{}`), "test-listener", "127.0.0.1:1234")
}

func TestNew_UnknownKind(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "f1", Kind: "nope"})
	if err == nil {
		t.Fatal("expected error for unknown stage kind, got nil")
	}
}

func TestNew_UnknownFilterType(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "f1", Kind: "filter", Type: "nope"})
	if err == nil {
		t.Fatal("expected error for unknown filter type, got nil")
	}
}

func TestNew_EmptyFilterType(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "f1", Kind: "filter", Type: ""})
	if err == nil {
		t.Fatal("expected error for empty filter type, got nil")
	}
}

func TestNew_InvalidCELExpression(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "f1", Kind: "filter", Type: "cel", Expression: "fields[ this is not valid cel"})
	if err == nil {
		t.Fatal("expected error for invalid CEL expression, got nil")
	}
}

func TestNewChain_Empty(t *testing.T) {
	for _, specs := range [][]spec.StageSpec{nil, {}} {
		c, err := NewChain(specs)
		if err != nil {
			t.Fatalf("NewChain(%v) error = %v", specs, err)
		}
		pass, droppedBy, err := c.Pass(newMsg())
		if !pass || droppedBy != "" || err != nil {
			t.Fatalf("empty chain Pass() = (%v, %q, %v), want (true, \"\", nil)", pass, droppedBy, err)
		}
	}
}

func TestNewChain_AllPass(t *testing.T) {
	specs := []spec.StageSpec{
		{ID: "f1", Kind: "filter", Type: "cel", Expression: "true"},
		{ID: "f2", Kind: "filter", Type: "cel", Expression: "1 == 1"},
	}
	c, err := NewChain(specs)
	if err != nil {
		t.Fatalf("NewChain error = %v", err)
	}
	pass, droppedBy, err := c.Pass(newMsg())
	if !pass || droppedBy != "" || err != nil {
		t.Fatalf("Pass() = (%v, %q, %v), want (true, \"\", nil)", pass, droppedBy, err)
	}
}

// spyStage records whether Process was invoked and returns a canned result.
type spyStage struct {
	called bool
	pass   bool
	err    error
}

func (s *spyStage) Process(msg *message.Message) (bool, error) {
	s.called = true
	return s.pass, s.err
}

func TestChain_FirstDropsShortCircuits(t *testing.T) {
	spy2 := &spyStage{pass: true}
	c := &Chain{members: []chainMember{
		{id: "f1", s: &spyStage{pass: false}},
		{id: "f2", s: spy2},
	}}

	pass, droppedBy, err := c.Pass(newMsg())
	if pass {
		t.Fatal("expected pass=false")
	}
	if droppedBy != "f1" {
		t.Fatalf("droppedBy = %q, want f1", droppedBy)
	}
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if spy2.called {
		t.Fatal("second stage's Process was invoked despite first stage dropping the message")
	}
}

func TestChain_SecondDrops(t *testing.T) {
	c := &Chain{members: []chainMember{
		{id: "f1", s: &spyStage{pass: true}},
		{id: "f2", s: &spyStage{pass: false}},
	}}

	pass, droppedBy, err := c.Pass(newMsg())
	if pass {
		t.Fatal("expected pass=false")
	}
	if droppedBy != "f2" {
		t.Fatalf("droppedBy = %q, want f2", droppedBy)
	}
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestChain_EvalErrorFailsOpenAndContinues(t *testing.T) {
	// f1 reports an error but still passes (fail-open), and we verify f2 is
	// still evaluated and its result (a drop) is what the chain reports,
	// with the error preserved.
	f1Err := errors.New("boom")
	c := &Chain{members: []chainMember{
		{id: "f1", s: &spyStage{pass: true, err: f1Err}},
		{id: "f2", s: &spyStage{pass: false}},
	}}

	pass, droppedBy, err := c.Pass(newMsg())
	if pass {
		t.Fatal("expected pass=false from f2")
	}
	if droppedBy != "f2" {
		t.Fatalf("droppedBy = %q, want f2", droppedBy)
	}
	if err == nil || !errors.Is(err, f1Err) {
		t.Fatalf("err = %v, want wrapped f1Err", err)
	}
}

func TestChain_EvalErrorFailsOpenAllPass(t *testing.T) {
	f1Err := errors.New("boom")
	c := &Chain{members: []chainMember{
		{id: "f1", s: &spyStage{pass: true, err: f1Err}},
		{id: "f2", s: &spyStage{pass: true}},
	}}

	pass, droppedBy, err := c.Pass(newMsg())
	if !pass {
		t.Fatal("expected pass=true (fail-open)")
	}
	if droppedBy != "" {
		t.Fatalf("droppedBy = %q, want empty", droppedBy)
	}
	if err == nil || !errors.Is(err, f1Err) {
		t.Fatalf("err = %v, want wrapped f1Err", err)
	}
}

func TestNewChain_RealCELEvalErrorContinues(t *testing.T) {
	// f1 references a field that isn't a string, causing an eval-time type
	// error (double() on a missing key errors, but the filter still fails open).
	specs := []spec.StageSpec{
		{ID: "f1", Kind: "filter", Type: "cel", Expression: `double(fields["missing"]) > 1.0`},
		{ID: "f2", Kind: "filter", Type: "cel", Expression: "false"},
	}
	c, err := NewChain(specs)
	if err != nil {
		t.Fatalf("NewChain error = %v", err)
	}
	pass, droppedBy, err := c.Pass(newMsg())
	if pass {
		t.Fatal("expected pass=false from f2")
	}
	if droppedBy != "f2" {
		t.Fatalf("droppedBy = %q, want f2", droppedBy)
	}
	if err == nil {
		t.Fatal("expected non-nil error from f1's eval failure")
	}
}

func TestNewChain_DuplicateIDs(t *testing.T) {
	specs := []spec.StageSpec{
		{ID: "f1", Kind: "filter", Type: "cel", Expression: "true"},
		{ID: "f1", Kind: "filter", Type: "cel", Expression: "false"},
	}
	_, err := NewChain(specs)
	if err == nil {
		t.Fatal("expected error for duplicate stage IDs, got nil")
	}
}

func TestNewChain_EmptyID(t *testing.T) {
	specs := []spec.StageSpec{
		{ID: "", Kind: "filter", Type: "cel", Expression: "true"},
	}
	_, err := NewChain(specs)
	if err == nil {
		t.Fatal("expected error for empty stage ID, got nil")
	}
}

func TestNewChain_MixedKinds_TransformThenFilterThenSchema(t *testing.T) {
	// transform sets upper_type = fields["type"] + "!", then a filter checks
	// upper_type == "a!" (confirming the transform's mutation is visible to
	// the later filter's CEL expression), then schema requires "val".
	specs := []spec.StageSpec{
		{ID: "t1", Kind: "transform", TransformOp: "set", Field: "upper_type", ValueExpr: `fields["type"] + "!"`},
		{ID: "f1", Kind: "filter", Type: "cel", Expression: `fields["upper_type"] == "a!"`},
		{ID: "s1", Kind: "schema", RequiredFields: []string{"val"}},
	}
	c, err := NewChain(specs)
	if err != nil {
		t.Fatalf("NewChain error = %v", err)
	}

	// Passes all three: type=a produces upper_type="a!" and val is present.
	msg := message.New([]byte(`{}`), "test", "127.0.0.1:1")
	msg.Fields["type"] = "a"
	msg.Fields["val"] = "x"
	pass, droppedBy, err := c.Pass(msg)
	if err != nil {
		t.Fatalf("Pass error = %v", err)
	}
	if !pass {
		t.Fatalf("expected pass=true, got dropped by %q", droppedBy)
	}
	if msg.Fields["upper_type"] != "a!" {
		t.Fatalf("expected transform to set upper_type, got %#v", msg.Fields["upper_type"])
	}

	// type=b -> transform sets upper_type="b!" -> filter drops it.
	msg2 := message.New([]byte(`{}`), "test", "127.0.0.1:1")
	msg2.Fields["type"] = "b"
	msg2.Fields["val"] = "x"
	pass, droppedBy, err = c.Pass(msg2)
	if err != nil {
		t.Fatalf("Pass error = %v", err)
	}
	if pass || droppedBy != "f1" {
		t.Fatalf("expected dropped by f1, got pass=%v droppedBy=%q", pass, droppedBy)
	}

	// type=a but missing val -> passes filter but dropped by schema.
	msg3 := message.New([]byte(`{}`), "test", "127.0.0.1:1")
	msg3.Fields["type"] = "a"
	pass, droppedBy, err = c.Pass(msg3)
	if err != nil {
		t.Fatalf("Pass error = %v", err)
	}
	if pass || droppedBy != "s1" {
		t.Fatalf("expected dropped by s1, got pass=%v droppedBy=%q", pass, droppedBy)
	}
}

// TestNewChain_AllFiveKinds builds a chain exercising all 5 stage kinds in
// one pipeline: transform -> filter -> schema -> rate-limit -> dedup. It
// confirms stages run strictly in declared order against the SAME
// *message.Message (no copying/cloning breaks visibility of an earlier
// stage's mutation to a later stage's CEL expression), and that rate-limit
// and dedup (both listener-wide shared state) correctly gate messages that
// already passed transform/filter/schema.
func TestNewChain_AllFiveKinds(t *testing.T) {
	specs := []spec.StageSpec{
		{ID: "t1", Kind: "transform", TransformOp: "set", Field: "tag", ValueExpr: `fields["type"] + "-tagged"`},
		{ID: "f1", Kind: "filter", Type: "cel", Expression: `fields["tag"] == "a-tagged"`},
		{ID: "s1", Kind: "schema", RequiredFields: []string{"id"}},
		{ID: "r1", Kind: "rate-limit", MaxPerSecond: 1},
		{ID: "d1", Kind: "dedup", KeyExpr: `fields["id"]`, Window: "1s"},
	}
	c, err := NewChain(specs)
	if err != nil {
		t.Fatalf("NewChain error = %v", err)
	}

	// First message: type=a -> tag="a-tagged" passes filter, has id so passes
	// schema, is first-in-window so passes rate-limit, and is first-seen so
	// passes dedup.
	msg1 := message.New([]byte(`{}`), "test", "127.0.0.1:1")
	msg1.Fields["type"] = "a"
	msg1.Fields["id"] = "abc"
	pass, droppedBy, err := c.Pass(msg1)
	if err != nil {
		t.Fatalf("Pass error = %v", err)
	}
	if !pass {
		t.Fatalf("expected first message to pass all 5 stages, got dropped by %q", droppedBy)
	}
	if msg1.Fields["tag"] != "a-tagged" {
		t.Fatalf("expected transform mutation visible, tag = %#v", msg1.Fields["tag"])
	}

	// Second message with the same id: passes transform/filter/schema, but
	// rate-limit (MaxPerSecond=1) drops it before dedup is ever evaluated.
	msg2 := message.New([]byte(`{}`), "test", "127.0.0.1:1")
	msg2.Fields["type"] = "a"
	msg2.Fields["id"] = "abc"
	pass, droppedBy, err = c.Pass(msg2)
	if err != nil {
		t.Fatalf("Pass error = %v", err)
	}
	if pass || droppedBy != "r1" {
		t.Fatalf("expected second message dropped by rate-limit r1, got pass=%v droppedBy=%q", pass, droppedBy)
	}
}
