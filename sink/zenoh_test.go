package sink

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestZenohRESTPublishesExactPUTKeyPayloadAndEncoding(t *testing.T) {
	raw := []byte{0, 1, 'G', 'o', 'F', 'i', '\n'}
	received := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo/temperature sensor/é" || r.URL.RawQuery != "" {
			t.Errorf("unexpected key URL: %s", r.URL)
		}
		data, _ := io.ReadAll(r.Body)
		received <- capturedRequest{method: r.Method, header: r.Header.Clone(), body: data}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	cfg := spec.SinkCfg{Type: "zenoh", URL: server.URL, Subject: "demo/temperature sensor/é", ContentType: "application/octet-stream", Auth: &spec.HTTPAuth{Type: "bearer", Token: "example"}, Headers: map[string]string{"X-Tenant": "demo"}}
	sk, err := New(cfg, "route")
	if err != nil {
		t.Fatal(err)
	}
	defer sk.Close()
	if err := sk.Send(&message.Message{Raw: raw}); err != nil {
		t.Fatal(err)
	}
	got := <-received
	if got.method != http.MethodPut || !bytes.Equal(got.body, raw) || got.header.Get("Content-Type") != "application/octet-stream" || got.header.Get("Authorization") != "Bearer example" || got.header.Get("X-Tenant") != "demo" {
		t.Fatalf("incorrect Zenoh REST request: %+v", got)
	}
	if sk.(*zenohSink).c.Timeout != 10*time.Second {
		t.Fatal("Zenoh request must have a finite timeout")
	}
}
func TestZenohRESTWrappedJSONAndAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.Header.Get("X-Zenoh-Token") != "example" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("wrong publishing headers: %s %v", r.Method, r.Header)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["fields"].(map[string]any)["temperature"] != float64(21) {
			t.Errorf("wrong wrapped payload: %+v", payload)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	sk, err := New(spec.SinkCfg{Type: "zenoh", URL: server.URL, Subject: "demo/temperature", BodyMode: "wrapped", ContentType: "application/json", Auth: &spec.HTTPAuth{Type: "apiKey", HeaderName: "X-Zenoh-Token", APIKey: "example"}}, "route")
	if err != nil {
		t.Fatal(err)
	}
	defer sk.Close()
	if err := sk.Send(&message.Message{Fields: map[string]any{"temperature": 21}, Meta: map[string]string{"name": "route"}}); err != nil {
		t.Fatal(err)
	}
}
func TestZenohRESTSurfacesRouterStatusAndRejectsRedirects(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/other-key")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			sk, err := New(spec.SinkCfg{Type: "zenoh", URL: server.URL, Subject: "demo/key"}, "route")
			if err != nil {
				t.Fatal(err)
			}
			defer sk.Close()
			if err := sk.Send(&message.Message{Raw: []byte("sample")}); err == nil {
				t.Fatalf("router status %d reported success", status)
			}
		})
	}
}
func TestZenohRESTRejectsInvalidKeysAndOriginsWithoutNetwork(t *testing.T) {
	for _, key := range []string{"", "/leading", "trailing/", "two//segments", "demo/..", "demo/.", "demo/*", "demo/$*", "demo/?x=1", "demo/#fragment", "@/admin", "demo\nkey", "demo/%2e%2e", "demo\\key"} {
		if err := Validate(spec.SinkCfg{Type: "zenoh", URL: "http://router:8000", Subject: key}); err == nil {
			t.Errorf("accepted invalid key %q", key)
		}
	}
	for _, origin := range []string{"router:8000", "zenoh://router:7447", "http://router:8000/prefix", "http://router:8000?query", "http://router:8000?", "http://router:8000#fragment", "http://user:password@router:8000"} {
		if err := Validate(spec.SinkCfg{Type: "zenoh", URL: origin, Subject: "demo/key"}); err == nil {
			t.Errorf("accepted invalid router origin %q", origin)
		}
	}
	for _, cfg := range []spec.SinkCfg{{Type: "zenoh", URL: "http://router:8000", Subject: "demo/key", Method: "POST"}, {Type: "zenoh", URL: "http://router:8000", Subject: "demo/key", Auth: &spec.HTTPAuth{Type: "unsupported"}}} {
		if err := Validate(cfg); err == nil {
			t.Fatal("accepted invalid Zenoh configuration")
		}
	}
	if err := Validate(spec.SinkCfg{Type: "zenoh", URL: "https://router.example", Subject: "demo/key", When: "123"}); err == nil || !strings.Contains(err.Error(), "bool") {
		t.Fatalf("invalid routing predicate accepted: %v", err)
	}
}

func TestZenohRESTDeleteRemovesKeyWithoutBody(t *testing.T) {
	received := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo/key" {
			t.Errorf("unexpected key URL: %s", r.URL)
		}
		data, _ := io.ReadAll(r.Body)
		received <- capturedRequest{method: r.Method, header: r.Header.Clone(), body: data}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sk, err := New(spec.SinkCfg{Type: "zenoh", URL: server.URL, Subject: "demo/key", Method: "delete"}, "route")
	if err != nil {
		t.Fatal(err)
	}
	defer sk.Close()
	if err := sk.Send(&message.Message{Raw: []byte("ignored"), Payload: []byte("ignored too")}); err != nil {
		t.Fatal(err)
	}
	if got := <-received; got.method != http.MethodDelete || len(got.body) != 0 {
		t.Fatalf("incorrect Zenoh REST delete: %+v", got)
	}
	for _, key := range []string{"demo/*", "demo/**", "@/router/local"} {
		if err := Validate(spec.SinkCfg{Type: "zenoh", URL: "http://router:8000", Subject: key, Method: "DELETE"}); err == nil {
			t.Fatalf("delete accepted non-concrete key %q", key)
		}
	}
}
