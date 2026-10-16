package pipeline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestEncodeFormatsThenEncoders(t *testing.T) {
	msg := message.New([]byte("raw-frame"), "flow", "peer")
	msg.Fields = map[string]any{"sensor": "alpha"}
	if out, _ := Encode(context.Background(), nil, msg); out != msg || out.Payload != nil {
		t.Fatal("no encoding must leave the message untouched")
	}
	cases := map[string]func([]byte) bool{
		"raw":    func(b []byte) bool { return string(b) == "raw-frame" },
		"fields": func(b []byte) bool { return string(b) == `{"sensor":"alpha"}` },
		"wrapped": func(b []byte) bool {
			var v map[string]any
			return json.Unmarshal(b, &v) == nil && v["id"] == msg.ID && v["fields"].(map[string]any)["sensor"] == "alpha"
		},
	}
	for format, ok := range cases {
		out, err := Encode(context.Background(), &spec.Encoding{Format: format, Encoders: []spec.CodecStep{{Type: "base64"}}}, msg)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := base64.StdEncoding.DecodeString(string(out.Payload))
		if err != nil || !ok(plain) {
			t.Fatalf("%s: %q %v", format, plain, err)
		}
		if out == msg || msg.Payload != nil {
			t.Fatal("encoding must not modify the shared message")
		}
	}
}
