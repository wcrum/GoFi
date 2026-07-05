package filter

import (
	"fmt"
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestNewTransform_UnknownOp(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "nope", Field: "x"})
	if err == nil {
		t.Fatal("expected error for unknown transformOp, got nil")
	}
}

func TestNewTransform_EmptyOp(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "", Field: "x"})
	if err == nil {
		t.Fatal("expected error for empty transformOp, got nil")
	}
}

func TestNewTransform_EmptyField(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "remove"})
	if err == nil {
		t.Fatal("expected error for empty field, got nil")
	}
}

func TestNewTransform_SetRequiresValueExpr(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "set", Field: "x"})
	if err == nil {
		t.Fatal("expected error for missing valueExpr on set, got nil")
	}
}

func TestNewTransform_SetInvalidValueExpr(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "set", Field: "x", ValueExpr: "not valid ["})
	if err == nil {
		t.Fatal("expected error for invalid valueExpr, got nil")
	}
}

func TestNewTransform_RenameRequiresNewField(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "rename", Field: "x"})
	if err == nil {
		t.Fatal("expected error for missing newField on rename, got nil")
	}
}

func TestTransform_Set(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "set", Field: "upper_type", ValueExpr: `fields["type"] + "!"`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["type"] = "a"

	pass, err := st.Process(msg)
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if !pass {
		t.Fatal("expected pass=true (transforms never drop)")
	}
	if msg.Fields["upper_type"] != "a!" {
		t.Fatalf("upper_type = %#v, want \"a!\"", msg.Fields["upper_type"])
	}
}

func TestTransform_Set_EvalErrorFailsOpen(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "set", Field: "x", ValueExpr: `double(fields["missing"])`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")

	pass, err := st.Process(msg)
	if !pass {
		t.Fatal("expected pass=true even on eval error (fail-open)")
	}
	if err == nil {
		t.Fatal("expected non-nil error from eval failure")
	}
	if _, ok := msg.Fields["x"]; ok {
		t.Fatal("field should not be set when eval fails")
	}
}

func TestTransform_Rename(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "rename", Field: "old", NewField: "new"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["old"] = "value"

	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("Process = (%v, %v)", pass, err)
	}
	if msg.Fields["new"] != "value" {
		t.Fatalf("new field = %#v, want \"value\"", msg.Fields["new"])
	}
	if _, ok := msg.Fields["old"]; ok {
		t.Fatal("old field should have been deleted")
	}
}

func TestTransform_Rename_MissingSourceIsNoOp(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "rename", Field: "old", NewField: "new"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")

	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("Process = (%v, %v)", pass, err)
	}
	if _, ok := msg.Fields["new"]; ok {
		t.Fatal("new field should not be set when source field was missing")
	}
}

// TestTransform_Set_NonScalarValueExpr confirms a "set" whose ValueExpr
// produces a non-scalar CEL result (a list literal) doesn't panic and stores
// something into Fields (an interface{} can hold any CEL-produced value;
// note CEL list/map results come back as cel-go's own ref.Val-typed
// collections, e.g. []ref.Val, not a plain []interface{}).
func TestTransform_Set_NonScalarValueExpr(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "set", Field: "x", ValueExpr: `[1, 2, 3]`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")

	pass, err := st.Process(msg)
	if err != nil {
		t.Fatalf("Process error = %v, want nil", err)
	}
	if !pass {
		t.Fatal("expected pass=true")
	}
	got := msg.Fields["x"]
	if got == nil {
		t.Fatal("expected a non-nil list value to be stored in Fields")
	}
	// Confirm it doesn't panic to stringify (e.g. via fmt.Sprint, as dedup's
	// valToString does for non-string CEL results).
	if s := fmt.Sprint(got); s == "" {
		t.Fatal("expected non-empty string representation of the stored list value")
	}
}

// TestTransform_Set_NonScalarMapValueExpr is the map-literal analogue of
// TestTransform_Set_NonScalarValueExpr.
func TestTransform_Set_NonScalarMapValueExpr(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "set", Field: "x", ValueExpr: `{"a": 1}`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")

	pass, err := st.Process(msg)
	if err != nil {
		t.Fatalf("Process error = %v, want nil", err)
	}
	if !pass {
		t.Fatal("expected pass=true")
	}
	if msg.Fields["x"] == nil {
		t.Fatal("expected a non-nil map value to be stored in Fields")
	}
}

// TestTransform_DotFlattenedKey confirms transforms operate at the flat
// Fields map key level — a key containing a literal dot (as produced by a
// parser that flattens nested JSON, e.g. "a.b") is treated as one opaque
// string key, not traversed as nested structure.
func TestTransform_DotFlattenedKey(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "rename", Field: "a.b", NewField: "a.c"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["a.b"] = "nested-flat-value"

	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("Process = (%v, %v), want (true, nil)", pass, err)
	}
	if msg.Fields["a.c"] != "nested-flat-value" {
		t.Fatalf("a.c = %#v, want \"nested-flat-value\"", msg.Fields["a.c"])
	}
	if _, ok := msg.Fields["a.b"]; ok {
		t.Fatal("a.b should have been deleted by rename")
	}
}

func TestTransform_Remove(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "t1", Kind: "transform", TransformOp: "remove", Field: "secret"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["secret"] = "shh"
	msg.Fields["keep"] = "yes"

	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("Process = (%v, %v)", pass, err)
	}
	if _, ok := msg.Fields["secret"]; ok {
		t.Fatal("secret field should have been removed")
	}
	if msg.Fields["keep"] != "yes" {
		t.Fatal("unrelated field should be untouched")
	}
}
