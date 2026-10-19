package listener

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/wcrum/gofi/internal/quictls"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/spec"
)

// freeUDPPort reserves and releases a UDP port; QUIC listen sources require
// an explicit port.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	pc.Close()
	return port
}

func quicFlow(t *testing.T, tlsCfg *spec.TLSConfig) spec.FlowDefinition {
	t.Helper()
	return spec.FlowDefinition{
		Name:    "quic-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")),
		Source:  spec.FlowSource{Listen: &spec.ListenSource{Network: spec.NetworkQUIC, Port: freeUDPPort(t), TLS: tlsCfg}},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Sinks:   []spec.SinkCfg{{Type: "stdout"}},
	}
}

func startQUICListener(t *testing.T, def spec.FlowDefinition, limits Limits) (*Listener, chan pipeline.TapEvent, string) {
	t.Helper()
	l, err := New(def)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.limits = limits
	t.Cleanup(l.Close)
	sub := l.Tap.Subscribe()
	t.Cleanup(func() { l.Tap.Unsubscribe(sub) })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go l.Run(ctx)
	return l, sub, fmt.Sprintf("127.0.0.1:%d", def.Source.Listening().Port)
}

func clientTLS(alpn string) *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, NextProtos: []string{alpn}, MinVersion: tls.VersionTLS13}
}

func dialQUIC(t *testing.T, addr string, tlsConf *tls.Config) *quic.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, addr, tlsConf, &quic.Config{})
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.CloseWithError(0, "test done") })
	return conn
}

func openAndWrite(t *testing.T, conn *quic.Conn, data string) *quic.Stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if _, err := stream.Write([]byte(data)); err != nil {
		t.Fatalf("write: %v", err)
	}
	return stream
}

func collect(t *testing.T, sub chan pipeline.TapEvent, n int) []pipeline.TapEvent {
	t.Helper()
	var events []pipeline.TapEvent
	deadline := time.After(5 * time.Second)
	for len(events) < n {
		select {
		case ev := <-sub:
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("got %d of %d tap events", len(events), n)
		}
	}
	return events
}

// Every stream of a connection is one framed byte stream, like one TCP
// connection; the default TLS material is an ephemeral self-signed
// certificate with ALPN gofi/1.
func TestQUICStreamsAreFramedLikeConnections(t *testing.T) {
	_, sub, addr := startQUICListener(t, quicFlow(t, nil), DefaultLimits())
	conn := dialQUIC(t, addr, clientTLS(spec.DefaultQUICALPN))
	if got := conn.ConnectionState().TLS.NegotiatedProtocol; got != spec.DefaultQUICALPN {
		t.Fatalf("ALPN = %q", got)
	}
	// Two streams on one connection; a line split across two writes on the
	// first stream still frames as one message.
	s1 := openAndWrite(t, conn, `{"stream":1,"n":1}`+"\n"+`{"stream":1,`)
	s2 := openAndWrite(t, conn, `{"stream":2,"n":1}`+"\n")
	if _, err := s1.Write([]byte(`"n":2}` + "\n")); err != nil {
		t.Fatal(err)
	}
	events := collect(t, sub, 3)
	seen := map[string]bool{}
	for _, ev := range events {
		if ev.Dropped {
			t.Fatalf("dropped: %+v", ev)
		}
		seen[string(ev.Msg.Raw)] = true
		if source := ev.Msg.Meta["source"]; !strings.Contains(source, "#") {
			t.Fatalf("meta source %q does not name the stream", source)
		}
	}
	for _, want := range []string{`{"stream":1,"n":1}`, `{"stream":1,"n":2}`, `{"stream":2,"n":1}`} {
		if !seen[want] {
			t.Fatalf("missing %s in %v", want, seen)
		}
	}
	s1.Close()
	s2.Close()
	// A peer with another ALPN is refused at the handshake.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if other, err := quic.DialAddr(ctx, addr, clientTLS("h3"), &quic.Config{}); err == nil {
		other.CloseWithError(0, "")
		t.Fatal("handshake with an unknown ALPN succeeded")
	}
}

// A source with a CA and requireClientCert admits only clients whose
// certificate chains to that CA.
func TestQUICRequireClientCertRejectsWrongCertificate(t *testing.T) {
	ca, err := quictls.NewCA("flow-ca")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := quictls.NewCA("other-ca")
	serverCert, serverKey, _ := ca.Issue("gofi", []string{"127.0.0.1"}, false)
	_, sub, addr := startQUICListener(t, quicFlow(t, &spec.TLSConfig{Cert: serverCert, Key: serverKey, CA: ca.CertPEM(), RequireClientCert: true}), DefaultLimits())

	rootPool := func(pem string) *tls.Config {
		conf := &tls.Config{NextProtos: []string{spec.DefaultQUICALPN}, MinVersion: tls.VersionTLS13, ServerName: "127.0.0.1"}
		conf.RootCAs = certPool(t, pem)
		return conf
	}
	// The TLS 1.3 server verifies the client certificate after the client's
	// Finished, so the client may see the failure either at dial or as the
	// connection being closed right after.
	rejected := func(name string, conf *tls.Config) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := quic.DialAddr(ctx, addr, conf, &quic.Config{})
		if err != nil {
			return
		}
		defer conn.CloseWithError(0, "")
		select {
		case <-conn.Context().Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: connection stayed open", name)
		}
	}
	rejected("no client certificate", rootPool(ca.CertPEM()))
	wrongCert, wrongKey, _ := other.Issue("intruder", nil, true)
	wrongConf := rootPool(ca.CertPEM())
	wrongConf.Certificates = []tls.Certificate{keyPair(t, wrongCert, wrongKey)}
	rejected("certificate from another CA", wrongConf)

	goodCert, goodKey, _ := ca.Issue("sender", nil, true)
	goodConf := rootPool(ca.CertPEM())
	goodConf.Certificates = []tls.Certificate{keyPair(t, goodCert, goodKey)}
	conn := dialQUIC(t, addr, goodConf)
	openAndWrite(t, conn, `{"ok":true}`+"\n")
	if ev := collect(t, sub, 1)[0]; string(ev.Msg.Raw) != `{"ok":true}` {
		t.Fatalf("got %s", ev.Msg.Raw)
	}
	// And the server certificate was verified against the CA, not skipped.
	if !conn.ConnectionState().TLS.HandshakeComplete || conn.ConnectionState().TLS.PeerCertificates[0].Subject.CommonName != "gofi" {
		t.Fatal("server certificate not the issued one")
	}
}

// Limits.MaxConnections caps QUIC connections (the surplus connection is
// closed with application error code 1) and, separately, the streams in
// flight across them (a surplus stream is reset with stream error code 1 and
// admitted once the budget frees).
func TestQUICConnectionCap(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxConnections = 1
	l, sub, addr := startQUICListener(t, quicFlow(t, nil), limits)
	first := dialQUIC(t, addr, clientTLS(spec.DefaultQUICALPN))
	s1 := openAndWrite(t, first, `{"n":1}`+"\n")
	collect(t, sub, 1)

	// A second stream while the first is open exceeds the stream budget.
	s2 := openAndWrite(t, first, `{"n":2}`+"\n")
	buf := make([]byte, 1)
	_ = s2.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := s2.Read(buf)
	var streamErr *quic.StreamError
	if !errors.As(err, &streamErr) || streamErr.ErrorCode != quicStreamRefused || !streamErr.Remote {
		t.Fatalf("surplus stream: read error = %v, want remote stream error %d", err, quicStreamRefused)
	}
	select {
	case ev := <-sub:
		t.Fatalf("refused stream reached the pipeline: %s", ev.Msg.Raw)
	case <-time.After(200 * time.Millisecond):
	}
	// Closing the first stream frees the budget; the next stream is served.
	s1.Close()
	waitFor(t, func() bool { return l.quic.streams.Load() == 0 })
	openAndWrite(t, first, `{"n":3}`+"\n")
	if ev := collect(t, sub, 1)[0]; string(ev.Msg.Raw) != `{"n":3}` {
		t.Fatalf("got %s", ev.Msg.Raw)
	}

	second := dialQUIC(t, addr, clientTLS(spec.DefaultQUICALPN))
	select {
	case <-second.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("second connection not refused")
	}
	var appErr *quic.ApplicationError
	if cause := context.Cause(second.Context()); !errors.As(cause, &appErr) || appErr.ErrorCode != quicCloseConnectionCap || !appErr.Remote {
		t.Fatalf("close cause = %v", cause)
	}
}

// Shutdown stops intake, finishes frames already received, then closes the
// connections with application error code 0.
func TestQUICShutdownDrainsThenClosesConnections(t *testing.T) {
	l, sub, addr := startQUICListener(t, quicFlow(t, nil), DefaultLimits())
	conn := dialQUIC(t, addr, clientTLS(spec.DefaultQUICALPN))
	stream := openAndWrite(t, conn, `{"n":1}`+"\n")
	collect(t, sub, 1)

	drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	if err := l.Shutdown(drainCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("drain took %v", time.Since(started))
	}
	select {
	case <-conn.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection not closed by the drained listener")
	}
	var appErr *quic.ApplicationError
	if cause := context.Cause(conn.Context()); !errors.As(cause, &appErr) || appErr.ErrorCode != quicCloseNormal || !appErr.Remote {
		t.Fatalf("close cause = %v", cause)
	}
	if _, err := stream.Write([]byte("x")); err == nil {
		t.Fatal("write on a closed connection succeeded")
	}
	// The port is free again.
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("port still bound: %v", err)
	}
	pc.Close()
}

func certPool(t *testing.T, pem string) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(pem)) {
		t.Fatal("bad CA PEM")
	}
	return pool
}

func keyPair(t *testing.T, cert, key string) tls.Certificate {
	t.Helper()
	pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

// Address validation (Retry) is demanded only once connection attempts
// from unvalidated addresses exceed quicRetryThreshold within a second.
func TestQUICAttemptRateDemandsRetryUnderFlood(t *testing.T) {
	rate := &quicAttemptRate{}
	addr := &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 1}
	for i := 0; i < quicRetryThreshold; i++ {
		if rate.verify(addr) {
			t.Fatalf("attempt %d asked for validation below the threshold", i+1)
		}
	}
	if !rate.verify(addr) {
		t.Fatal("attempt above the threshold not validated")
	}
	// A new window starts clean.
	rate.window = time.Now().Add(-2 * time.Second)
	if rate.verify(addr) {
		t.Fatal("first attempt of a new window asked for validation")
	}
}
