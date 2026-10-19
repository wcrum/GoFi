package retry

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

func TestDelayIsCappedAndGrows(t *testing.T) {
	p := Policy{Initial: 100 * time.Millisecond, Max: time.Second, Multiplier: 2, Jitter: 0}
	want := []time.Duration{100e6, 200e6, 400e6, 800e6, 1e9, 1e9, 1e9}
	for i, w := range want {
		if got := p.Delay(i + 1); got != w {
			t.Errorf("Delay(%d) = %v, want %v", i+1, got, w)
		}
	}
	j := Policy{Initial: time.Second, Max: time.Second, Jitter: 0.5}
	for i := 0; i < 100; i++ {
		d := j.Delay(1)
		if d < 500*time.Millisecond || d > 1500*time.Millisecond {
			t.Fatalf("jittered delay out of range: %v", d)
		}
	}
	var zero Policy
	if d := zero.Delay(1); d < 160*time.Millisecond || d > 240*time.Millisecond {
		t.Errorf("default initial delay = %v", d)
	}
}

func TestDoRetriesUntilSuccess(t *testing.T) {
	var calls int32
	var retries []int
	err := Do(context.Background(), Policy{Initial: time.Millisecond, Max: 2 * time.Millisecond, Jitter: 0}, func(context.Context) error {
		if atomic.AddInt32(&calls, 1) < 4 {
			return errors.New("not yet")
		}
		return nil
	}, func(attempt int, err error, next time.Duration) { retries = append(retries, attempt) })
	if err != nil || calls != 4 || len(retries) != 3 {
		t.Fatalf("err=%v calls=%d retries=%v", err, calls, retries)
	}
}

func TestDoStopsOnContextAndPermanent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	boom := errors.New("down")
	err := Do(ctx, Policy{Initial: time.Millisecond, Max: 5 * time.Millisecond}, func(context.Context) error { return boom }, nil)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, boom) {
		t.Fatalf("want deadline wrapping last error, got %v", err)
	}

	var calls int
	err = Do(context.Background(), Policy{Initial: time.Millisecond}, func(context.Context) error { calls++; return Permanent(boom) }, nil)
	if calls != 1 || !errors.Is(err, boom) || !IsPermanent(err) {
		t.Fatalf("permanent: calls=%d err=%v", calls, err)
	}

	calls = 0
	err = Do(context.Background(), Policy{Initial: time.Millisecond, MaxAttempts: 3}, func(context.Context) error { calls++; return boom }, nil)
	if calls != 3 || err == nil || !strings.Contains(err.Error(), "gave up after 3") {
		t.Fatalf("max attempts: calls=%d err=%v", calls, err)
	}
}

func TestBackoffResets(t *testing.T) {
	b := Backoff{Policy: Policy{Initial: time.Millisecond, Max: 8 * time.Millisecond, Jitter: 0}}
	b.Next()
	b.Next()
	if d := b.Next(); d != 4*time.Millisecond || b.Attempt() != 3 {
		t.Fatalf("third delay %v attempt %d", d, b.Attempt())
	}
	b.Reset()
	if d := b.Next(); d != time.Millisecond {
		t.Fatalf("after reset %v", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Sleep(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// flapping is an HTTP receiver that is unreachable (listener closed) for
// stretches, then comes back on the same address, like a gateway on a
// cellular link.
type flapping struct {
	t        *testing.T
	addr     string
	received atomic.Int64
	srv      *http.Server
	ln       net.Listener
}

func newFlapping(t *testing.T) *flapping {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &flapping{t: t, addr: ln.Addr().String()}
	f.ln = ln
	f.serve()
	return f
}

func (f *flapping) serve() {
	f.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.received.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})}
	go f.srv.Serve(f.ln)
}

func (f *flapping) down() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = f.srv.Shutdown(ctx)
	_ = f.ln.Close()
}

func (f *flapping) up() {
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.ln, err = net.Listen("tcp", f.addr)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		f.t.Fatalf("relisten: %v", err)
	}
	f.serve()
}

func TestSinkSurvivesFlappingReceiver(t *testing.T) {
	f := newFlapping(t)
	defer f.down()

	raw, err := sink.New(spec.SinkCfg{Type: "http", URL: "http://" + f.addr + "/ingest", BodyMode: "raw"}, "flap-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := WrapSink(ctx, raw, Policy{Initial: 5 * time.Millisecond, Max: 50 * time.Millisecond, Jitter: 0.2})
	s.Flow, s.Kind = "flap", "http"

	send := func(i int) {
		if err := s.Send(&message.Message{Raw: []byte{byte(i)}}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	// Up: deliveries succeed immediately.
	for i := 0; i < 5; i++ {
		send(i)
	}
	// Down for a while: a send started now must block and retry, not fail.
	f.down()
	done := make(chan struct{})
	go func() {
		defer close(done)
		send(5)
	}()
	select {
	case <-done:
		t.Fatal("send succeeded while the receiver was down")
	case <-time.After(150 * time.Millisecond):
	}
	f.up()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("send did not recover after the receiver came back")
	}
	// Flap several more times under a steady stream.
	for round := 0; round < 3; round++ {
		f.down()
		go func() { time.Sleep(40 * time.Millisecond); f.up() }()
		for i := 0; i < 3; i++ {
			send(10 + round*3 + i)
		}
	}
	if got := f.received.Load(); got < 15 {
		t.Fatalf("receiver saw %d deliveries, want >= 15", got)
	}
}

func TestSinkPermanentErrorsAreNotRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	raw, err := sink.New(spec.SinkCfg{Type: "http", URL: srv.URL, BodyMode: "raw"}, "perm")
	if err != nil {
		t.Fatal(err)
	}
	s := WrapSink(context.Background(), raw, Policy{Initial: time.Millisecond, Max: time.Millisecond})
	s.Permanent = func(err error) bool {
		var se *sink.StatusError
		return errors.As(err, &se) && se.HTTPStatus() >= 400 && se.HTTPStatus() < 500
	}
	if err := s.Send(&message.Message{Raw: []byte("x")}); err == nil || hits.Load() != 1 {
		t.Fatalf("err=%v hits=%d", err, hits.Load())
	}
}

// TestDNSFailureIsTransient: a hostname that cannot resolve is just another
// retryable failure; the sink never panics or gives up before ctx ends.
func TestDNSFailureIsTransient(t *testing.T) {
	// A real lookup can take seconds on hosts whose resolver is slow to answer
	// NXDOMAIN; fail resolution immediately so the test exercises retry, not DNS.
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "no-such-host.invalid", IsNotFound: true}
	}}
	t.Cleanup(func() { net.DefaultResolver = previous })
	raw, err := sink.New(spec.SinkCfg{Type: "http", URL: "http://no-such-host.invalid:1/x", BodyMode: "raw"}, "dns")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var attempts int32
	s := WrapSink(ctx, raw, Policy{Initial: time.Millisecond, Max: 20 * time.Millisecond})
	s.Next = countingSink{raw, &attempts}
	err = s.Send(&message.Message{Raw: []byte("x")})
	if !errors.Is(err, context.DeadlineExceeded) || atomic.LoadInt32(&attempts) < 2 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

type countingSink struct {
	sink.Sink
	n *int32
}

func (c countingSink) Send(m *message.Message) error {
	atomic.AddInt32(c.n, 1)
	return c.Sink.Send(m)
}
