package traffic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/quic-go/quic-go"
)

// Message is one generated line: {"id": "<prefix>-<n>", "seq": n, "ts": ..., "pad": "..."}.
func Message(prefix string, seq int64, padBytes int) []byte {
	msg := map[string]any{"id": fmt.Sprintf("%s-%d", prefix, seq), "seq": seq, "ts": time.Now().UnixNano()}
	if padBytes > 0 {
		msg["pad"] = strings.Repeat("x", padBytes)
	}
	data, _ := json.Marshal(msg)
	return append(data, '\n')
}

// Result summarizes a generator run.
type Result struct {
	Sent      int64         `json:"sent"`
	Failed    int64         `json:"failed"`
	Reconnect int64         `json:"reconnects"`
	Duration  time.Duration `json:"duration"`
	IDs       []string      `json:"-"`
	FirstErr  string        `json:"firstError,omitempty"`
}

// Rate is messages per second across the whole generator (0 = as fast as
// possible).
type Generator struct {
	Network string // tcp | udp | quic | nats
	Addr    string // host:port or nats URL
	Subject string // NATS subject
	Prefix  string
	Rate    int
	Pad     int
	// Connections is the number of parallel TCP connections (default 1).
	// For quic it is the number of parallel streams on one QUIC connection.
	Connections int
	// Reconnect keeps a TCP or QUIC sender retrying through connection loss
	// (a listener restart) instead of failing.
	Reconnect bool
	// Dial timeout per attempt.
	DialTimeout time.Duration
	// QUIC tunes the quic client (Network "quic"). The zero value dials with
	// ALPN gofi/1 and skips certificate verification, which is what a flow
	// with no tls block (ephemeral self-signed certificate) needs.
	QUIC QUICOptions

	seq    atomic.Int64
	sent   atomic.Int64
	failed atomic.Int64
	recon  atomic.Int64
	mu     sync.Mutex
	ids    []string
	first  string
	qconn  *quicConn
}

// QUICOptions is the generator's QUIC client configuration. Every field is
// optional; see Generator.QUIC for the defaults.
type QUICOptions struct {
	// TLS replaces the generated client TLS config entirely when set; the
	// other fields are then ignored.
	TLS *tls.Config
	// CA is an inline PEM bundle the server certificate must chain to.
	// Setting it turns verification on.
	CA string
	// Cert and Key are an inline PEM client certificate and private key for
	// flows that set tls.requireClientCert.
	Cert, Key string
	// ServerName overrides the SNI/verification name (default: the host part
	// of Addr).
	ServerName string
	// ALPN is the application protocol list (default ["gofi/1"]).
	ALPN []string
	// VerifyServer turns verification on against the system roots (CA does
	// the same with a private root).
	VerifyServer bool
	// IdleTimeout is how long the client waits without traffic before it
	// declares the connection dead (default 5s), so a restarted listener is
	// noticed and the stream reconnects. Keep-alives run at half of it.
	IdleTimeout time.Duration
}

// DefaultALPN is the application protocol GoFi's QUIC listen source accepts.
const DefaultALPN = "gofi/1"

// TLSConfig builds the client tls.Config for addr from the options.
func (o QUICOptions) TLSConfig(addr string) (*tls.Config, error) {
	if o.TLS != nil {
		return o.TLS, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: o.ALPN, InsecureSkipVerify: !(o.VerifyServer || o.CA != "")}
	if len(cfg.NextProtos) == 0 {
		cfg.NextProtos = []string{DefaultALPN}
	}
	cfg.ServerName = o.ServerName
	if cfg.ServerName == "" {
		if host, _, err := net.SplitHostPort(addr); err == nil {
			cfg.ServerName = host
		}
	}
	if o.CA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(o.CA)) {
			return nil, errors.New("quic: CA is not a PEM certificate bundle")
		}
		cfg.RootCAs = pool
	}
	if o.Cert != "" || o.Key != "" {
		cert, err := tls.X509KeyPair([]byte(o.Cert), []byte(o.Key))
		if err != nil {
			return nil, fmt.Errorf("quic: client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func (o QUICOptions) quicConfig() *quic.Config {
	idle := o.IdleTimeout
	if idle <= 0 {
		idle = 5 * time.Second
	}
	return &quic.Config{MaxIdleTimeout: idle, KeepAlivePeriod: idle / 2, HandshakeIdleTimeout: 3 * time.Second}
}

// quicConn shares one QUIC connection between the generator's stream workers
// and redials it when it has died (the listener restarted).
type quicConn struct {
	mu   sync.Mutex
	conn *quic.Conn
	opts QUICOptions
	addr string
	dial time.Duration
}

func (q *quicConn) get(ctx context.Context) (*quic.Conn, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn != nil && q.conn.Context().Err() == nil {
		return q.conn, nil
	}
	tlsCfg, err := q.opts.TLSConfig(q.addr)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, q.dial)
	defer cancel()
	conn, err := quic.DialAddr(dialCtx, q.addr, tlsCfg, q.opts.quicConfig())
	if err != nil {
		return nil, err
	}
	q.conn = conn
	return conn, nil
}

// drop closes the shared connection if it is still conn (a worker saw it
// fail), so the next get redials instead of reusing a half-dead connection.
func (q *quicConn) drop(conn *quic.Conn) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn == conn && conn != nil {
		_ = conn.CloseWithError(0, "generator: reconnecting")
		q.conn = nil
	}
}

// drainQUICStream finishes a stream the way a QUIC client must: Close sends
// FIN but does not wait for the data to be acknowledged, and a connection
// close right after it discards unacknowledged stream data. Reading until
// the peer closes its side (GoFi does so after it has read EOF) proves the
// bytes arrived; the wait is bounded for peers that keep their side open.
func drainQUICStream(stream *quic.Stream) {
	_ = stream.Close()
	_ = stream.SetReadDeadline(time.Now().Add(time.Second))
	var buf [256]byte
	for {
		if _, err := stream.Read(buf[:]); err != nil {
			return
		}
	}
}

func (q *quicConn) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn != nil {
		_ = q.conn.CloseWithError(0, "generator: done")
		q.conn = nil
	}
}

// Run sends until ctx ends or total messages (0 = until ctx) were sent.
func (g *Generator) Run(ctx context.Context, total int64) Result {
	started := time.Now()
	conns := g.Connections
	if conns <= 0 || g.Network == "udp" {
		conns = max(1, conns)
	}
	if g.Network == "quic" {
		g.qconn = &quicConn{opts: g.QUIC, addr: g.Addr, dial: g.dialTimeout()}
		defer g.qconn.close()
	}
	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.worker(ctx, total)
		}()
	}
	wg.Wait()
	g.mu.Lock()
	defer g.mu.Unlock()
	return Result{Sent: g.sent.Load(), Failed: g.failed.Load(), Reconnect: g.recon.Load(), Duration: time.Since(started), IDs: append([]string(nil), g.ids...), FirstErr: g.first}
}

// Sent is the running count.
func (g *Generator) Sent() int64 { return g.sent.Load() }

func (g *Generator) next(total int64) (int64, bool) {
	n := g.seq.Add(1)
	if total > 0 && n > total {
		return 0, false
	}
	return n, true
}

func (g *Generator) record(id string) {
	g.sent.Add(1)
	g.mu.Lock()
	g.ids = append(g.ids, id)
	g.mu.Unlock()
}

func (g *Generator) fail(err error) {
	g.failed.Add(1)
	g.mu.Lock()
	if g.first == "" && err != nil {
		g.first = err.Error()
	}
	g.mu.Unlock()
}

func (g *Generator) worker(ctx context.Context, total int64) {
	var ticker *time.Ticker
	if g.Rate > 0 {
		ticker = time.NewTicker(time.Second / time.Duration(g.Rate))
		defer ticker.Stop()
	}
	var send func([]byte) error
	var closeFn func()
	connect := func() error {
		switch g.Network {
		case "udp":
			conn, err := net.Dial("udp", g.Addr)
			if err != nil {
				return err
			}
			send = func(b []byte) error { _, err := conn.Write(b); return err }
			closeFn = func() { conn.Close() }
		case "nats":
			nc, err := nats.Connect(g.Addr, nats.Timeout(g.dialTimeout()))
			if err != nil {
				return err
			}
			send = func(b []byte) error { return nc.Publish(g.Subject, b) }
			closeFn = func() { nc.Flush(); nc.Close() }
		case "quic":
			// One connection for the whole generator; this worker owns one
			// bidirectional stream on it, framed like one TCP connection.
			conn, err := g.qconn.get(ctx)
			if err != nil {
				return err
			}
			openCtx, cancel := context.WithTimeout(ctx, g.dialTimeout())
			stream, err := conn.OpenStreamSync(openCtx)
			cancel()
			if err != nil {
				g.qconn.drop(conn)
				return err
			}
			send = func(b []byte) error {
				_ = stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_, err := stream.Write(b)
				if err != nil && conn.Context().Err() != nil {
					g.qconn.drop(conn)
				}
				return err
			}
			closeFn = func() { drainQUICStream(stream) }
		default:
			conn, err := net.DialTimeout("tcp", g.Addr, g.dialTimeout())
			if err != nil {
				return err
			}
			send = func(b []byte) error {
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_, err := conn.Write(b)
				return err
			}
			closeFn = func() { conn.Close() }
		}
		return nil
	}
	if err := connect(); err != nil {
		if !g.Reconnect {
			g.fail(err)
			return
		}
	}
	defer func() {
		if closeFn != nil {
			closeFn()
		}
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		if ticker != nil {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		n, ok := g.next(total)
		if !ok {
			return
		}
		id := fmt.Sprintf("%s-%d", g.Prefix, n)
		payload := Message(g.Prefix, n, g.Pad)
		if send == nil {
			if err := connect(); err != nil {
				g.fail(err)
				g.recon.Add(1)
				sleepCtx(ctx, 100*time.Millisecond)
				continue
			}
		}
		if err := send(payload); err != nil {
			g.fail(err)
			if closeFn != nil {
				closeFn()
			}
			send, closeFn = nil, nil
			if !g.Reconnect {
				return
			}
			g.recon.Add(1)
			sleepCtx(ctx, 100*time.Millisecond)
			continue
		}
		g.record(id)
	}
}

func (g *Generator) dialTimeout() time.Duration {
	if g.DialTimeout > 0 {
		return g.DialTimeout
	}
	return 3 * time.Second
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// SendOnce writes one message over TCP and closes; useful as a probe.
func SendOnce(addr string, payload []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write(payload)
	return err
}

// WaitListening dials addr until it accepts (the flow's port is bound).
// network is tcp (also ""), udp, or quic; a quic probe completes a handshake
// against the flow's certificate without verifying it.
func WaitListening(ctx context.Context, network, addr string) error {
	for {
		var err error
		switch network {
		case "udp":
			// UDP cannot be probed by dialing; wait for ICMP is unreliable.
			// Callers should use the API phase instead.
			return nil
		case "quic":
			err = probeQUIC(ctx, addr)
		default:
			var conn net.Conn
			conn, err = net.DialTimeout("tcp", addr, 500*time.Millisecond)
			if err == nil {
				conn.Close()
			}
		}
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s not listening: %w", addr, errors.Join(ctx.Err(), err))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// probeQUIC completes one QUIC handshake with addr and closes the connection.
func probeQUIC(ctx context.Context, addr string) error {
	tlsCfg, err := QUICOptions{}.TLSConfig(addr)
	if err != nil {
		return err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	conn, err := quic.DialAddr(dialCtx, addr, tlsCfg, &quic.Config{HandshakeIdleTimeout: 500 * time.Millisecond})
	if err != nil {
		return err
	}
	return conn.CloseWithError(0, "probe")
}

// SendOnceQUIC opens one QUIC connection and stream to addr, writes payload,
// and closes both; useful as a probe or a one-shot sender from a script.
func SendOnceQUIC(ctx context.Context, addr string, payload []byte, opts QUICOptions) error {
	tlsCfg, err := opts.TLSConfig(addr)
	if err != nil {
		return err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(dialCtx, addr, tlsCfg, opts.quicConfig())
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "done")
	stream, err := conn.OpenStreamSync(dialCtx)
	if err != nil {
		return err
	}
	if _, err := stream.Write(payload); err != nil {
		return err
	}
	drainQUICStream(stream)
	return nil
}

// IdleConnections opens n TCP connections to addr and holds them open until
// ctx ends; it returns how many connected.
func IdleConnections(ctx context.Context, addr string, n int) (int, func()) {
	var conns []net.Conn
	var mu sync.Mutex
	opened := 0
	for i := 0; i < n; i++ {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			continue
		}
		mu.Lock()
		conns = append(conns, conn)
		mu.Unlock()
		opened++
	}
	closeAll := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
		conns = nil
	}
	go func() {
		<-ctx.Done()
		closeAll()
	}()
	return opened, closeAll
}
