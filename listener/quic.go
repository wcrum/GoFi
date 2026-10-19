package listener

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/wcrum/gofi/internal/quictls"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// QUIC listen source. A QUIC connection may carry many streams; each
// accepted bidirectional stream is one byte stream through the pipeline,
// framed exactly like one TCP connection (serveConn). Limits.MaxConnections
// caps QUIC connections and, separately, the streams in flight across all of
// them (a stream costs what a TCP connection costs: a pipeline goroutine and
// a framer buffer of up to Framing.MaxBytes), quicMaxStreamsPerConn caps the
// streams one peer may hold open on a connection, and Limits.IdleTimeout
// applies to every stream.
const (
	// quicMaxStreamsPerConn bounds concurrent bidirectional streams per
	// connection, so one peer cannot take the whole stream budget.
	// Unidirectional streams are refused: nothing would read them.
	quicMaxStreamsPerConn = 32
	// quicIdleTimeout closes a connection with no packets at all when the
	// flow sets no idle timeout (quic-go's own default is also 30s).
	quicIdleTimeout = 30 * time.Second
	// quicHandshakeTimeout bounds a handshake, so half-open attempts cannot
	// hold server state for long.
	quicHandshakeTimeout = 5 * time.Second
	// Flow-control windows bound the bytes a peer may push before the
	// pipeline consumes them: per stream (frames are at most Framing.MaxBytes,
	// 1 MiB by default) and per connection.
	quicStreamWindow     = 1 << 20
	quicConnectionWindow = 4 << 20
	quicInitialWindow    = 256 << 10
	// quicRetryThreshold is the number of connection attempts from
	// unvalidated addresses per second above which the listener demands
	// address validation (a Retry packet, RFC 9000 section 8.1.2) before it
	// allocates handshake state. Below it a handshake costs one round trip;
	// above it a flood of Initials with spoofed sources costs the attacker
	// a round trip per attempt and GoFi only a token, and GoFi never sends
	// more than it received to an address that did not prove it is there.
	quicRetryThreshold = 100
)

// Application error codes GoFi closes connections with.
const (
	quicCloseNormal        quic.ApplicationErrorCode = 0 // drain finished or the listener stopped
	quicCloseConnectionCap quic.ApplicationErrorCode = 1 // Limits.MaxConnections reached
	// quicStreamRefused resets a stream opened while the listener-wide
	// stream budget is spent; the peer may retry later.
	quicStreamRefused quic.StreamErrorCode = 1
)

// quicListener is the bound QUIC endpoint and its live connections. The
// transport (the UDP socket) outlives the listener: closing the listener
// stops new connections while the accepted ones finish their streams; then
// shutdown closes the connections and releases the socket.
type quicListener struct {
	pc    net.PacketConn // ours to close: the transport only stops reading it
	tr    *quic.Transport
	ln    *quic.Listener
	mu    sync.Mutex
	conns map[*quic.Conn]struct{}
	once  sync.Once
	// streams counts bidirectional streams in flight across all connections;
	// bounded by Limits.MaxConnections like TCP connections are.
	streams atomic.Int64
}

// listenQUIC binds a QUIC listener on addr (UDP) for def's listen source.
func listenQUIC(def spec.FlowDefinition, addr string, limits Limits) (*quicListener, error) {
	listen := def.Source.Listening()
	tlsConf, err := quictls.Server(listen.TLS, def.Name)
	if err != nil {
		return nil, err
	}
	idle := quicIdleTimeout
	if limits.IdleTimeout > 0 {
		idle = limits.IdleTimeout
	}
	conf := &quic.Config{
		HandshakeIdleTimeout:           quicHandshakeTimeout,
		MaxIdleTimeout:                 idle,
		MaxIncomingStreams:             quicMaxStreamsPerConn,
		MaxIncomingUniStreams:          -1,
		InitialStreamReceiveWindow:     quicInitialWindow,
		MaxStreamReceiveWindow:         quicStreamWindow,
		InitialConnectionReceiveWindow: quicInitialWindow,
		MaxConnectionReceiveWindow:     quicConnectionWindow,
		Allow0RTT:                      false, // 0-RTT data is replayable
	}
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	rate := &quicAttemptRate{}
	tr := &quic.Transport{Conn: pc, VerifySourceAddress: rate.verify}
	ln, err := tr.Listen(tlsConf, conf)
	if err != nil {
		tr.Close()
		pc.Close()
		return nil, err
	}
	slog.Info("QUIC listener configured", "name", def.Name, "alpn", tlsConf.NextProtos, "clientAuth", tlsConf.ClientAuth != tls.NoClientCert, "maxStreamsPerConnection", quicMaxStreamsPerConn, "connectionIdleTimeout", idle)
	return &quicListener{pc: pc, tr: tr, ln: ln, conns: map[*quic.Conn]struct{}{}}, nil
}

func (q *quicListener) add(conn *quic.Conn) {
	q.mu.Lock()
	q.conns[conn] = struct{}{}
	q.mu.Unlock()
}

func (q *quicListener) remove(conn *quic.Conn) {
	q.mu.Lock()
	delete(q.conns, conn)
	q.mu.Unlock()
}

// shutdown ends every live connection with code (CloseWithError returns
// once the CONNECTION_CLOSE went out), then releases the UDP socket. It runs
// once; the listener itself was closed before.
func (q *quicListener) shutdown(code quic.ApplicationErrorCode, reason string) {
	q.once.Do(func() {
		q.mu.Lock()
		conns := make([]*quic.Conn, 0, len(q.conns))
		for conn := range q.conns {
			conns = append(conns, conn)
		}
		q.mu.Unlock()
		var wg sync.WaitGroup
		for _, conn := range conns {
			wg.Add(1)
			go func(conn *quic.Conn) {
				defer wg.Done()
				_ = conn.CloseWithError(code, reason)
			}(conn)
		}
		wg.Wait()
		_ = q.tr.Close()
		_ = q.pc.Close()
	})
}

// Addr is the bound UDP address.
func (q *quicListener) Addr() net.Addr { return q.ln.Addr() }

// quicAttemptRate counts connection attempts from unvalidated addresses in
// one-second windows and asks for address validation once a window exceeds
// quicRetryThreshold.
type quicAttemptRate struct {
	mu     sync.Mutex
	window time.Time
	count  int
}

func (r *quicAttemptRate) verify(net.Addr) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Sub(r.window) >= time.Second {
		r.window, r.count = now, 0
	}
	r.count++
	return r.count > quicRetryThreshold
}

// runQUIC accepts connections until the listener closes (drain or hard
// stop), waits for the streams in flight, then closes the connections with
// application error code 0.
func (l *Listener) runQUIC() {
	for {
		conn, err := l.quic.ln.Accept(l.accepting)
		if err != nil {
			if l.accepting.Err() != nil || errors.Is(err, quic.ErrServerClosed) {
				break
			}
			slog.Warn("QUIC accept error", "listener", l.def.Name, "error", err)
			observability.Report(l.def.Name, observability.SeverityWarning, observability.ComponentSource, "accept: "+err.Error())
			continue
		}
		if l.connCount.Load() >= int64(l.limits.MaxConnections) {
			_ = conn.CloseWithError(quicCloseConnectionCap, "connection cap reached")
			l.refuse()
			continue
		}
		// Counted and registered here, not in the goroutine, so the cap is
		// exact and closeAll below cannot miss a connection just accepted.
		l.connCount.Add(1)
		if l.connGauge != nil {
			l.connGauge.Inc()
		}
		l.quic.add(conn)
		go l.serveQUICConn(conn)
	}
	// Intake is over. Streams already accepted finish (the drain's read
	// deadline ends them promptly); then the connections and the socket go.
	l.waitInflight()
	l.quic.shutdown(quicCloseNormal, "listener stopped")
}

// serveQUICConn accepts the streams of one connection and serves each like
// a TCP connection. It returns when the connection ends: the peer closed
// it, the idle timeout passed, or the listener closed it.
func (l *Listener) serveQUICConn(conn *quic.Conn) {
	defer func() {
		l.quic.remove(conn)
		l.connCount.Add(-1)
		if l.connGauge != nil {
			l.connGauge.Dec()
		}
	}()
	remote := conn.RemoteAddr().String()
	state := conn.ConnectionState().TLS
	slog.Info("QUIC client connected", "listener", l.def.Name, "remote", remote, "alpn", state.NegotiatedProtocol, "clientCert", len(state.PeerCertificates) > 0)
	for {
		stream, err := conn.AcceptStream(l.processing)
		if err != nil {
			break
		}
		if l.quic.streams.Load() >= int64(l.limits.MaxConnections) {
			// The listener-wide stream budget is spent: reset this stream
			// (the connection stays up) and count it as a refusal.
			stream.CancelRead(quicStreamRefused)
			stream.CancelWrite(quicStreamRefused)
			l.refuse()
			continue
		}
		sc := &quicStreamConn{Stream: stream, local: conn.LocalAddr(), remote: quicStreamAddr{addr: conn.RemoteAddr(), id: stream.StreamID()}}
		if !l.track(sc) {
			_ = sc.Close()
			break
		}
		l.quic.streams.Add(1)
		go func() {
			defer l.quic.streams.Add(-1)
			l.serveConn(sc)
		}()
	}
	reason := context.Cause(conn.Context())
	if reason == nil {
		reason = conn.Context().Err()
	}
	slog.Info("QUIC client disconnected", "listener", l.def.Name, "remote", remote, "reason", reason)
	_ = conn.CloseWithError(quicCloseNormal, "")
}

// quicStreamConn presents one QUIC stream as a net.Conn so the TCP path
// (serveConn, idleConn, the drain's read deadlines) serves it unchanged.
type quicStreamConn struct {
	*quic.Stream
	local, remote net.Addr
}

func (c *quicStreamConn) LocalAddr() net.Addr  { return c.local }
func (c *quicStreamConn) RemoteAddr() net.Addr { return c.remote }

// Close ends both directions: the send side with a FIN, the receive side by
// telling the peer to stop (no-op after its FIN arrived).
func (c *quicStreamConn) Close() error {
	c.Stream.CancelRead(0)
	return c.Stream.Close()
}

// quicStreamAddr names a stream on a connection: "host:port#streamID".
type quicStreamAddr struct {
	addr net.Addr
	id   quic.StreamID
}

func (a quicStreamAddr) Network() string { return spec.NetworkQUIC }
func (a quicStreamAddr) String() string  { return fmt.Sprintf("%s#%d", a.addr, a.id) }
