package traffic

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// quicServer is a stand-in for a GoFi QUIC listen source: it accepts
// connections with ALPN gofi/1, treats every stream as one newline-framed
// byte stream, and records the ids it reads.
type quicServer struct {
	ln      *quic.Listener
	udp     *net.UDPConn
	mu      sync.Mutex
	ids     map[string]int
	streams int
	conns   int
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gofi-test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func startQUICServer(t *testing.T, addr string) *quicServer {
	t.Helper()
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := quic.Listen(udp, &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}, NextProtos: []string{DefaultALPN}}, &quic.Config{MaxIncomingStreams: 256, MaxIdleTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s := &quicServer{ln: ln, udp: udp, ids: map[string]int{}}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	go s.accept()
	t.Cleanup(s.stop)
	return s
}

func (s *quicServer) addr() string { return s.udp.LocalAddr().String() }

func (s *quicServer) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept(s.ctx)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns++
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				stream, err := conn.AcceptStream(s.ctx)
				if err != nil {
					return
				}
				s.mu.Lock()
				s.streams++
				s.mu.Unlock()
				s.wg.Add(1)
				go func() {
					defer s.wg.Done()
					// Like GoFi's listener: close our side once the client's EOF
					// has been read, which tells the client its data arrived.
					defer stream.Close()
					sc := bufio.NewScanner(stream)
					for sc.Scan() {
						var m struct {
							ID string `json:"id"`
						}
						if json.Unmarshal(sc.Bytes(), &m) == nil && m.ID != "" {
							s.mu.Lock()
							s.ids[m.ID]++
							s.mu.Unlock()
						}
					}
				}()
			}
		}()
	}
}

func (s *quicServer) stop() {
	s.cancel()
	_ = s.ln.Close()
	_ = s.udp.Close()
	s.wg.Wait()
}

func (s *quicServer) count() (ids, streams, conns int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ids), s.streams, s.conns
}

func (s *quicServer) missing(sent []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, id := range sent {
		if s.ids[id] == 0 {
			out = append(out, id)
		}
	}
	return out
}

func waitIDs(t *testing.T, s *quicServer, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got, _, _ := s.count(); got >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _, _ := s.count()
	t.Fatalf("server read %d of %d ids", got, n)
}

func TestQUICGeneratorOneConnectionManyStreams(t *testing.T) {
	srv := startQUICServer(t, "127.0.0.1:0")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := WaitListening(ctx, "quic", srv.addr()); err != nil {
		t.Fatal(err)
	}
	gen := &Generator{Network: "quic", Addr: srv.addr(), Prefix: "q", Connections: 16}
	res := gen.Run(ctx, 2000)
	if res.Failed != 0 || res.Sent != 2000 {
		t.Fatalf("sent=%d failed=%d first=%s", res.Sent, res.Failed, res.FirstErr)
	}
	waitIDs(t, srv, 2000)
	if m := srv.missing(res.IDs); len(m) != 0 {
		t.Fatalf("%d ids never arrived: %v", len(m), m[:min(5, len(m))])
	}
	_, streams, conns := srv.count()
	// The generator multiplexes all 16 streams on one connection; the readiness
	// probe may or may not complete a handshake the server counts.
	if streams != 16 || conns < 1 || conns > 2 {
		t.Fatalf("server saw %d connections and %d streams, want 16 streams on one generator connection", conns, streams)
	}
}

func TestQUICGeneratorReconnectsAfterServerRestart(t *testing.T) {
	srv := startQUICServer(t, "127.0.0.1:0")
	addr := srv.addr()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	gen := &Generator{Network: "quic", Addr: addr, Prefix: "r", Rate: 200, Connections: 2, Reconnect: true, QUIC: QUICOptions{IdleTimeout: 2 * time.Second}}
	genCtx, stopGen := context.WithCancel(ctx)
	results := make(chan Result, 1)
	go func() { results <- gen.Run(genCtx, 0) }()
	waitIDs(t, srv, 50)
	srv.stop()
	srv2 := startQUICServer(t, addr)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if got, _, _ := srv2.count(); got >= 100 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	stopGen()
	res := <-results
	got, _, _ := srv2.count()
	if got < 100 {
		t.Fatalf("delivery did not resume after the server restart: %d ids on the new server, reconnects=%d first=%s", got, res.Reconnect, res.FirstErr)
	}
	if res.Reconnect == 0 {
		t.Fatalf("no reconnect was counted across the restart (sent=%d failed=%d)", res.Sent, res.Failed)
	}
}

func TestQUICOptionsTLSConfig(t *testing.T) {
	cfg, err := QUICOptions{}.TLSConfig("10.0.0.1:9000")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InsecureSkipVerify || cfg.ServerName != "10.0.0.1" || len(cfg.NextProtos) != 1 || cfg.NextProtos[0] != DefaultALPN {
		t.Fatalf("defaults: %+v", cfg)
	}
	if _, err := (QUICOptions{CA: "not pem"}).TLSConfig("h:1"); err == nil {
		t.Fatal("bad CA accepted")
	}
	cfg, err = QUICOptions{VerifyServer: true, ServerName: "gofi.example", ALPN: []string{"x/1"}}.TLSConfig("h:1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InsecureSkipVerify || cfg.ServerName != "gofi.example" || cfg.NextProtos[0] != "x/1" {
		t.Fatalf("overrides: %+v", cfg)
	}
}
