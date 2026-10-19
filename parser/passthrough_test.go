package parser

import (
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestPassthroughParser_RawString(t *testing.T) {
	p := &passthroughParser{}
	msg := message.New([]byte("hello world"), "test", "test")

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["raw"].(string); !ok || v != "hello world" {
		t.Fatalf(`Fields["raw"] = %#v, want "hello world"`, msg.Fields["raw"])
	}
}

func TestPassthroughParser_EmptyInput(t *testing.T) {
	p := &passthroughParser{}
	msg := message.New([]byte{}, "test", "test")

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["raw"].(string); !ok || v != "" {
		t.Fatalf(`Fields["raw"] = %#v, want ""`, msg.Fields["raw"])
	}
}

func TestPassthroughParser_NilInput(t *testing.T) {
	p := &passthroughParser{}
	msg := message.New(nil, "test", "test")

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["raw"].(string); !ok || v != "" {
		t.Fatalf(`Fields["raw"] = %#v, want ""`, msg.Fields["raw"])
	}
}

// TestPassthroughParser_NonUTF8Bytes confirms actual behavior for a byte
// sequence that isn't valid UTF-8. Go's string(bytes) conversion never
// errors — it just reinterprets the raw bytes as a string, preserving them
// byte-for-byte (invalid UTF-8 sequences are carried through verbatim in the
// string's underlying bytes; it's only rune-oriented consumers, e.g.
// range-over-string or %q formatting, that would surface replacement
// characters). This test confirms the byte-for-byte round trip.
func TestPassthroughParser_NonUTF8Bytes(t *testing.T) {
	p := &passthroughParser{}
	raw := []byte{0xFF, 0xFE, 0x00, 0x01}
	msg := message.New(raw, "test", "test")

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	v, ok := msg.Fields["raw"].(string)
	if !ok {
		t.Fatalf(`Fields["raw"] = %#v, want string`, msg.Fields["raw"])
	}
	if v != string(raw) {
		t.Fatalf("Fields[raw] bytes = %v, want %v", []byte(v), raw)
	}
	if len(v) != len(raw) {
		t.Fatalf("len(Fields[raw]) = %d, want %d (byte-for-byte preservation)", len(v), len(raw))
	}
}

// TestTextFormatIsThePassthroughParser: the top-level parser accepts
// format "text", the parse stage's name for "the decoded frame as a string
// in fields.raw", as a synonym of "none".
func TestTextFormatIsThePassthroughParser(t *testing.T) {
	for _, format := range []string{"text", "none", ""} {
		p, err := New(spec.ParserCfg{Format: format})
		if err != nil {
			t.Fatalf("format %q rejected: %v", format, err)
		}
		msg := message.New([]byte("plain line"), "test", "test")
		if err := p.Parse(msg); err != nil {
			t.Fatal(err)
		}
		if msg.Fields["raw"] != "plain line" {
			t.Fatalf("format %q: fields = %v", format, msg.Fields)
		}
	}
	if _, err := New(spec.ParserCfg{Format: "txt"}); err == nil {
		t.Fatal("unknown format accepted")
	}
}
