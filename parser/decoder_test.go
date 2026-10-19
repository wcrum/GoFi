package parser

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func compressForTest(t *testing.T, input []byte, gzipFormat bool) []byte {
	t.Helper()
	var output bytes.Buffer
	var writer io.WriteCloser
	if gzipFormat {
		writer = gzip.NewWriter(&output)
	} else {
		writer = zlib.NewWriter(&output)
	}
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestDecodersParseAndPreserveOriginalFrame(t *testing.T) {
	payload := []byte(`{"service":"payments","status":200}`)
	tests := []struct {
		name string
		raw  []byte
	}{
		{"base64", []byte(base64.StdEncoding.EncodeToString(payload))},
		{"base64url", []byte(base64.RawURLEncoding.EncodeToString(payload))},
		{"hex", []byte(hex.EncodeToString(payload))},
		{"gzip", compressForTest(t, payload, true)},
		{"zlib", compressForTest(t, payload, false)},
		{"url", []byte(url.QueryEscape(string(payload)))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := decoding("json", spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: tc.name}}})
			if err != nil {
				t.Fatal(err)
			}
			original := append([]byte(nil), tc.raw...)
			msg := message.New(tc.raw, "test", "test")
			if err := p.Parse(msg); err != nil {
				t.Fatal(err)
			}
			if msg.Fields["service"] != "payments" || msg.Fields["status"] != float64(200) {
				t.Fatalf("decoded fields = %#v", msg.Fields)
			}
			if !bytes.Equal(msg.Raw, original) {
				t.Fatal("decoder modified original frame")
			}
		})
	}
}

func TestDecoderChainOrderAndPassthrough(t *testing.T) {
	payload := []byte("text with a plus + and newline\n")
	compressed := compressForTest(t, payload, true)
	raw := []byte(base64.StdEncoding.EncodeToString(compressed))
	p, err := decoding("none", spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: "base64"}, {Type: "gzip"}}})
	if err != nil {
		t.Fatal(err)
	}
	msg := message.New(raw, "test", "test")
	if err := p.Parse(msg); err != nil {
		t.Fatal(err)
	}
	if msg.Fields["raw"] != string(payload) || !bytes.Equal(msg.Raw, raw) {
		t.Fatalf("unexpected decoded message: %#v", msg)
	}
	p, err = decoding("none", spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: "gzip"}, {Type: "base64"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Parse(message.New(raw, "test", "test")); err == nil {
		t.Fatal("reversed decoder order should fail")
	}
}

func TestDecodersRejectInvalidConfiguration(t *testing.T) {
	for _, cfg := range []spec.DecodeCfg{
		{Decoders: []spec.CodecStep{{Type: "unknown"}}},
		{MaxDecodedBytes: -1},
	} {
		if _, err := decoding("json", cfg); err == nil {
			t.Fatalf("expected error for %#v", cfg)
		}
	}
	if _, err := New(spec.ParserCfg{ErrorPolicy: "typo"}); err == nil {
		t.Fatal("expected error for an unknown errorPolicy")
	}
}

func TestDecodersRejectMalformedOrOversizedOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"base64", []byte("not-base64!")},
		{"base64url", []byte("a")},
		{"hex", []byte("abc")},
		{"gzip", []byte("not gzip")},
		{"zlib", []byte("not zlib")},
		{"url", []byte("bad%zz")},
		{"gzip", compressForTest(t, []byte(strings.Repeat("x", 4096)), true)},
		{"zlib", compressForTest(t, []byte(strings.Repeat("x", 4096)), false)},
		{"base64", []byte(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 65))))},
		{"hex", []byte(hex.EncodeToString([]byte(strings.Repeat("x", 65))))},
		{"url", []byte(strings.Repeat("%78", 65))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := decoding("", spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: tc.name}}, MaxDecodedBytes: 64})
			if err != nil {
				t.Fatal(err)
			}
			msg := message.New(tc.raw, "test", "test")
			if err := p.Parse(msg); err == nil {
				t.Fatal("expected malformed or oversized decoding error")
			}
			if len(msg.Fields) != 0 {
				t.Fatalf("failed decoding exposed partial fields: %#v", msg.Fields)
			}
		})
	}
}

func TestDecoderExactLimitAndChecksum(t *testing.T) {
	payload := []byte(strings.Repeat("x", 64))
	raw := compressForTest(t, payload, true)
	p, err := decoding("", spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: "gzip"}}, MaxDecodedBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Parse(message.New(raw, "test", "test")); err != nil {
		t.Fatalf("exactly-at-limit payload: %v", err)
	}
	raw[len(raw)-8] ^= 0xff // corrupt the checksum, preserving the gzip header
	if err := p.Parse(message.New(raw, "test", "test")); err == nil {
		t.Fatal("expected gzip checksum error")
	}
}

// A parser that replaces the fields map must still deliver its result when
// decoders run first (the Python parser does this).
type replacingParser struct{}

func (replacingParser) Parse(msg *message.Message) error {
	msg.Fields = map[string]any{"decoded": string(msg.Raw)}
	return nil
}

func TestDecodedParserResultReachesTheMessage(t *testing.T) {
	p, err := WithDecode(replacingParser{}, spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: "base64"}}})
	if err != nil {
		t.Fatal(err)
	}
	msg := message.New([]byte("aGVsbG8="), "test", "local")
	if err := p.Parse(msg); err != nil {
		t.Fatal(err)
	}
	if msg.Fields["decoded"] != "hello" || string(msg.Raw) != "aGVsbG8=" {
		t.Fatalf("fields %v raw %q", msg.Fields, msg.Raw)
	}
}

func decoding(format string, cfg spec.DecodeCfg) (Parser, error) {
	p, err := New(spec.ParserCfg{Format: format})
	if err != nil {
		return nil, err
	}
	return WithDecode(p, cfg)
}

// ForFlow reads a legacy parser.decoders flow as a decode step.
func TestForFlowRunsLegacyDecoders(t *testing.T) {
	def := spec.FlowDefinition{Parser: spec.ParserCfg{Format: "none", LegacyDecoders: []spec.CodecStep{{Type: "base64"}}}}
	p, err := ForFlow(def)
	if err != nil {
		t.Fatal(err)
	}
	msg := message.New([]byte("aGVsbG8="), "test", "local")
	if err := p.Parse(msg); err != nil || msg.Fields["raw"] != "hello" {
		t.Fatalf("fields %v err %v", msg.Fields, err)
	}
}
