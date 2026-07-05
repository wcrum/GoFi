package parser

import (
	"strings"
	"testing"

	"github.com/wcrum/gofi/message"
)

func newJSONMsg(raw string) *message.Message {
	return message.New([]byte(raw), "test", "test")
}

func TestJSONParser_FlatObject(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"a":1,"b":"x","c":true}`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}

	// encoding/json unmarshals JSON numbers into interface{} as float64.
	if v, ok := msg.Fields["a"].(float64); !ok || v != 1 {
		t.Fatalf("Fields[a] = %#v, want float64(1)", msg.Fields["a"])
	}
	if v, ok := msg.Fields["b"].(string); !ok || v != "x" {
		t.Fatalf("Fields[b] = %#v, want string(x)", msg.Fields["b"])
	}
	if v, ok := msg.Fields["c"].(bool); !ok || v != true {
		t.Fatalf("Fields[c] = %#v, want bool(true)", msg.Fields["c"])
	}
	if len(msg.Fields) != 3 {
		t.Fatalf("len(Fields) = %d, want 3 (got %#v)", len(msg.Fields), msg.Fields)
	}
}

func TestJSONParser_NestedObject(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"a":{"b":{"c":1}}}`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	v, ok := msg.Fields["a.b.c"].(float64)
	if !ok || v != 1 {
		t.Fatalf("Fields[a.b.c] = %#v, want float64(1)", msg.Fields["a.b.c"])
	}
	if len(msg.Fields) != 1 {
		t.Fatalf("len(Fields) = %d, want 1 (got %#v)", len(msg.Fields), msg.Fields)
	}
}

// TestJSONParser_ArrayOfScalars documents CURRENT behavior: arrays are treated
// as opaque leaf values and stored directly under the prefix key. They are not
// recursed into or exploded into indexed keys (e.g. "tags.0").
func TestJSONParser_ArrayOfScalars(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"tags":["x","y","z"]}`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	arr, ok := msg.Fields["tags"].([]interface{})
	if !ok {
		t.Fatalf("Fields[tags] = %#v, want []interface{}", msg.Fields["tags"])
	}
	want := []interface{}{"x", "y", "z"}
	if len(arr) != len(want) {
		t.Fatalf("Fields[tags] = %#v, want %#v", arr, want)
	}
	for i := range want {
		if arr[i] != want[i] {
			t.Fatalf("Fields[tags][%d] = %#v, want %#v", i, arr[i], want[i])
		}
	}
}

// TestJSONParser_ArrayOfObjects documents CURRENT behavior: today's
// flattenInto does NOT recurse into array elements, even when those elements
// are objects. The entire raw []interface{} (containing map[string]interface{}
// elements) is stored unflattened under the prefix key. Neither "items.0.id"
// nor any other flattened variant is produced. This is a real limitation of
// the current parser (documented here, not "fixed" — that would be a product
// decision out of scope for this test).
func TestJSONParser_ArrayOfObjects(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"items":[{"id":1},{"id":2}]}`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	arr, ok := msg.Fields["items"].([]interface{})
	if !ok {
		t.Fatalf("Fields[items] = %#v, want []interface{}", msg.Fields["items"])
	}
	if len(arr) != 2 {
		t.Fatalf("len(Fields[items]) = %d, want 2", len(arr))
	}
	elem0, ok := arr[0].(map[string]interface{})
	if !ok || elem0["id"] != float64(1) {
		t.Fatalf("Fields[items][0] = %#v, want map with id=1", arr[0])
	}
	elem1, ok := arr[1].(map[string]interface{})
	if !ok || elem1["id"] != float64(2) {
		t.Fatalf("Fields[items][1] = %#v, want map with id=2", arr[1])
	}

	if _, ok := msg.Fields["items.0.id"]; ok {
		t.Fatal("Fields[items.0.id] should not exist; arrays are not recursed into")
	}
	if len(msg.Fields) != 1 {
		t.Fatalf("len(Fields) = %d, want 1 (got %#v)", len(msg.Fields), msg.Fields)
	}
}

func TestJSONParser_NullValue(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"a":null}`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	v, ok := msg.Fields["a"]
	if !ok {
		t.Fatal("Fields[a] key should be present (with nil value), not absent")
	}
	if v != nil {
		t.Fatalf("Fields[a] = %#v, want nil", v)
	}
}

func TestJSONParser_Boolean(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"flag":false}`)
	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["flag"].(bool); !ok || v != false {
		t.Fatalf("Fields[flag] = %#v, want bool(false)", msg.Fields["flag"])
	}
}

func TestJSONParser_Float(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"pi":3.14159}`)
	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["pi"].(float64); !ok || v != 3.14159 {
		t.Fatalf("Fields[pi] = %#v, want float64(3.14159)", msg.Fields["pi"])
	}
}

func TestJSONParser_NegativeNumber(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"n":-42}`)
	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["n"].(float64); !ok || v != -42 {
		t.Fatalf("Fields[n] = %#v, want float64(-42)", msg.Fields["n"])
	}
}

func TestJSONParser_VeryLargeNumber(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"big":123456789012345}`)
	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	v, ok := msg.Fields["big"].(float64)
	if !ok {
		t.Fatalf("Fields[big] = %#v, want float64", msg.Fields["big"])
	}
	if v != float64(123456789012345) {
		t.Fatalf("Fields[big] = %v, want %v", v, float64(123456789012345))
	}
}

func TestJSONParser_EmptyString(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"s":""}`)
	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["s"].(string); !ok || v != "" {
		t.Fatalf("Fields[s] = %#v, want empty string", msg.Fields["s"])
	}
}

func TestJSONParser_EmptyObject(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{}`)
	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if len(msg.Fields) != 0 {
		t.Fatalf("Fields = %#v, want empty map", msg.Fields)
	}
}

// TestJSONParser_EmptyArrayField documents current behavior: an empty array
// value is stored as-is (an empty, non-nil []interface{}) under its key.
func TestJSONParser_EmptyArrayField(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"a":[]}`)
	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	arr, ok := msg.Fields["a"].([]interface{})
	if !ok {
		t.Fatalf("Fields[a] = %#v, want []interface{}", msg.Fields["a"])
	}
	if len(arr) != 0 {
		t.Fatalf("Fields[a] = %#v, want empty slice", arr)
	}
}

// TestJSONParser_TopLevelArray documents current behavior: when the JSON
// document's root value is an array (not an object), flattenInto is called
// with prefix="". The []interface{} case matches before recursing into any
// object structure, so the entire array is stored directly under the empty
// string key: Fields[""].
func TestJSONParser_TopLevelArray(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`[1,2,3]`)
	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	arr, ok := msg.Fields[""].([]interface{})
	if !ok {
		t.Fatalf(`Fields[""] = %#v, want []interface{}`, msg.Fields[""])
	}
	want := []interface{}{float64(1), float64(2), float64(3)}
	if len(arr) != len(want) {
		t.Fatalf(`Fields[""] = %#v, want %#v`, arr, want)
	}
	for i := range want {
		if arr[i] != want[i] {
			t.Fatalf(`Fields[""][%d] = %#v, want %#v`, i, arr[i], want[i])
		}
	}
	if len(msg.Fields) != 1 {
		t.Fatalf("len(Fields) = %d, want 1 (got %#v)", len(msg.Fields), msg.Fields)
	}
}

func TestJSONParser_MalformedJSON(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"a": 1,`) // truncated

	err := p.Parse(msg)
	if err == nil {
		t.Fatal("expected error for malformed/truncated JSON, got nil")
	}
	if !strings.Contains(err.Error(), "json") && !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("error message %q does not mention json/unmarshal context", err.Error())
	}
}

// TestJSONParser_DuplicateFlattenedKey documents a pathological collision: a
// literal dot-containing key ("a.b") and a nested path ("a" -> "b") can
// flatten to the same destination key ("a.b"). Since flattenInto iterates a
// real Go map (top-level JSON object), iteration order is nondeterministic,
// so which value wins (1 from the literal key, or 2 from the nested object)
// is not guaranteed. This test only asserts that no panic/error occurs and
// that the resulting value is one of the two possible values.
func TestJSONParser_DuplicateFlattenedKey(t *testing.T) {
	p := &jsonParser{}
	msg := newJSONMsg(`{"a.b": 1, "a": {"b": 2}}`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	v, ok := msg.Fields["a.b"].(float64)
	if !ok {
		t.Fatalf(`Fields["a.b"] = %#v, want float64`, msg.Fields["a.b"])
	}
	if v != 1 && v != 2 {
		t.Fatalf(`Fields["a.b"] = %v, want 1 or 2`, v)
	}
}
