package codec

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestEveryBuiltinRoundTrips(t *testing.T) {
	input := []byte(`{"sensor":"alpha","note":"a b&c=d/é"}`)
	for _, name := range spec.BuiltinCodecs {
		t.Run(name, func(t *testing.T) {
			steps := []spec.CodecStep{{Type: name}}
			encoded, err := Encode(context.Background(), steps, input, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(context.Background(), steps, encoded, 0, nil)
			if err != nil || !bytes.Equal(decoded, input) {
				t.Fatalf("round trip %q: %v", decoded, err)
			}
		})
	}
}

func TestChainsRunInOrder(t *testing.T) {
	input := []byte("payload")
	encode := []spec.CodecStep{{Type: "gzip"}, {Type: "base64"}}
	encoded, err := Encode(context.Background(), encode, input, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Decoding reverses the order: base64 first, then gzip.
	decoded, err := Decode(context.Background(), []spec.CodecStep{{Type: "base64"}, {Type: "gzip"}}, encoded, 0, nil)
	if err != nil || string(decoded) != "payload" {
		t.Fatalf("%q %v", decoded, err)
	}
	if _, err := Decode(context.Background(), encode, encoded, 0, nil); err == nil {
		t.Fatal("decoding in the wrong order must fail")
	}
}

func TestPythonStepsUseTheInstalledRunner(t *testing.T) {
	SetPythonRunner(func(_ context.Context, ref *spec.PythonRef, kind string, data []byte, _ *message.Message) ([]byte, error) {
		return append([]byte(kind+":"+ref.Function+":"), data...), nil
	})
	t.Cleanup(func() { SetPythonRunner(nil) })
	steps := []spec.CodecStep{{Type: "python", Python: &spec.PythonRef{Library: "lib", Function: "wrap"}}, {Type: "hex"}}
	out, err := Encode(context.Background(), steps, []byte("x"), 0, nil)
	if err != nil || string(out) != "656e636f6465723a777261703a78" { // hex("encoder:wrap:x")
		t.Fatalf("%q %v", out, err)
	}
}

func TestCodecStepJSONShorthandAndValidation(t *testing.T) {
	var cfg spec.ParserCfg
	if err := json.Unmarshal([]byte(`{"format":"json","decoders":["base64",{"type":"python","python":{"library":"l","function":"f"}}]}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Decoders[0].Type != "base64" || cfg.Decoders[1].Python.Function != "f" {
		t.Fatalf("%+v", cfg.Decoders)
	}
	for _, bad := range []spec.CodecStep{{Type: "rot13"}, {Type: "python"}, {Type: "base64", Python: &spec.PythonRef{Library: "l", Function: "f"}}} {
		if err := bad.Validate("decoder"); err == nil {
			t.Fatalf("%+v must be rejected", bad)
		}
	}
	if err := (&spec.Encoding{Format: "xml"}).Validate(); err == nil {
		t.Fatal("unknown encoding format must be rejected")
	}
}
