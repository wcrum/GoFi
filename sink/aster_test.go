package sink

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func writeAsterReceipt(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"id": bytes.Repeat([]byte{1}, 32), "publisher": bytes.Repeat([]byte{2}, 32),
		"publisherCounter": "1", "eventSequence": "1", "acceptanceMarker": "1", "priority": "PRIORITY_ROUTINE", "inserted": true,
	}); err != nil {
		t.Error(err)
	}
}

func asterTestConfig(url string) spec.SinkCfg {
	return spec.SinkCfg{Type: "aster", URL: url, Aster: &spec.AsterCfg{
		Topic: "application.traffic", Scope: "mission/apps", Token: "test-bearer-token", LogicalKeyField: "app",
	}}
}

func TestAsterConnectPublicationAndStableRetryIdentity(t *testing.T) {
	var publications []asterPublication
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != asterPublishPath {
			t.Errorf("unexpected RPC: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-bearer-token" || len(r.Header.Values("Authorization")) != 1 {
			t.Error("missing or duplicated authorization")
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Connect-Protocol-Version") != "1" || r.Header.Get("Connect-Timeout-Ms") != "10000" {
			t.Errorf("invalid Connect headers: %#v", r.Header)
		}
		var publication asterPublication
		if err := json.NewDecoder(r.Body).Decode(&publication); err != nil {
			t.Error(err)
		}
		publications = append(publications, publication)
		if len(publications) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"unavailable","message":"private upstream diagnostics"}`))
			return
		}
		writeAsterReceipt(t, w)
	}))
	defer server.Close()
	s, err := New(asterTestConfig(server.URL), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	msg := message.New([]byte{0, 0xff, 'x'}, "test", "peer")
	msg.Fields["app"] = "payments"
	if err := s.Send(msg); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("expected sanitized rejection, got %v", err)
	}
	if err := s.Send(msg); err != nil {
		t.Fatal(err)
	}
	if len(publications) != 2 || !bytes.Equal(publications[0].OperationKey, publications[1].OperationKey) || len(publications[0].OperationKey) != 32 {
		t.Fatalf("retry operation identity changed: %#v", publications)
	}
	got := publications[1]
	if !bytes.Equal(got.Payload, msg.Raw) || string(got.LogicalKey) != "payments" || got.Topic != "application.traffic" || got.Scope != "mission/apps" || got.Priority != "PRIORITY_ROUTINE" {
		t.Fatalf("unexpected publication: %#v", got)
	}
	newMsg := message.New(msg.Raw, "test", "peer")
	newMsg.Fields["app"] = "payments"
	if err := s.Send(newMsg); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(publications[1].OperationKey, publications[2].OperationKey) {
		t.Fatal("different ingest events reused an operation key")
	}
}

func TestAsterWrappedPayloadTokenRotationAndNoRedirect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("first-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var tokens []string
	redirect := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		if redirect {
			http.Redirect(w, r, "http://192.0.2.1:8181/forbidden", http.StatusTemporaryRedirect)
			return
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		decoded, err := base64.StdEncoding.DecodeString(body["payload"].(string))
		if err != nil || !bytes.Contains(decoded, []byte(`"app":"payments"`)) {
			t.Errorf("wrapped payload = %s, %v", decoded, err)
		}
		writeAsterReceipt(t, w)
	}))
	defer server.Close()
	cfg := asterTestConfig(server.URL)
	cfg.Aster.Token = ""
	t.Setenv("GOFI_ASTER_TOKEN_FILE", path)
	cfg.BodyMode = "wrapped"
	s, err := New(cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	msg := message.New([]byte("wire"), "test", "peer")
	msg.Fields["app"] = "payments"
	if err := s.Send(msg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(msg); err != nil {
		t.Fatal(err)
	}
	if tokens[0] != "Bearer first-token" || tokens[1] != "Bearer second-token" {
		t.Fatalf("token mount did not rotate: %#v", tokens)
	}
	redirect = true
	if err := s.Send(msg); err == nil {
		t.Fatal("agent redirect must be rejected")
	}
}

func TestAsterRejectsRemoteOriginsAndInvalidConfig(t *testing.T) {
	for _, origin := range []string{
		"http://aster.svc:8181", "http://localhost:8181", "http://192.0.2.1:8181", "https://127.0.0.1:8181",
		"http://user:secret@127.0.0.1:8181", "http://127.0.0.1:8181/path", "http://127.0.0.1:8181?token=secret", "http://127.0.0.1:0",
	} {
		if _, err := New(asterTestConfig(origin), "test"); err == nil {
			t.Errorf("accepted non-local agent origin %q", origin)
		}
	}
	for _, mutate := range []func(*spec.SinkCfg){
		func(cfg *spec.SinkCfg) { cfg.Aster = nil },
		func(cfg *spec.SinkCfg) { cfg.Aster.Topic = "" },
		func(cfg *spec.SinkCfg) { cfg.Aster.Scope = "" },
		func(cfg *spec.SinkCfg) { cfg.Aster.Priority = "unknown" },
		func(cfg *spec.SinkCfg) { cfg.Aster.Token = "bad\nheader" },
		func(cfg *spec.SinkCfg) { cfg.Aster.TokenFile = "/some/file" },
		func(cfg *spec.SinkCfg) { cfg.Aster.TimeoutSeconds = 31 },
		func(cfg *spec.SinkCfg) { cfg.BodyMode = "unknown" },
	} {
		cfg := asterTestConfig("http://127.0.0.1:8181")
		mutate(&cfg)
		if _, err := New(cfg, "test"); err == nil {
			t.Fatalf("accepted invalid config %#v", cfg)
		}
	}
}

func TestAsterRequiresActualPublicationReceipt(t *testing.T) {
	for _, body := range []string{"{}", `{"id":"not-base64!"}`, `{"message":"accepted"}`, "not JSON"} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			s, err := New(asterTestConfig(server.URL), "test")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			msg := message.New([]byte("wire"), "test", "peer")
			msg.Fields["app"] = "payments"
			if err := s.Send(msg); err == nil {
				t.Fatal("accepted incomplete or invalid agent receipt")
			}
		})
	}
}
