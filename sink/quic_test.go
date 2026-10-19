package sink

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/wcrum/gofi/internal/quictls"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// quicServer is an in-process stream-oriented QUIC destination: every line
// read on any stream goes to lines; streams counts the streams opened.
type quicServer struct {
	pc      net.PacketConn
	ln      *quic.Listener
	lines   chan string
	streams atomic.Int32
	conns   chan *quic.Conn
}

func startQUICServer(t *testing.T, addr string, tlsConf *tls.Config) *quicServer {
	t.Helper()
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := quic.Listen(pc, tlsConf, &quic.Config{MaxIdleTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s := &quicServer{pc: pc, ln: ln, lines: make(chan string, 64), conns: make(chan *quic.Conn, 8)}
	go func() {
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				return
			}
			select {
			case s.conns <- conn:
			default:
			}
			go func() {
				for {
					stream, err := conn.AcceptStream(context.Background())
					if err != nil {
						return
					}
					s.streams.Add(1)
					go func() {
						sc := bufio.NewScanner(stream)
						for sc.Scan() {
							s.lines <- sc.Text()
						}
						stream.Close()
					}()
				}
			}()
		}
	}()
	t.Cleanup(s.stop)
	return s
}

func (s *quicServer) addr() string { return s.pc.LocalAddr().String() }

// stop tears the server down without closing its connections first, as a
// crashed or restarted destination would.
func (s *quicServer) stop() {
	s.ln.Close()
	s.pc.Close()
}

func (s *quicServer) expect(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-s.lines:
		if got != want {
			t.Fatalf("received %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%q not received", want)
	}
}

// newQUICSink builds a quic sink for cfg and returns the concrete type so
// tests can shorten its liveness timers.
func newQUICSink(t *testing.T, cfg spec.SinkCfg) *quicSink {
	t.Helper()
	sk, err := newQUIC(cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	q, ok := sk.(*quicSink)
	if !ok {
		t.Fatalf("newQUIC returned %T, want *quicSink", sk)
	}
	return q
}

func selfSignedServerTLS(t *testing.T) *tls.Config {
	t.Helper()
	pair, err := quictls.SelfSigned("dest")
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, NextProtos: []string{spec.DefaultQUICALPN}, MinVersion: tls.VersionTLS13}
}

func TestQUICSinkSendsOnOneLongLivedStream(t *testing.T) {
	srv := startQUICServer(t, "127.0.0.1:0", selfSignedServerTLS(t))
	sk, err := New(spec.SinkCfg{Type: "quic", URL: "quic://" + srv.addr(), Delimiter: "\n", TLS: &spec.TLSConfig{InsecureSkipVerify: true}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer sk.Close()
	raw := []byte("payload")
	for i := 0; i < 3; i++ {
		if err := sk.Send(&message.Message{Raw: raw}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		srv.expect(t, "payload")
	}
	if string(raw) != "payload" {
		t.Fatal("sink modified message bytes")
	}
	if n := srv.streams.Load(); n != 1 {
		t.Fatalf("messages used %d streams, want 1", n)
	}
	// Flush finishes the stream and waits for the destination's FIN; the
	// next send opens a new stream on the same connection.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Flush(ctx, sk); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := sk.Send(&message.Message{Raw: []byte("after")}); err != nil {
		t.Fatal(err)
	}
	srv.expect(t, "after")
	if n := srv.streams.Load(); n != 2 {
		t.Fatalf("streams after flush = %d, want 2", n)
	}
}

// The destination closing the connection is noticed on the next send,
// which redials at once; a destination that vanished is noticed when
// keep-alives go unanswered, and dials back off until it returns.
func TestQUICSinkReconnects(t *testing.T) {
	tlsConf := selfSignedServerTLS(t)
	srv := startQUICServer(t, "127.0.0.1:0", tlsConf)
	addr := srv.addr()
	sk := newQUICSink(t, spec.SinkCfg{Type: "quic", URL: "quic://" + addr, Delimiter: "\n", TLS: &spec.TLSConfig{InsecureSkipVerify: true}})
	defer sk.Close()
	// Short liveness timers keep the test fast; production uses the
	// package constants.
	sk.quicConf.MaxIdleTimeout = 500 * time.Millisecond
	sk.quicConf.KeepAlivePeriod = 100 * time.Millisecond
	sk.quicConf.HandshakeIdleTimeout = 300 * time.Millisecond

	if err := sk.Send(&message.Message{Raw: []byte("one")}); err != nil {
		t.Fatal(err)
	}
	srv.expect(t, "one")

	// 1. The destination closes the connection: once the close has arrived
	// (a write before that succeeds locally, as on TCP), the next send
	// reconnects.
	conn := <-srv.conns
	conn.CloseWithError(7, "going away")
	sk.mu.Lock()
	closed := sk.conn
	sk.mu.Unlock()
	select {
	case <-closed.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("remote close not noticed")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := sk.Send(&message.Message{Raw: []byte("two")})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("send after remote close: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	srv.expect(t, "two")

	// 2. The destination vanishes: the idle timeout fires, sends fail with
	// backoff, and the sink reconnects once it is back on the same port.
	srv.stop()
	sk.mu.Lock()
	dead := sk.conn
	sk.mu.Unlock()
	select {
	case <-dead.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("dead destination not noticed")
	}
	var failed int
	for i := 0; i < 3; i++ {
		if err := sk.Send(&message.Message{Raw: []byte("lost")}); err != nil {
			failed++
			if !Retryable(err) {
				t.Fatalf("send to a dead destination is not retryable: %v", err)
			}
		}
	}
	if failed == 0 {
		t.Fatal("sends to a dead destination succeeded")
	}
	sk.mu.Lock()
	retryAt, failures := sk.retryAt, sk.failures
	sk.mu.Unlock()
	if failures == 0 || retryAt.IsZero() {
		t.Fatalf("no backoff scheduled: failures=%d retryAt=%v", failures, retryAt)
	}

	srv = startQUICServer(t, addr, tlsConf)
	deadline = time.Now().Add(10 * time.Second)
	for {
		err := sk.Send(&message.Message{Raw: []byte("three")})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reconnection: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	srv.expect(t, "three")
}

// Server certificates are verified by default; a CA pins the issuer and a
// certificate from another CA (or a self-signed one) is rejected.
func TestQUICSinkVerifiesServerCertificate(t *testing.T) {
	ca, err := quictls.NewCA("dest-ca")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := quictls.NewCA("other-ca")
	cert, key, _ := ca.Issue("dest", []string{"127.0.0.1", "dest.example"}, false)
	pair, _ := tls.X509KeyPair([]byte(cert), []byte(key))
	srv := startQUICServer(t, "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}, NextProtos: []string{spec.DefaultQUICALPN}, MinVersion: tls.VersionTLS13})

	// Close right after Send: the sink must let the destination finish the
	// stream before the connection goes, or the message is discarded.
	send := func(tlsCfg *spec.TLSConfig) error {
		sk := newQUICSink(t, spec.SinkCfg{Type: "quic", URL: "quic://" + srv.addr(), Delimiter: "\n", TLS: tlsCfg})
		defer sk.Close()
		sk.quicConf.HandshakeIdleTimeout = time.Second
		return sk.Send(&message.Message{Raw: []byte("hello")})
	}
	// Default: system roots do not know the private CA.
	if err := send(nil); err == nil || !isCertError(err) {
		t.Fatalf("default verification: %v", err)
	}
	// Wrong CA.
	if err := send(&spec.TLSConfig{CA: other.CertPEM()}); err == nil || !isCertError(err) {
		t.Fatalf("wrong CA: %v", err)
	}
	// Right CA, wrong name.
	if err := send(&spec.TLSConfig{CA: ca.CertPEM(), ServerName: "elsewhere.example"}); err == nil || !isCertError(err) {
		t.Fatalf("wrong server name: %v", err)
	}
	// Right CA (name defaults to the URL host) delivers.
	if err := send(&spec.TLSConfig{CA: ca.CertPEM()}); err != nil {
		t.Fatalf("right CA: %v", err)
	}
	srv.expect(t, "hello")
	if err := send(&spec.TLSConfig{CA: ca.CertPEM(), ServerName: "dest.example"}); err != nil {
		t.Fatalf("right CA with server name: %v", err)
	}
	srv.expect(t, "hello")
}

func isCertError(err error) bool {
	var transport *quic.TransportError
	if errors.As(err, &transport) && transport.ErrorCode.IsCryptoError() {
		return true
	}
	text := err.Error()
	return strings.Contains(text, "certificate") || strings.Contains(text, "x509") || strings.Contains(text, "CRYPTO_ERROR")
}

func TestQUICSinkValidation(t *testing.T) {
	bad := []spec.SinkCfg{
		{Type: "quic", URL: "tcp://host:1234"},
		{Type: "quic", URL: "quic://host"},
		{Type: "quic", URL: "quic://host:1234/path"},
		{Type: "quic", URL: "quic://host:1234", TLS: &spec.TLSConfig{RequireClientCert: true}},
		{Type: "quic", URL: "quic://host:1234", TLS: &spec.TLSConfig{CA: "junk"}},
		{Type: "quic", URL: "quic://host:1234", TLS: &spec.TLSConfig{Cert: "x"}},
		{Type: "quic", URL: "quic://host:1234", TLS: &spec.TLSConfig{CA: "x", InsecureSkipVerify: true}},
		{Type: "quic", URL: "quic://host:1234", BodyMode: "json"},
		{Type: "quic", URL: "quic://host:1234", Kubernetes: &spec.KubernetesTarget{Selector: map[string]string{"app": "x"}, Port: 1}},
	}
	for _, cfg := range bad {
		if err := Validate(cfg); err == nil {
			t.Errorf("accepted %+v", cfg)
		}
		if _, err := New(cfg, "test"); err == nil {
			t.Errorf("New accepted %+v", cfg)
		}
	}
	good := spec.SinkCfg{Type: "quic", URL: "quic://dest.example:4433", Delimiter: "\n", TLS: &spec.TLSConfig{ServerName: "dest.example", ALPN: []string{"gofi/1"}}}
	if err := Validate(good); err != nil {
		t.Fatalf("Validate(good): %v", err)
	}
	sk, err := New(good, "test") // builds without dialing
	if err != nil {
		t.Fatalf("New(good): %v", err)
	}
	sk.Close()

	// A Kubernetes selector target is addressed through the target Service
	// like tcp/udp: one sink for delivery "one", a fan-out otherwise.
	t.Setenv("GOFI_K8S_NAMESPACE", "apps")
	target := &spec.KubernetesTarget{Selector: map[string]string{"app": "receiver"}, Port: 4433, Delivery: "one"}
	k8s := spec.SinkCfg{Type: "quic", Kubernetes: target, Delimiter: "\n", TLS: &spec.TLSConfig{ServerName: "receiver.apps"}}
	if err := Validate(k8s); err != nil {
		t.Fatalf("Validate(kubernetes): %v", err)
	}
	single := newQUICSink(t, k8s)
	if want := spec.KubernetesServiceName("test", "quic", target) + ".apps.svc.cluster.local:4433"; single.address != want || single.tlsConf.ServerName != "receiver.apps" {
		t.Fatalf("selector target: address %q serverName %q", single.address, single.tlsConf.ServerName)
	}
	single.Close()
	target.Delivery = ""
	fan, err := newQUIC(k8s, "test")
	if f, ok := fan.(*fanoutSink); err != nil || !ok || f.network != "quic" || f.port != "4433" {
		t.Fatalf("fan-out target: %T %v", fan, err)
	}
	fan.Close()
}
