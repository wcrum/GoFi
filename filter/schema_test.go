package filter

import (
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestNewSchema_RequiresFields(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "s1", Kind: "schema"})
	if err == nil {
		t.Fatal("expected error for empty requiredFields, got nil")
	}
}

func TestSchema_PassesWithAllFieldsPresent(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "s1", Kind: "schema", RequiredFields: []string{"type", "val"}})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["type"] = "a"
	msg.Fields["val"] = "1"

	pass, err := st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("Process = (%v, %v), want (true, nil)", pass, err)
	}
}

func TestSchema_DropsWithMissingField(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "s1", Kind: "schema", RequiredFields: []string{"type", "val"}})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["type"] = "a"
	// "val" missing

	pass, err := st.Process(msg)
	if err != nil {
		t.Fatalf("Process error = %v, want nil (missing field is not an error)", err)
	}
	if pass {
		t.Fatal("expected pass=false when a required field is missing")
	}
}

// TestSchema_DropsWithPartiallyPresentFields confirms that having SOME (but
// not all) required fields present still drops the message — this isn't an
// all-or-nothing check, every required field must independently be present.
func TestSchema_DropsWithPartiallyPresentFields(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "s1", Kind: "schema", RequiredFields: []string{"a", "b", "c"}})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["a"] = "1"
	msg.Fields["b"] = "2"
	// "c" missing: 2 of 3 required fields present.

	pass, err := st.Process(msg)
	if err != nil {
		t.Fatalf("Process error = %v, want nil", err)
	}
	if pass {
		t.Fatal("expected pass=false when only 2 of 3 required fields are present")
	}
}

// TestSchema_PresentNilValueSatisfiesRequirement documents a subtle but
// deliberate distinction: schemaStage checks key PRESENCE via the two-value
// map read (`_, ok := msg.Fields[field]`), not whether the value is
// non-nil. A field parsed from JSON `null` (e.g. `{"a": null}`) is present
// in Fields with a nil interface{} value, and that SATISFIES the schema
// requirement today — this is intentional, not a bug (see task instructions:
// do not "fix" this).
func TestSchema_PresentNilValueSatisfiesRequirement(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "s1", Kind: "schema", RequiredFields: []string{"a"}})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg := message.New(nil, "l", "r")
	msg.Fields["a"] = nil // present, but nil value (as if parsed from JSON null)

	pass, err := st.Process(msg)
	if err != nil {
		t.Fatalf("Process error = %v, want nil", err)
	}
	if !pass {
		t.Fatal("expected pass=true: a present-but-nil field value satisfies the requirement (presence check, not nil check)")
	}
}
