package sink

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/wcrum/gofi/internal/quictls"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// quicSink writes every message to one long-lived bidirectional stream on
// one long-lived QUIC connection, the way a GoFi QUIC listen source (or any
// stream-oriented QUIC server) reads it: one byte stream, Delimiter after
// each payload. Messages are delivered in Send order; QUIC retransmits lost
// packets within the stream, so a message is either delivered whole or the
// stream dies. A failed write or a dead connection drops the connection; the
// next Send redials, after an exponential backoff once dials start failing.
//
// Send is synchronous, like the tcp sink: the pipeline records each result
// on the route (observability.RouteResult). Like tcp and udp, quic is not in
// QueuedTypes: a write completes into the local send buffer, so there is no
// per-request latency to hide behind a queue. What the socket sinks lack in
// return (retry, breaker) the dial backoff below covers for the expensive
// part, the handshake.
//
// QUIC writes succeed locally; the peer's receipt is confirmed only when it
// acknowledges the data. Flush and Close therefore finish the stream (FIN)
// and wait for the destination to finish its side, which a GoFi QUIC source
// does once it has read everything, before the connection is closed:
// closing a QUIC connection discards data that was not acknowledged yet.
type quicSink struct {
	address, delimiter, bodyMode string
	tlsConf                      *tls.Config
	quicConf                     *quic.Config

	// mu guards the fields below and is held across a write so messages never
	// interleave; dialMu lets one sender dial at a time without holding mu,
	// so a slow dial stalls only senders that need the connection.
	mu       sync.Mutex
	dialMu   sync.Mutex
	conn     *quic.Conn
	stream   *quic.Stream
	failures int       // consecutive failed dials
	retryAt  time.Time // no dial before this
}

// Reconnect backoff bounds and liveness of the sink connection. A
// destination that vanished (restart, network cut) is noticed when
// keep-alives go unanswered for the idle timeout; the next Send then redials.
const (
	quicSinkRetryInitial = 200 * time.Millisecond
	quicSinkRetryMax     = 30 * time.Second
	quicSinkKeepAlive    = 5 * time.Second
	quicSinkIdleTimeout  = 15 * time.Second
	// quicSinkWriteError is the application error code the sink closes a
	// connection with after a failed write; quicCloseNormalSink is a normal close.
	quicSinkWriteError  quic.ApplicationErrorCode = 1
	quicCloseNormalSink quic.ApplicationErrorCode = 0
)

// newQUIC builds the sink for cfg. A Kubernetes selector target is addressed
// like the tcp and udp sinks: through the target Service GoFi publishes in
// its own namespace, fanning out to every ready Pod unless delivery is
// "one". The TLS server name stays the Service host (or tls.serverName) for
// every Pod, so the receivers' certificate must carry that name.
func newQUIC(cfg spec.SinkCfg, listenerName string) (Sink, error) {
	cfg, fanout, err := kubernetesTargetURL(cfg, listenerName)
	if err != nil {
		return nil, err
	}
	u, mode, err := networkEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	tlsConf, err := quictls.Client(cfg.TLS, u.Hostname())
	if err != nil {
		return nil, err
	}
	quicConf := &quic.Config{
		HandshakeIdleTimeout: networkWriteTimeout,
		MaxIdleTimeout:       quicSinkIdleTimeout,
		KeepAlivePeriod:      quicSinkKeepAlive,
		// The destination never opens streams toward the sink.
		MaxIncomingStreams:    -1,
		MaxIncomingUniStreams: -1,
	}
	peer := func(address string) *quicSink {
		return &quicSink{address: address, delimiter: cfg.Delimiter, bodyMode: mode, tlsConf: tlsConf, quicConf: quicConf}
	}
	if fanout {
		return newFanout(spec.NetworkQUIC, u.Hostname(), u.Port(), func(address string) Sink { return peer(address) }), nil
	}
	return peer(u.Host), nil
}

// Send writes payload and delimiter to the stream. Writes hold the lock so
// messages never interleave; a failure drops the connection and is returned.
func (s *quicSink) Send(msg *message.Message) error {
	payload, err := socketPayload(msg, s.bodyMode)
	if err != nil {
		return err
	}
	if len(s.delimiter) > 0 {
		// One buffer, never msg.Raw's backing array.
		payload = append(append(make([]byte, 0, len(payload)+len(s.delimiter)), payload...), s.delimiter...)
	}
	stream, err := s.live()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stream != stream {
		// Replaced by a concurrent failure; use the live one.
		if s.stream == nil {
			return transientError{errors.New("quic sink: connection lost")}
		}
		stream = s.stream
	}
	_ = stream.SetWriteDeadline(time.Now().Add(networkWriteTimeout))
	if _, err := stream.Write(payload); err != nil {
		// Do not retry: part of the message may have been delivered. The
		// next message opens a fresh connection.
		s.dropLocked(quicSinkWriteError, "write failed")
		return transientError{fmt.Errorf("quic sink: write: %w", err)}
	}
	return nil
}

// current returns the live stream, forgetting a connection that died
// between sends (destination restarted, idle timeout): the next dial is
// immediate, backoff starts only when dials fail. The caller holds s.mu.
func (s *quicSink) currentLocked() *quic.Stream {
	if s.conn != nil && s.conn.Context().Err() != nil {
		slog.Info("quic sink connection lost; reconnecting", "address", s.address, "reason", context.Cause(s.conn.Context()))
		s.conn, s.stream = nil, nil
	}
	return s.stream
}

// live returns the live stream, dialing a connection or opening a stream
// when needed. Dialing happens outside s.mu, one dial at a time.
func (s *quicSink) live() (*quic.Stream, error) {
	s.mu.Lock()
	if stream := s.currentLocked(); stream != nil {
		s.mu.Unlock()
		return stream, nil
	}
	if now := time.Now(); now.Before(s.retryAt) {
		wait := s.retryAt.Sub(now)
		s.mu.Unlock()
		return nil, transientError{fmt.Errorf("quic sink: %s unreachable; next dial in %s", s.address, wait.Round(time.Millisecond))}
	}
	s.mu.Unlock()

	s.dialMu.Lock()
	defer s.dialMu.Unlock()
	// Another sender may have connected while this one waited.
	s.mu.Lock()
	if stream := s.currentLocked(); stream != nil {
		s.mu.Unlock()
		return stream, nil
	}
	conn := s.conn // alive but without a stream (after Flush), or nil
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), networkWriteTimeout)
	defer cancel()
	dialed := false
	if conn == nil {
		var err error
		conn, err = quic.DialAddr(ctx, s.address, s.tlsConf.Clone(), s.quicConf)
		if err != nil {
			s.mu.Lock()
			s.backoffLocked()
			s.mu.Unlock()
			return nil, transientError{fmt.Errorf("quic sink: dial %s: %w", s.address, err)}
		}
		dialed = true
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		_ = conn.CloseWithError(quicSinkWriteError, "open stream failed")
		s.mu.Lock()
		if s.conn == conn {
			s.conn, s.stream = nil, nil
		}
		s.backoffLocked()
		s.mu.Unlock()
		return nil, transientError{fmt.Errorf("quic sink: open stream: %w", err)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if dialed && s.conn != nil && s.conn != conn {
		// Closed and replaced meanwhile (Close); keep the newer state.
		_ = conn.CloseWithError(quicCloseNormalSink, "superseded")
		return nil, transientError{errors.New("quic sink: connection replaced")}
	}
	s.conn, s.stream = conn, stream
	if s.failures > 0 {
		slog.Info("quic sink reconnected", "address", s.address, "attempts", s.failures+1)
	}
	s.failures, s.retryAt = 0, time.Time{}
	return stream, nil
}

// backoffLocked schedules the next dial attempt: exponential from
// quicSinkRetryInitial to quicSinkRetryMax with up to 25% jitter.
func (s *quicSink) backoffLocked() {
	s.failures++
	delay := quicSinkRetryInitial << min(s.failures-1, 20)
	if delay > quicSinkRetryMax || delay <= 0 {
		delay = quicSinkRetryMax
	}
	delay += time.Duration(rand.Int64N(int64(delay) / 4))
	s.retryAt = time.Now().Add(delay)
	if s.failures == 1 || s.failures%10 == 0 {
		slog.Warn("quic sink destination unreachable", "address", s.address, "attempts", s.failures, "retryIn", delay.Round(time.Millisecond))
	}
}

// dropLocked closes the connection with code and forgets it.
func (s *quicSink) dropLocked(code quic.ApplicationErrorCode, reason string) {
	if s.conn != nil {
		_ = s.conn.CloseWithError(code, reason)
	}
	s.conn, s.stream = nil, nil
}

// finishStreamLocked ends the current stream with a FIN and waits until the
// destination finishes its side (a GoFi QUIC source does once it has read
// everything, so its EOF means every message was received) or ctx ends.
// Later sends open a new stream on the same connection. The caller holds
// s.mu. A nil error means the stream's data is known to be delivered, or
// there was no stream to finish.
func (s *quicSink) finishStreamLocked(ctx context.Context) error {
	stream := s.stream
	s.stream = nil
	if stream == nil || s.conn == nil || s.conn.Context().Err() != nil {
		return nil
	}
	if err := stream.Close(); err != nil {
		return nil // already reset by the peer; nothing left to deliver
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(networkWriteTimeout)
	}
	_ = stream.SetReadDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = stream.SetReadDeadline(time.Now()) })
	defer stop()
	_, err := io.Copy(io.Discard, stream)
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	var streamErr *quic.StreamError
	if errors.As(err, &streamErr) {
		return nil // the peer stopped reading or ended the stream: delivered or refused, either way done
	}
	return fmt.Errorf("quic sink: flush: %s did not finish the stream: %w", s.address, err)
}

// Flush finishes the current stream and waits for the destination to
// acknowledge it, or for ctx.
func (s *quicSink) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finishStreamLocked(ctx)
}

// Close lets the destination finish the current stream (bounded by
// networkWriteTimeout), then ends the connection with application error
// code 0. Without the wait, closing the connection would discard the
// messages the last sends left in the send buffer.
func (s *quicSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), networkWriteTimeout)
	defer cancel()
	if err := s.finishStreamLocked(ctx); err != nil {
		slog.Warn("quic sink closed with unacknowledged data", "address", s.address, "error", err)
	}
	s.dropLocked(quicCloseNormalSink, "sink closed")
	return nil
}
