package sink

import (
	"encoding/json"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLokiPushShapeAuthenticationAndTimestamp(t *testing.T) {
	ts := time.Date(2026, 10, 15, 12, 0, 0, 123, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/loki/api/v1/push" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-Scope-OrgID") != "development" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("push headers missing")
		}
		var got lokiPush
		if e := json.NewDecoder(r.Body).Decode(&got); e != nil {
			t.Error(e)
			return
		}
		if len(got.Streams) != 1 || len(got.Streams[0].Values) != 1 {
			t.Errorf("wrong Loki streams: %#v", got)
			return
		}
		stream := got.Streams[0]
		if stream.Stream["app"] != "gofi" || stream.Stream["listener"] != "telemetry" || stream.Stream["environment"] != "development" {
			t.Errorf("wrong staticlabels %#v", stream.Stream)
		}
		if stream.Values[0][0] != "1792065600000000123" {
			t.Errorf("timestamp must be nanoseconds string: %s", stream.Values[0][0])
		}
		var line map[string]any
		if e := json.Unmarshal([]byte(stream.Values[0][1]), &line); e != nil {
			t.Error(e)
		}
		if line["id"] != "event-1" || line["fields"].(map[string]any)["status"] != float64(201) {
			t.Errorf("wrong logline: %#v", line)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	s, e := newLoki(spec.SinkCfg{URL: server.URL, Labels: map[string]string{"environment": "development"}, Headers: map[string]string{"X-Scope-OrgID": "development"}, Auth: &spec.HTTPAuth{Type: "bearer", Token: "secret"}}, "telemetry")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Send(&message.Message{ID: "event-1", Fields: map[string]any{"status": 201}, Meta: map[string]string{"ts": ts.Format(time.RFC3339Nano)}}); e != nil {
		t.Fatal(e)
	}
}
func TestLokiExplicitPushPathRawBodyAndRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/push" {
			t.Errorf("duplicated pushpath %s", r.URL.Path)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "user" || password != "password" {
			t.Error("basic auth missing")
		}
		var got lokiPush
		if e := json.NewDecoder(r.Body).Decode(&got); e != nil {
			t.Error(e)
		}
		if got.Streams[0].Values[0][1] != "raw\nrecord" {
			t.Error("raw mode did not preserve log line")
		}
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, "rate limited")
	}))
	defer server.Close()
	s, e := newLoki(spec.SinkCfg{URL: server.URL + "/loki/api/v1/push", BodyMode: "raw", Auth: &spec.HTTPAuth{Type: "basic", Username: "user", Password: "password"}}, "test")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Send(&message.Message{Raw: []byte("raw\nrecord")}); e == nil || !strings.Contains(e.Error(), "429") {
		t.Fatalf("expected rejected push: %v", e)
	}
}
func TestLokiStaticValidation(t *testing.T) {
	for _, cfg := range []spec.SinkCfg{{URL: "file:///tmp/logs"}, {URL: "http://user:secret@localhost:3100"}, {URL: "https://loki.example", Labels: map[string]string{"bad-label": "x"}}, {URL: "https://loki.example", Labels: map[string]string{"__reserved": "x"}}, {URL: "https://loki.example", BodyMode: "invalid"}, {URL: "https://loki.example", Auth: &spec.HTTPAuth{Type: "invalid"}}} {
		if e := ValidateLoki(cfg); e == nil {
			t.Fatalf("invalid config accepted %#v", cfg)
		}
	}
	if e := ValidateLoki(spec.SinkCfg{URL: "http://unreachable.invalid:3100"}); e != nil {
		t.Fatal("validation must not dial network", e)
	}
}
