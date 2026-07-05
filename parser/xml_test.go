package parser

import (
	"testing"

	"github.com/wcrum/gofi/message"
)

func newXMLMsg(raw string) *message.Message {
	return message.New([]byte(raw), "test", "test")
}

func TestXMLParser_ElementWithAttributes(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg(`<event id="1" type="a-f-G"/>`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["event.id"].(string); !ok || v != "1" {
		t.Fatalf(`Fields["event.id"] = %#v, want "1"`, msg.Fields["event.id"])
	}
	if v, ok := msg.Fields["event.type"].(string); !ok || v != "a-f-G" {
		t.Fatalf(`Fields["event.type"] = %#v, want "a-f-G"`, msg.Fields["event.type"])
	}
}

func TestXMLParser_NestedElementWithText(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg(`<root><child>hello</child></root>`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["root.child"].(string); !ok || v != "hello" {
		t.Fatalf(`Fields["root.child"] = %#v, want "hello"`, msg.Fields["root.child"])
	}
}

// TestXMLParser_SiblingNestedStructures is the key regression check for the
// path-stack: two sibling branches ("a" and "b"), each with their own child
// named "x", must not leak values into each other. If EndElement handling had
// an off-by-one in popping the path stack, one of these would clobber or
// merge with the other.
func TestXMLParser_SiblingNestedStructures(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg(`<root><a><x>1</x></a><b><x>2</x></b></root>`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["root.a.x"].(string); !ok || v != "1" {
		t.Fatalf(`Fields["root.a.x"] = %#v, want "1"`, msg.Fields["root.a.x"])
	}
	if v, ok := msg.Fields["root.b.x"].(string); !ok || v != "2" {
		t.Fatalf(`Fields["root.b.x"] = %#v, want "2"`, msg.Fields["root.b.x"])
	}
}

func TestXMLParser_DeeplyNested(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg(`<a><b><c><d>deep</d></c></b></a>`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["a.b.c.d"].(string); !ok || v != "deep" {
		t.Fatalf(`Fields["a.b.c.d"] = %#v, want "deep"`, msg.Fields["a.b.c.d"])
	}
}

// TestXMLParser_RepeatedTextConcatenates confirms that when multiple CharData
// tokens occur under the same element (here, split around an XML comment,
// which most decoders surface as two separate CharData tokens), the parser
// concatenates them with a single space separator rather than overwriting or
// erroring.
func TestXMLParser_RepeatedTextConcatenates(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg(`<a>hello<!--comment--> world</a>`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	got, ok := msg.Fields["a"].(string)
	if !ok {
		t.Fatalf(`Fields["a"] = %#v, want string`, msg.Fields["a"])
	}
	if got != "hello world" {
		t.Fatalf(`Fields["a"] = %q, want "hello world"`, got)
	}
}

// TestXMLParser_WhitespaceOnlyTextIsSkipped confirms that whitespace-only
// CharData between elements (e.g. from pretty-printed/indented XML) is
// trimmed to empty and skipped rather than polluting the parent element's
// field with a stray space or empty string value.
func TestXMLParser_WhitespaceOnlyTextIsSkipped(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg("<root>\n  <child>x</child>\n</root>")

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["root"]; ok {
		t.Fatalf(`Fields["root"] = %#v, want absent (whitespace-only text should be skipped)`, v)
	}
	if v, ok := msg.Fields["root.child"].(string); !ok || v != "x" {
		t.Fatalf(`Fields["root.child"] = %#v, want "x"`, msg.Fields["root.child"])
	}
}

// TestXMLParser_SelfClosingEmptyElement confirms that an element with no
// attributes and no text content produces no field at all (nothing writes to
// Fields for it, per the code path: StartElement only writes attr fields,
// EndElement never writes, CharData is never emitted for an empty element).
func TestXMLParser_SelfClosingEmptyElement(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg(`<root><empty/></root>`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["root.empty"]; ok {
		t.Fatalf(`Fields["root.empty"] = %#v, want absent`, v)
	}
	if len(msg.Fields) != 0 {
		t.Fatalf("Fields = %#v, want empty map", msg.Fields)
	}
}

func TestXMLParser_CDATA(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg(`<root><data><![CDATA[raw <text> here]]></data></root>`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["root.data"].(string); !ok || v != "raw <text> here" {
		t.Fatalf(`Fields["root.data"] = %#v, want "raw <text> here"`, msg.Fields["root.data"])
	}
}

// TestXMLParser_MalformedXML documents CURRENT behavior: the decode loop
// simply breaks on any token error (e.g. an unclosed tag) and Parse returns a
// nil error. Whatever was successfully parsed before the error point remains
// in Fields. This silently swallows XML errors rather than surfacing them to
// the caller, which is arguably a footgun worth revisiting as a product
// decision — but changing that behavior is out of scope for this test file.
func TestXMLParser_MalformedXML(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg(`<root><child>hello</child><unclosed>`)

	err := p.Parse(msg)
	if err != nil {
		t.Fatalf("Parse error = %v, want nil (current behavior swallows decode errors)", err)
	}
	// The successfully-parsed "child" element before the truncation point
	// should still be present.
	if v, ok := msg.Fields["root.child"].(string); !ok || v != "hello" {
		t.Fatalf(`Fields["root.child"] = %#v, want "hello"`, msg.Fields["root.child"])
	}
}

// TestXMLParser_Namespaces confirms Go's encoding/xml strips namespace
// prefixes via xml.Name.Local, so the field key uses just the local element
// name ("child"), not the prefixed form ("x:child") or the namespace URI.
func TestXMLParser_Namespaces(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg(`<root xmlns:x="urn:example"><x:child>val</x:child></root>`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["root.child"].(string); !ok || v != "val" {
		t.Fatalf(`Fields["root.child"] = %#v, want "val"`, msg.Fields["root.child"])
	}
	if _, ok := msg.Fields["root.x:child"]; ok {
		t.Fatal(`Fields["root.x:child"] should not exist; namespace prefix should be stripped`)
	}
}

func TestXMLParser_AttributeEscaping(t *testing.T) {
	p := &xmlParser{}
	msg := newXMLMsg(`<event note="a &amp; b"/>`)

	if err := p.Parse(msg); err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if v, ok := msg.Fields["event.note"].(string); !ok || v != "a & b" {
		t.Fatalf(`Fields["event.note"] = %#v, want "a & b"`, msg.Fields["event.note"])
	}
}
