// Package traffic generates identified messages over TCP, UDP, QUIC, or NATS and
// verifies delivery with an HTTP receiver that counts them by id, so a chaos
// scenario can state exactly how many messages were sent, delivered,
// duplicated, and lost across a restart.
package traffic

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Receiver is an HTTP sink target. It accepts the HTTP sink's wrapped body
// ({"fields": {...}, "meta": {...}}) or a raw JSON object and records the
// "id" field.
type Receiver struct {
	srv      *http.Server
	ln       net.Listener
	mu       sync.Mutex
	seen     map[string]int
	firstAt  map[string]time.Time
	requests atomic.Int64
	bytes    atomic.Int64
	// Delay stalls every request (slow sink). Blackhole accepts the TCP
	// connection but never answers until Blackhole is cleared.
	delay      atomic.Int64 // nanoseconds
	blackhole  atomic.Bool
	failWith   atomic.Int32 // HTTP status to answer with, 0 = 200
	dropConn   atomic.Bool  // close the TCP connection without answering
	lastAt     atomic.Int64
	lastBody   []byte
	byID       map[string]map[string]any
	keepFields bool
}

// StartReceiver listens on addr ("127.0.0.1:0" picks a port).
func StartReceiver(addr string) (*Receiver, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	r := &Receiver{ln: ln, seen: map[string]int{}, firstAt: map[string]time.Time{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", r.handle)
	mux.HandleFunc("/stats", r.stats)
	r.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go r.srv.Serve(ln)
	return r, nil
}

// URL is the base URL (http://host:port).
func (r *Receiver) URL() string { return "http://" + r.ln.Addr().String() }

// Addr is host:port.
func (r *Receiver) Addr() string { return r.ln.Addr().String() }

// SetDelay stalls each request by d (0 clears).
func (r *Receiver) SetDelay(d time.Duration) { r.delay.Store(int64(d)) }

// SetBlackhole makes requests hang until cleared.
func (r *Receiver) SetBlackhole(on bool) { r.blackhole.Store(on) }

// SetDrop closes every connection without answering (a sink that resets),
// so each send fails immediately and is counted as a dispatch error.
func (r *Receiver) SetDrop(on bool) { r.dropConn.Store(on) }

// SetFailStatus answers every request with status (0 restores 200).
func (r *Receiver) SetFailStatus(status int) { r.failWith.Store(int32(status)) }

func (r *Receiver) handle(w http.ResponseWriter, req *http.Request) {
	r.requests.Add(1)
	r.lastAt.Store(time.Now().UnixNano())
	body, err := io.ReadAll(io.LimitReader(req.Body, 4<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.bytes.Add(int64(len(body)))
	if r.dropConn.Load() {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				conn.Close()
				return
			}
		}
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if r.blackhole.Load() {
		for r.blackhole.Load() {
			select {
			case <-req.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	if d := time.Duration(r.delay.Load()); d > 0 {
		select {
		case <-req.Context().Done():
			return
		case <-time.After(d):
		}
	}
	if status := int(r.failWith.Load()); status != 0 {
		w.WriteHeader(status)
		return
	}
	if id := extractID(body); id != "" {
		r.mu.Lock()
		r.seen[id]++
		if _, ok := r.firstAt[id]; !ok {
			r.firstAt[id] = time.Now()
		}
		r.lastBody = body
		if r.keepFields {
			if r.byID == nil {
				r.byID = map[string]map[string]any{}
			}
			r.byID[id] = extractFields(body)
		}
		r.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

// KeepFields makes the receiver remember every message's fields (for
// asserting that an edit changed what is delivered). Memory grows with ids.
func (r *Receiver) KeepFields(on bool) {
	r.mu.Lock()
	r.keepFields = on
	r.mu.Unlock()
}

// Fields returns the fields delivered for id (KeepFields must be on).
func (r *Receiver) Fields(id string) (map[string]any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.byID[id]
	return f, ok
}

// LastFields returns the fields of the most recent delivery.
func (r *Receiver) LastFields() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return extractFields(r.lastBody)
}

func extractFields(body []byte) map[string]any {
	var wrapped struct {
		Fields map[string]any `json:"fields"`
	}
	if json.Unmarshal(body, &wrapped) != nil || wrapped.Fields == nil {
		var flat map[string]any
		_ = json.Unmarshal(body, &flat)
		return flat
	}
	return wrapped.Fields
}

func extractID(body []byte) string {
	var wrapped struct {
		Fields map[string]any `json:"fields"`
		ID     any            `json:"id"`
	}
	if json.Unmarshal(body, &wrapped) != nil {
		// Newline-delimited raw bodies (TCP/UDP sinks relayed) are not expected.
		return ""
	}
	if wrapped.Fields != nil {
		if id, ok := wrapped.Fields["id"]; ok {
			return stringify(id)
		}
	}
	if wrapped.ID != nil {
		return stringify(wrapped.ID)
	}
	return ""
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		data, _ := json.Marshal(t)
		return string(data)
	default:
		data, _ := json.Marshal(t)
		return strings.Trim(string(data), `"`)
	}
}

// Stats is the receiver's tally.
type Stats struct {
	Requests   int64 `json:"requests"`
	Bytes      int64 `json:"bytes"`
	Unique     int   `json:"unique"`
	Duplicates int   `json:"duplicates"`
}

// Stats returns the tally.
func (r *Receiver) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	dups := 0
	for _, n := range r.seen {
		if n > 1 {
			dups += n - 1
		}
	}
	return Stats{Requests: r.requests.Load(), Bytes: r.bytes.Load(), Unique: len(r.seen), Duplicates: dups}
}

// Seen reports whether id was delivered.
func (r *Receiver) Seen(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[id] > 0
}

// Missing returns the ids in sent that never arrived, sorted.
func (r *Receiver) Missing(sent []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, id := range sent {
		if r.seen[id] == 0 {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// LastDelivery is when the most recent request arrived (zero: never).
func (r *Receiver) LastDelivery() time.Time {
	n := r.lastAt.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// FirstSeen is when id first arrived.
func (r *Receiver) FirstSeen(id string) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.firstAt[id]
	return t, ok
}

// Reset forgets every id.
func (r *Receiver) Reset() {
	r.mu.Lock()
	r.seen = map[string]int{}
	r.firstAt = map[string]time.Time{}
	r.mu.Unlock()
	r.requests.Store(0)
	r.bytes.Store(0)
}

func (r *Receiver) stats(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(r.Stats())
}

// Close stops the receiver.
func (r *Receiver) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.srv.Shutdown(ctx)
}

// WaitFor polls until at least n unique ids arrived or ctx ends.
func (r *Receiver) WaitFor(ctx context.Context, n int) error {
	for {
		if r.Stats().Unique >= n {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// WaitQuiet returns once no request has arrived for idle, or ctx ends.
func (r *Receiver) WaitQuiet(ctx context.Context, idle time.Duration) error {
	for {
		last := r.LastDelivery()
		if !last.IsZero() && time.Since(last) >= idle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(idle / 4):
		}
	}
}
