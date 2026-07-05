package sink

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// capturedRequest holds everything the test server observed about an
// incoming request, so assertions can run after the handler returns.
type capturedRequest struct {
	method string
	header http.Header
	body   []byte
}

func startCaptureServer(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.method = r.Method
		captured.header = r.Header.Clone()
		captured.body = body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func TestHTTPSinkDefaultMethodIsPost(t *testing.T) {
	srv, captured := startCaptureServer(t)

	s, err := newHTTP(spec.SinkCfg{Type: "http", URL: srv.URL})
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	msg := &message.Message{Fields: map[string]interface{}{"a": "b"}, Meta: map[string]string{"m": "1"}}
	if err := s.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if captured.method != http.MethodPost {
		t.Errorf("expected method POST, got %s", captured.method)
	}
}

func TestHTTPSinkExplicitMethodHonored(t *testing.T) {
	srv, captured := startCaptureServer(t)

	s, err := newHTTP(spec.SinkCfg{Type: "http", URL: srv.URL, Method: "PUT"})
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	msg := &message.Message{Fields: map[string]interface{}{}, Meta: map[string]string{}}
	if err := s.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if captured.method != http.MethodPut {
		t.Errorf("expected method PUT, got %s", captured.method)
	}
}

func TestHTTPSinkCustomHeaders(t *testing.T) {
	srv, captured := startCaptureServer(t)

	s, err := newHTTP(spec.SinkCfg{
		Type:    "http",
		URL:     srv.URL,
		Headers: map[string]string{"X-Custom": "hello", "X-Other": "world"},
	})
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	msg := &message.Message{Fields: map[string]interface{}{}, Meta: map[string]string{}}
	if err := s.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := captured.header.Get("X-Custom"); got != "hello" {
		t.Errorf("X-Custom header = %q, want hello", got)
	}
	if got := captured.header.Get("X-Other"); got != "world" {
		t.Errorf("X-Other header = %q, want world", got)
	}
}

func TestHTTPSinkCustomHeaderOverridesContentType(t *testing.T) {
	srv, captured := startCaptureServer(t)

	s, err := newHTTP(spec.SinkCfg{
		Type:    "http",
		URL:     srv.URL,
		Headers: map[string]string{"Content-Type": "text/plain"},
	})
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	msg := &message.Message{Fields: map[string]interface{}{}, Meta: map[string]string{}}
	if err := s.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := captured.header.Get("Content-Type"); got != "text/plain" {
		t.Errorf("Content-Type header = %q, want text/plain (user header should win)", got)
	}
}

func TestHTTPSinkAuthBearer(t *testing.T) {
	srv, captured := startCaptureServer(t)

	s, err := newHTTP(spec.SinkCfg{
		Type: "http",
		URL:  srv.URL,
		Auth: &spec.HTTPAuth{Type: "bearer", Token: "tok123"},
	})
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	msg := &message.Message{Fields: map[string]interface{}{}, Meta: map[string]string{}}
	if err := s.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := captured.header.Get("Authorization"); got != "Bearer tok123" {
		t.Errorf("Authorization header = %q, want 'Bearer tok123'", got)
	}
}

func TestHTTPSinkAuthBasic(t *testing.T) {
	srv, captured := startCaptureServer(t)

	s, err := newHTTP(spec.SinkCfg{
		Type: "http",
		URL:  srv.URL,
		Auth: &spec.HTTPAuth{Type: "basic", Username: "alice", Password: "secret"},
	})
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	msg := &message.Message{Fields: map[string]interface{}{}, Meta: map[string]string{}}
	if err := s.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	req := &http.Request{Header: captured.header}
	user, pass, ok := req.BasicAuth()
	if !ok {
		t.Fatalf("expected basic auth header to be present")
	}
	if user != "alice" || pass != "secret" {
		t.Errorf("basic auth = %s:%s, want alice:secret", user, pass)
	}
}

func TestHTTPSinkAuthAPIKeyDefaultHeader(t *testing.T) {
	srv, captured := startCaptureServer(t)

	s, err := newHTTP(spec.SinkCfg{
		Type: "http",
		URL:  srv.URL,
		Auth: &spec.HTTPAuth{Type: "apiKey", APIKey: "abc"},
	})
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	msg := &message.Message{Fields: map[string]interface{}{}, Meta: map[string]string{}}
	if err := s.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := captured.header.Get("X-API-Key"); got != "abc" {
		t.Errorf("X-API-Key header = %q, want abc", got)
	}
}

func TestHTTPSinkAuthAPIKeyCustomHeader(t *testing.T) {
	srv, captured := startCaptureServer(t)

	s, err := newHTTP(spec.SinkCfg{
		Type: "http",
		URL:  srv.URL,
		Auth: &spec.HTTPAuth{Type: "apiKey", HeaderName: "X-Custom-Key", APIKey: "abc"},
	})
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	msg := &message.Message{Fields: map[string]interface{}{}, Meta: map[string]string{}}
	if err := s.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := captured.header.Get("X-Custom-Key"); got != "abc" {
		t.Errorf("X-Custom-Key header = %q, want abc", got)
	}
}

func TestHTTPSinkBodyModeRaw(t *testing.T) {
	srv, captured := startCaptureServer(t)

	s, err := newHTTP(spec.SinkCfg{Type: "http", URL: srv.URL, BodyMode: "raw"})
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	msg := &message.Message{Raw: []byte("<xml>original</xml>"), Fields: map[string]interface{}{"a": "b"}}
	if err := s.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if string(captured.body) != "<xml>original</xml>" {
		t.Errorf("body = %q, want raw payload verbatim", captured.body)
	}
}

func TestHTTPSinkBodyModeWrappedDefault(t *testing.T) {
	srv, captured := startCaptureServer(t)

	s, err := newHTTP(spec.SinkCfg{Type: "http", URL: srv.URL})
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	msg := &message.Message{
		Raw:    []byte("<xml>original</xml>"),
		Fields: map[string]interface{}{"a": "b"},
		Meta:   map[string]string{"m": "1"},
	}
	if err := s.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(captured.body, &decoded); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	fields, ok := decoded["fields"].(map[string]interface{})
	if !ok || fields["a"] != "b" {
		t.Errorf("decoded fields = %v, want {a: b}", decoded["fields"])
	}
	meta, ok := decoded["meta"].(map[string]interface{})
	if !ok || meta["m"] != "1" {
		t.Errorf("decoded meta = %v, want {m: 1}", decoded["meta"])
	}
}

func TestNewHTTPRejectsInvalidMethod(t *testing.T) {
	if _, err := newHTTP(spec.SinkCfg{Type: "http", URL: "http://example.com", Method: "TRACE"}); err == nil {
		t.Fatal("expected error for unsupported method, got nil")
	}
}

func TestNewHTTPRejectsInvalidBodyMode(t *testing.T) {
	if _, err := newHTTP(spec.SinkCfg{Type: "http", URL: "http://example.com", BodyMode: "weird"}); err == nil {
		t.Fatal("expected error for unsupported bodyMode, got nil")
	}
}

func TestNewHTTPRejectsUnknownAuthType(t *testing.T) {
	if _, err := newHTTP(spec.SinkCfg{Type: "http", URL: "http://example.com", Auth: &spec.HTTPAuth{Type: "hmac"}}); err == nil {
		t.Fatal("expected error for unknown auth type, got nil")
	}
}

func TestNewHTTPRejectsEmptyAuthType(t *testing.T) {
	if _, err := newHTTP(spec.SinkCfg{Type: "http", URL: "http://example.com", Auth: &spec.HTTPAuth{}}); err == nil {
		t.Fatal("expected error for empty auth type on non-nil Auth, got nil")
	}
}
