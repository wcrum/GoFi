package filter

import (
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestRegexFiltersRawAndDecodedFields(t *testing.T) {
	msg := message.New([]byte("POST /payments HTTP/1.1"), "test", "test")
	msg.Fields["app.name"] = "payments-v2"
	for _, tc := range []struct {
		field, expression string
		want              bool
	}{
		{"", `^POST /payments\b`, true},
		{"", `^GET `, false},
		{"app.name", `^payments-v[0-9]+$`, true},
		{"app.name", `^inventory`, false},
	} {
		stage, err := New(spec.StageSpec{Kind: "filter", Type: "regex", Field: tc.field, Expression: tc.expression})
		if err != nil {
			t.Fatal(err)
		}
		if pass, err := stage.Process(msg); err != nil || pass != tc.want {
			t.Fatalf("regex %q field %q = %v, %v; want %v", tc.expression, tc.field, pass, err, tc.want)
		}
	}
}

func TestRegexConfigurationAndFieldErrors(t *testing.T) {
	for _, expression := range []string{"", "[", "(?<=unsupported)lookbehind"} {
		if _, err := New(spec.StageSpec{Kind: "filter", Type: "regex", Expression: expression}); err == nil {
			t.Fatalf("expected invalid expression %q to fail", expression)
		}
	}
	stage, err := New(spec.StageSpec{Kind: "filter", Type: "regex", Field: "value", Expression: ".*"})
	if err != nil {
		t.Fatal(err)
	}
	msg := message.New(nil, "test", "test")
	if pass, err := stage.Process(msg); !pass || err == nil {
		t.Fatalf("missing field = %v, %v; want fail-open error", pass, err)
	}
	msg.Fields["value"] = 42
	if pass, err := stage.Process(msg); !pass || err == nil {
		t.Fatalf("numeric field = %v, %v; want fail-open error", pass, err)
	}
}
