// Package listener opens a TCP or UDP network port and runs a pipeline
// for each incoming connection. Each Listener owns its sinks (NATS conn, etc.)
// and its stage chain independently — the controller creates and cancels
// listeners without any shared state between them.
package listener

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/framing"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/parser"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/source"
	"github.com/wcrum/gofi/spec"
)

// Listener manages a bound network port and dispatches connections to pipelines.
type Listener struct {
	def    spec.FlowDefinition
	ln     net.Listener
	packet net.PacketConn
	quic   *quicListener // network quic (quic.go); every stream is served like a TCP connection

	sinks          []sink.Sink
	deadLetterSink sink.Sink // nil if not configured

	// chain is built exactly ONCE per listener (not per connection) so that
	// stateful stages — rate-limit's counter, dedup's seen-keys map — are
	// shared across every connection this listener ever accepts, rather than
	// silently resetting each time a client reconnects.
	chain *filter.Chain

	// pipe is the one Pipeline every connection, datagram, and pulled message
	// of this listener runs through: parser, routing conditions, and metric
	// handles are built once here, not per stream.
	pipe *pipeline.Pipeline

	// limits are the capacity knobs (connection cap, idle timeout, UDP
	// buffer, worker counts), from the environment unless a test sets them.
	limits    Limits
	connCount atomic.Int64
	connGauge prometheus.Gauge
	refuseLog time.Time // last "connection cap reached" log (guarded by mu)

	Tap *pipeline.Tap

	lifecycle
}

// New creates a Listener: connects the sink(s), builds the stage chain,
// binds the port. Fails fast if any operation fails so the controller gets
// an immediate error.
func New(def spec.FlowDefinition) (*Listener, error) {
	// Older controllers may send parser.decoders; Python steps may name only
	// a function and take the flow's default library.
	def, err := def.Prepared()
	if err != nil {
		return nil, err
	}
	if err := def.Source.Validate(); err != nil {
		return nil, err
	}
	// Route health describes this definition's destinations only.
	observability.ResetRoutes(def.Name)
	if _, err := parser.ForFlow(def); err != nil {
		return nil, err
	}
	if _, err := framing.New(def.Framing, strings.NewReader("")); err != nil {
		return nil, err
	}
	// Sink and dead-letter conditions compile once for the listener.
	routing, err := pipeline.CompileRouting(def)
	if err != nil {
		return nil, err
	}
	sinks := make([]sink.Sink, 0, len(def.Sinks))
	for i, sc := range def.Sinks {
		sk, err := sink.New(sc, def.Name)
		if err != nil {
			closeAll(sinks, nil)
			return nil, fmt.Errorf("listener %q: sink %d: %w", def.Name, i, err)
		}
		// HTTP-family sinks deliver from a bounded queue (sink.Queued) so a
		// slow destination never stalls the pipeline goroutine.
		sinks = append(sinks, sink.Queued(sk, sc.Type, def.Name, observability.RouteSink(i)))
	}

	var deadLetterSink sink.Sink
	if def.DeadLetterSink != nil {
		var err error
		deadLetterSink, err = sink.New(*def.DeadLetterSink, def.Name+"-deadletter")
		if err != nil {
			closeAll(sinks, nil)
			return nil, fmt.Errorf("listener %q: dead-letter sink: %w", def.Name, err)
		}
		deadLetterSink = sink.Queued(deadLetterSink, def.DeadLetterSink.Type, def.Name, observability.RouteDeadLetter)
	}

	chain, err := filter.NewChain(def.Stages)
	if err != nil {
		closeAll(sinks, deadLetterSink)
		return nil, fmt.Errorf("listener %q: stages: %w", def.Name, err)
	}

	tap := pipeline.NewTap()
	pipe, err := pipeline.NewShared(def, sinks, deadLetterSink, chain, tap, routing)
	if err != nil {
		closeAll(sinks, deadLetterSink)
		return nil, fmt.Errorf("listener %q: pipeline: %w", def.Name, err)
	}
	limits := processLimits()

	if def.Source.Listen == nil {
		// Fetch/subscribe flows bind no port; Run connects outward.
		slog.Info("flow source configured", "name", def.Name, "source", source.Describe(def.Source), "framing", def.Framing.Mode, "parser", def.Parser.Format, "sinks", len(def.Sinks), "stages", len(def.Stages), "workers", limits.SourceWorkers)
		return newListener(&Listener{def: def, sinks: sinks, deadLetterSink: deadLetterSink, chain: chain, pipe: pipe, limits: limits, Tap: tap}), nil
	}

	addr := fmt.Sprintf(":%d", def.Source.Listening().Port)
	network := def.Source.Listening().Network
	if network == "" {
		network = "tcp"
	}

	var ln net.Listener
	var packet net.PacketConn
	var q *quicListener
	switch network {
	case "udp":
		packet, err = net.ListenPacket(network, addr)
	case spec.NetworkQUIC:
		q, err = listenQUIC(def, addr, limits)
	default:
		ln, err = net.Listen(network, addr)
	}
	if err != nil {
		closeAll(sinks, deadLetterSink)
		return nil, fmt.Errorf("listener %q: bind %s%s: %w", def.Name, network, addr, err)
	}

	if udp, ok := packet.(*net.UDPConn); ok {
		effective, drops := tuneUDPSocket(udp, limits.UDPReadBuffer)
		observability.UDPReadBuffer(def.Name, effective)
		slog.Info("UDP socket tuned", "name", def.Name, "requestedReadBuffer", limits.UDPReadBuffer, "effectiveReadBuffer", effective, "kernelDropAccounting", drops, "workers", limits.UDPWorkers)
	}

	slog.Info("listener bound",
		"name", def.Name,
		"network", network,
		"addr", addr,
		"framing", def.Framing.Mode,
		"parser", def.Parser.Format,
		"sinks", len(def.Sinks),
		"stages", len(def.Stages),
		"maxConnections", limits.MaxConnections,
		"idleTimeout", limits.IdleTimeout,
	)

	return newListener(&Listener{
		def:            def,
		ln:             ln,
		packet:         packet,
		quic:           q,
		sinks:          sinks,
		deadLetterSink: deadLetterSink,
		chain:          chain,
		pipe:           pipe,
		limits:         limits,
		connGauge:      observability.Connections(def.Name),
		Tap:            tap,
	}), nil
}

func closeAll(sinks []sink.Sink, deadLetterSink sink.Sink) {
	for _, s := range sinks {
		s.Close()
	}
	if deadLetterSink != nil {
		deadLetterSink.Close()
	}
}

// Run accepts connections and processes them until ctx is cancelled (a hard
// stop) or Shutdown drains the listener. It returns once every connection,
// datagram, or pulled message it started has finished processing.
func (l *Listener) Run(ctx context.Context) {
	l.started.Store(true)
	defer close(l.done)
	stop := context.AfterFunc(ctx, l.hardStop)
	defer stop()
	defer l.waitInflight()
	if l.def.Source.Listen == nil {
		l.runPulled()
		return
	}
	if l.packet != nil {
		l.runPackets()
		return
	}
	if l.quic != nil {
		l.runQUIC()
		return
	}

	for {
		conn, err := l.ln.Accept()
		if err != nil {
			if l.accepting.Err() != nil {
				return
			}
			slog.Warn("accept error", "listener", l.def.Name, "error", err)
			observability.Report(l.def.Name, observability.SeverityWarning, observability.ComponentSource, "accept: "+err.Error())
			continue
		}
		if l.connCount.Load() >= int64(l.limits.MaxConnections) {
			// Refuse cleanly: accept so the backlog does not fill with
			// half-open connections, then close. The sender sees EOF.
			conn.Close()
			l.refuse()
			continue
		}
		if !l.track(conn) {
			conn.Close()
			return
		}
		go l.handleConn(conn)
	}
}

// refuseLogInterval rate-limits the connection-cap warning.
const refuseLogInterval = 10 * time.Second

func (l *Listener) refuse() {
	observability.ConnectionRefused(l.def.Name)
	l.mu.Lock()
	warn := time.Since(l.refuseLog) >= refuseLogInterval
	if warn {
		l.refuseLog = time.Now()
	}
	l.mu.Unlock()
	if warn {
		slog.Warn("connection cap reached; refusing new connections or streams", "listener", l.def.Name, "maxConnections", l.limits.MaxConnections)
		observability.Report(l.def.Name, observability.SeverityWarning, observability.ComponentSource, fmt.Sprintf("connection cap (%d) reached; new connections (and QUIC streams) are refused", l.limits.MaxConnections))
	}
}

func (l *Listener) handleConn(conn net.Conn) {
	l.connCount.Add(1)
	if l.connGauge != nil {
		l.connGauge.Inc()
	}
	defer func() {
		l.connCount.Add(-1)
		if l.connGauge != nil {
			l.connGauge.Dec()
		}
	}()
	l.serveConn(conn)
}

// serveConn runs one tracked byte stream (a TCP connection or a QUIC
// stream) through the pipeline until it ends, the idle timeout passes, or
// processing stops; then closes and untracks it.
func (l *Listener) serveConn(conn net.Conn) {
	defer l.untrack(conn)
	defer conn.Close()
	stop := context.AfterFunc(l.processing, func() { conn.Close() })
	defer stop()
	remote := conn.RemoteAddr().String()
	slog.Info("client connected", "listener", l.def.Name, "remote", remote)

	var stream net.Conn = conn
	var idle *idleConn
	if l.limits.IdleTimeout > 0 {
		idle = &idleConn{Conn: conn, timeout: l.limits.IdleTimeout, l: l}
		stream = idle
	}
	l.pipe.RunReader(l.processing, stream, remote)
	if idle != nil && idle.idle.Load() {
		observability.ConnectionIdleClosed(l.def.Name)
		slog.Info("client idle; connection closed", "listener", l.def.Name, "remote", remote, "idleTimeout", l.limits.IdleTimeout)
		return
	}
	slog.Info("client disconnected", "listener", l.def.Name, "remote", remote)
}

// Each UDP datagram is an independent stream: raw framing preserves the
// datagram boundary while line/delimiter framing can split its payload.
// With one worker (the default) datagrams are processed in order, so the one
// being processed when the socket closes always finishes; with more, they
// are processed concurrently and out of order.
func (l *Listener) runPackets() {
	workers := l.limits.UDPWorkers
	if workers <= 1 {
		l.readPackets(0)
		return
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); l.readPackets(i) }(i)
	}
	wg.Wait()
}

// readPackets is one UDP reader loop. On Linux each read also carries the
// kernel's cumulative drop count for the socket (SO_RXQ_OVFL); worker 0
// turns it into gofi_udp_kernel_drops_total.
func (l *Listener) readPackets(worker int) {
	buf := make([]byte, 65536)
	oob := make([]byte, oobSize)
	udp, _ := l.packet.(*net.UDPConn)
	var lastDrops uint64
	haveDrops := false
	for {
		var n int
		var remote net.Addr
		var err error
		if udp != nil && oobSize > 0 {
			var oobn int
			var from *net.UDPAddr
			n, oobn, _, from, err = udp.ReadMsgUDP(buf, oob)
			if from != nil {
				remote = from
			}
			if err == nil && worker == 0 {
				if drops, ok := kernelDrops(oob[:oobn]); ok {
					if haveDrops && drops > lastDrops {
						observability.UDPKernelDrops(l.def.Name, drops-lastDrops)
					}
					lastDrops, haveDrops = drops, true
				}
			}
		} else {
			n, remote, err = l.packet.ReadFrom(buf)
		}
		if err != nil {
			if l.accepting.Err() == nil {
				slog.Warn("UDP read failed", "listener", l.def.Name, "error", err)
				observability.Report(l.def.Name, observability.SeverityError, observability.ComponentSource, "UDP read: "+err.Error())
			}
			return
		}
		from := ""
		if remote != nil {
			from = remote.String()
		}
		l.pipe.RunReader(l.processing, bytes.NewReader(buf[:n]), from)
	}
}

// runPulled runs a fetch or subscribe source: each response or message is one
// stream through the pipeline, exactly like one connection or datagram. The
// source stops on drain; a message it already handed over still completes.
//
// Sources that deliver at most once may spread messages over
// Limits.SourceWorkers goroutines (out of order); acknowledging brokers
// always process on their consumer goroutine so the ack means the pipeline ran.
func (l *Listener) runPulled() {
	process := func(ctx context.Context, data []byte, remote string) {
		// Per-message source metadata (Kafka headers, Zenoh keys, Python
		// gofi.Frame meta) rides on the emit context; processing still
		// follows the listener's lifecycle.
		l.pipe.RunReader(message.WithSourceMeta(l.processing, message.SourceMeta(ctx)), bytes.NewReader(data), remote)
	}
	emit := func(ctx context.Context, data []byte, remote string) {
		if !l.track(nil) {
			return
		}
		defer l.untrack(nil)
		process(ctx, data, remote)
	}
	var pool chan pulledMessage
	var workers sync.WaitGroup
	if n := l.limits.SourceWorkers; n > 1 && !source.AcksAfterEmit(l.def.Source) {
		pool = make(chan pulledMessage, n)
		for i := 0; i < n; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for m := range pool {
					process(m.ctx, m.data, m.remote)
					l.untrack(nil)
				}
			}()
		}
		emit = func(ctx context.Context, data []byte, remote string) {
			if !l.track(nil) {
				return
			}
			// Blocks when every worker is busy: back-pressure to the
			// client (NATS then counts slow-consumer drops).
			pool <- pulledMessage{ctx: ctx, data: data, remote: remote}
		}
	}
	err := source.Run(l.accepting, l.def.Name, l.def.Source, emit)
	if pool != nil {
		close(pool)
		workers.Wait()
	}
	if err != nil && l.accepting.Err() == nil {
		slog.Error("flow source stopped", "listener", l.def.Name, "error", err)
		observability.Report(l.def.Name, observability.SeverityError, observability.ComponentSource, "source stopped: "+err.Error())
	}
}

// pulledMessage is one message handed to the source worker pool.
type pulledMessage struct {
	ctx    context.Context
	data   []byte
	remote string
}

func (l *Listener) closeEndpoint() {
	if l.ln != nil {
		l.ln.Close()
	}
	if l.packet != nil {
		l.packet.Close()
	}
	if l.quic != nil {
		l.quic.ln.Close()
		if !l.started.Load() {
			// Run never started, so runQUIC will not release the socket.
			l.quic.shutdown(quicCloseNormal, "listener stopped")
		}
	}
}
