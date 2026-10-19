package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

type zenohFrame struct {
	data   string
	remote string
	meta   map[string]string
}

type zenohCollector struct {
	mu     sync.Mutex
	frames []zenohFrame
	got    chan struct{}
}

func (c *zenohCollector) emit(ctx context.Context, data []byte, remote string) {
	c.mu.Lock()
	c.frames = append(c.frames, zenohFrame{string(data), remote, message.SourceMeta(ctx)})
	c.mu.Unlock()
	c.got <- struct{}{}
}

func (c *zenohCollector) wait(t *testing.T, n int) []zenohFrame {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-c.got:
		case <-time.After(5 * time.Second):
			t.Fatalf("got %d of %d frames: %+v", i, n, c.frames)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]zenohFrame(nil), c.frames...)
}

// zenohRouter emulates the REST plugin: GET /<keyExpr> with Accept:
// text/event-stream streams samples as SSE events named by kind; any other
// GET answers a JSON array of samples (a storage query).
func zenohRouter(t *testing.T, events string, query string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/demo/sensors/**" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Tenant") != "a" {
			t.Errorf("unexpected request %s %s %v", r.Method, r.URL, r.Header)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, query)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, events)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the subscription stays open
	}))
}

func TestZenohSubscribesOverSSEAndDecodesSamples(t *testing.T) {
	events := "event: PUT\ndata: {\"key\":\"demo/sensors/a\",\"value\":{\"t\":21.5},\"encoding\":\"application/json\",\"timestamp\":\"7386690599959157260/33a0\"}\n\n" +
		"event: DELETE\ndata: {\"key\":\"demo/sensors/a\",\"value\":null,\"encoding\":\"zenoh/bytes\",\"timestamp\":null}\n\n" +
		"event: PUT\ndata: {\"key\":\"demo/sensors/b\",\"value\":\"hello\",\"encoding\":\"text/plain;charset=utf-8\",\"timestamp\":null}\n\n" +
		"event: PUT\ndata: {\"key\":\"demo/sensors/c\",\"value\":\"AAEC\",\"encoding\":\"zenoh/bytes\",\"timestamp\":null}\n\n"
	server := zenohRouter(t, events, "[]")
	defer server.Close()
	cfg := &spec.ZenohSubscribe{URL: server.URL, KeyExpr: "demo/sensors/**", Auth: &spec.HTTPAuth{Type: "bearer", Token: "token"}, Headers: map[string]string{"X-Tenant": "a"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &zenohCollector{got: make(chan struct{}, 100)}
	go Run(ctx, "z", spec.FlowSource{Subscribe: &spec.SubscribeSource{Zenoh: cfg}}, c.emit)
	frames := c.wait(t, 3) // the DELETE is filtered by the default kinds (PUT)
	if frames[0].data != `{"t":21.5}` || frames[1].data != "hello" || frames[2].data != "\x00\x01\x02" {
		t.Fatalf("payloads %+v", frames)
	}
	first := frames[0].meta
	if frames[0].remote != "zenoh:demo/sensors/**" || first["zenoh.key"] != "demo/sensors/a" || first["zenoh.kind"] != "PUT" || first["zenoh.encoding"] != "application/json" || first["zenoh.timestamp"] != "7386690599959157260/33a0" {
		t.Fatalf("meta %+v remote %q", first, frames[0].remote)
	}
	if _, ok := frames[1].meta["zenoh.timestamp"]; ok {
		t.Fatalf("absent timestamp must not be set: %+v", frames[1].meta)
	}
}

func TestZenohQueriesStoragesThenForwardsDeletes(t *testing.T) {
	events := "event: DELETE\ndata: {\"key\":\"demo/sensors/a\",\"value\":null,\"encoding\":\"zenoh/bytes\",\"timestamp\":\"ts-2\"}\n\n"
	query := `[{"key":"demo/sensors/a","value":"21","encoding":"text/plain","timestamp":"ts-1"},{"key":"ERROR","value":"timeout","encoding":"text/plain","timestamp":null}]`
	server := zenohRouter(t, events, query)
	defer server.Close()
	cfg := &spec.ZenohSubscribe{URL: server.URL, KeyExpr: "demo/sensors/**", Kinds: []string{"PUT", "DELETE"}, Query: true, Auth: &spec.HTTPAuth{Type: "bearer", Token: "token"}, Headers: map[string]string{"X-Tenant": "a"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &zenohCollector{got: make(chan struct{}, 100)}
	go Run(ctx, "z", spec.FlowSource{Subscribe: &spec.SubscribeSource{Zenoh: cfg}}, c.emit)
	frames := c.wait(t, 2)
	if frames[0].data != "21" || frames[0].meta["zenoh.query"] != "true" || frames[0].meta["zenoh.kind"] != "PUT" {
		t.Fatalf("query frame %+v", frames[0])
	}
	var deleted map[string]string
	if err := json.Unmarshal([]byte(frames[1].data), &deleted); err != nil || deleted["key"] != "demo/sensors/a" || deleted["kind"] != "DELETE" || deleted["timestamp"] != "ts-2" || frames[1].meta["zenoh.kind"] != "DELETE" {
		t.Fatalf("delete frame %+v", frames[1])
	}
}

func TestZenohPayloadDecoding(t *testing.T) {
	for _, tc := range []struct {
		encoding, value string
		want            []byte
	}{
		{"application/json", `[1, 2]`, []byte(`[1, 2]`)},
		{"text/json", `"quoted"`, []byte(`"quoted"`)},
		{"text/plain", `"plain"`, []byte("plain")},
		{"zenoh/string", `"s"`, []byte("s")},
		{"application/octet-stream", `"R29GaQ=="`, []byte("GoFi")},
		{"zenoh/bytes", `"not base64!"`, []byte("not base64!")},
		{"zenoh/bytes", `null`, []byte{}},
		{"application/integer", `42`, []byte("42")},
	} {
		if got := zenohPayload(tc.encoding, json.RawMessage(tc.value)); !bytes.Equal(got, tc.want) {
			t.Errorf("%s %s: got %q want %q", tc.encoding, tc.value, got, tc.want)
		}
	}
}

func TestZenohRefusesRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.invalid/x", http.StatusFound)
	}))
	defer server.Close()
	z, err := newZenohSource("z", &spec.ZenohSubscribe{URL: server.URL, KeyExpr: "demo/a"}, func(context.Context, []byte, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := z.run(context.Background()); err == nil {
		t.Fatal("followed a router redirect")
	}
}
