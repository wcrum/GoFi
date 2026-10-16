package source

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/wcrum/gofi/spec"
)

type collector struct {
	mu     sync.Mutex
	frames []string
	got    chan struct{}
}

func newCollector() *collector { return &collector{got: make(chan struct{}, 100)} }
func (c *collector) emit(_ context.Context, data []byte, _ string) {
	c.mu.Lock()
	c.frames = append(c.frames, string(data))
	c.mu.Unlock()
	c.got <- struct{}{}
}
func (c *collector) wait(t *testing.T, n int) []string {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-c.got:
		case <-time.After(5 * time.Second):
			t.Fatalf("got %d of %d frames: %q", i, n, c.frames)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.frames...)
}

func TestFetchPollsOnIntervalWithAuthAndSplits(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := polls.Add(1)
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-Feed") != "a" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if n == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable) // retried at the next interval
			return
		}
		fmt.Fprintf(w, `{"data":{"items":[{"id":%d},{"id":%d}]}}`, n*10, n*10+1)
	}))
	defer server.Close()
	src := spec.FlowSource{Fetch: &spec.FetchSource{Split: "$.data.items[*]", HTTP: &spec.HTTPFetch{URL: server.URL, Every: "1s", Headers: map[string]string{"X-Feed": "a"}, Auth: &spec.HTTPAuth{Type: "bearer", Token: "secret"}}}}
	if err := src.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	go Run(ctx, "f", src, c.emit)
	frames := c.wait(t, 2)
	if frames[0] != `{"id":20}` || frames[1] != `{"id":21}` {
		t.Fatalf("frames %q", frames)
	}
}

func TestFetchWithoutSplitEmitsWholeBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		fmt.Fprint(w, "line1\nline2")
	}))
	defer server.Close()
	c := newCollector()
	err := poll(context.Background(), server.Client(), &spec.HTTPFetch{URL: server.URL, Method: "post"}, nil, false, c.emit)
	if err != nil || c.wait(t, 1)[0] != "line1\nline2" {
		t.Fatalf("err %v frames %q", err, c.frames)
	}
}

func TestSplitJSONReportsPathProblems(t *testing.T) {
	for _, tc := range []struct {
		body string
		keys []string
	}{{`{"items":{}}`, []string{"items"}}, {`{"other":[]}`, []string{"items"}}, {`not json`, nil}, {`[1]`, []string{"items"}}} {
		if _, err := SplitJSON([]byte(tc.body), tc.keys); err == nil {
			t.Errorf("%s %v: expected an error", tc.body, tc.keys)
		}
	}
	items, err := SplitJSON([]byte(`[1,"a",{"b":2}]`), []string{})
	if err != nil || len(items) != 3 || string(items[1]) != `"a"` {
		t.Fatalf("%q %v", items, err)
	}
}

func TestWebSocketSendsSubscribeAndEmitsMessagesAcrossReconnects(t *testing.T) {
	backoff.min, backoff.max = 10*time.Millisecond, 20*time.Millisecond
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" {
			http.Error(w, "no key", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_, hello, err := conn.Read(r.Context())
		if err != nil || string(hello) != `{"subscribe":"all"}` {
			return
		}
		n := connections.Add(1)
		conn.Write(r.Context(), websocket.MessageText, []byte(fmt.Sprintf("text-%d", n)))
		conn.Write(r.Context(), websocket.MessageBinary, []byte{0, byte(n)})
		conn.Close(websocket.StatusNormalClosure, "bye") // forces a reconnect
	}))
	defer server.Close()
	src := spec.FlowSource{Subscribe: &spec.SubscribeSource{WebSocket: &spec.StreamSubscribe{URL: "ws" + server.URL[4:], Send: `{"subscribe":"all"}`, Auth: &spec.HTTPAuth{Type: "apiKey", APIKey: "k"}}}}
	if err := src.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	go Run(ctx, "ws", src, c.emit)
	frames := c.wait(t, 4)
	if frames[0] != "text-1" || frames[1] != "\x00\x01" || frames[2] != "text-2" {
		t.Fatalf("frames %q", frames)
	}
}

func TestSSEJoinsDataLinesAndResumesWithLastEventID(t *testing.T) {
	backoff.min, backoff.max = 10*time.Millisecond, 20*time.Millisecond
	var resumed atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if id := r.Header.Get("Last-Event-ID"); id != "" {
			resumed.Store(id)
			fmt.Fprint(w, "data: after "+id+"\n\n")
			return
		}
		fmt.Fprint(w, ": comment\nid: 7\nevent: reading\ndata: {\"a\":1,\ndata: \"b\":2}\n\ndata:second\n\n")
	}))
	defer server.Close()
	src := spec.FlowSource{Subscribe: &spec.SubscribeSource{SSE: &spec.StreamSubscribe{URL: server.URL}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	go Run(ctx, "sse", src, c.emit)
	frames := c.wait(t, 3)
	if frames[0] != "{\"a\":1,\n\"b\":2}" || frames[1] != "second" || frames[2] != "after 7" || resumed.Load() != "7" {
		t.Fatalf("frames %q resumed %v", frames, resumed.Load())
	}
}
