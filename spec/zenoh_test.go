package spec

import (
	"encoding/json"
	"testing"
)

func TestZenohSubscribeValidation(t *testing.T) {
	valid := func(z ZenohSubscribe) error {
		return FlowSource{Subscribe: &SubscribeSource{Zenoh: &z}}.Validate()
	}
	for _, key := range []string{"demo/sensors/**", "*/temperature", "**", "demo/a b/é"} {
		if err := valid(ZenohSubscribe{URL: "http://router:8000", KeyExpr: key}); err != nil {
			t.Errorf("%q: %v", key, err)
		}
	}
	for _, key := range []string{"", "@/router/**", "demo/@/x", "demo/../x", "demo//a", "demo/", "/demo", "demo/*eval", "demo/$*", "demo?x=1", "demo#a", "demo%2Fa", "**/**", "demo/\x01"} {
		if err := valid(ZenohSubscribe{URL: "http://router:8000", KeyExpr: key}); err == nil {
			t.Errorf("accepted key expression %q", key)
		}
	}
	for _, origin := range []string{"router:8000", "zenoh://router:7447", "tcp://router:7447", "http://router:8000/prefix", "http://router:8000?q", "http://router:8000#f", "http://user:pw@router:8000"} {
		if err := valid(ZenohSubscribe{URL: origin, KeyExpr: "demo/a"}); err == nil {
			t.Errorf("accepted origin %q", origin)
		}
	}
	for _, z := range []ZenohSubscribe{
		{URL: "http://router:8000", KeyExpr: "demo/a", Kinds: []string{"PATCH"}},
		{URL: "http://router:8000", KeyExpr: "demo/a", Auth: &HTTPAuth{Type: "digest"}},
		{URL: "http://router:8000", KeyExpr: "demo/a", Headers: map[string]string{"X-A": "b\r\nInjected: 1"}},
	} {
		if err := valid(z); err == nil {
			t.Errorf("accepted %+v", z)
		}
	}
	both := FlowSource{Subscribe: &SubscribeSource{Zenoh: &ZenohSubscribe{URL: "http://router:8000", KeyExpr: "demo/a"}, SSE: &StreamSubscribe{URL: "http://x"}}}
	if both.Validate() == nil {
		t.Error("accepted two subscribe streams")
	}
}

func TestZenohSubscribeKindsAndJSON(t *testing.T) {
	var src FlowSource
	if err := json.Unmarshal([]byte(`{"subscribe":{"zenoh":{"url":"https://router","keyExpr":"demo/**","kinds":["DELETE"],"query":true}}}`), &src); err != nil {
		t.Fatal(err)
	}
	z := src.Subscribe.Zenoh
	if z == nil || !z.Query || z.AcceptsKind("PUT") || !z.AcceptsKind("DELETE") {
		t.Fatalf("decoded %+v", z)
	}
	if def := (&ZenohSubscribe{}); !def.AcceptsKind("PUT") || def.AcceptsKind("DELETE") {
		t.Fatal("default kinds must be PUT only")
	}
}
