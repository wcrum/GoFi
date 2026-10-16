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

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/framing"
	"github.com/wcrum/gofi/parser"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// Listener manages a bound network port and dispatches connections to pipelines.
type Listener struct {
	def    spec.ListenerDefinition
	ln     net.Listener
	packet net.PacketConn

	sinks          []sink.Sink
	deadLetterSink sink.Sink // nil if not configured

	// chain is built exactly ONCE per listener (not per connection) so that
	// stateful stages — rate-limit's counter, dedup's seen-keys map — are
	// shared across every connection this listener ever accepts, rather than
	// silently resetting each time a client reconnects.
	chain *filter.Chain

	Tap       *pipeline.Tap
	closeOnce sync.Once
}

// New creates a Listener: connects the sink(s), builds the stage chain,
// binds the port. Fails fast if any operation fails so the controller gets
// an immediate error.
func New(def spec.ListenerDefinition) (*Listener, error) {
	if err := def.Source.Validate(); err != nil {
		return nil, err
	}
	if _, err := parser.New(def.Parser); err != nil {
		return nil, err
	}
	if _, err := framing.New(def.Framing, strings.NewReader("")); err != nil {
		return nil, err
	}
	for _, cfg := range def.Sinks {
		if cfg.When != "" {
			if _, err := filter.CompileCEL(cfg.When); err != nil {
				return nil, err
			}
		}
	}
	sinks := make([]sink.Sink, 0, len(def.Sinks))
	for i, sc := range def.Sinks {
		sk, err := sink.New(sc, def.Name)
		if err != nil {
			closeAll(sinks, nil)
			return nil, fmt.Errorf("listener %q: sink %d: %w", def.Name, i, err)
		}
		sinks = append(sinks, sk)
	}

	var deadLetterSink sink.Sink
	if def.DeadLetterSink != nil {
		var err error
		deadLetterSink, err = sink.New(*def.DeadLetterSink, def.Name+"-deadletter")
		if err != nil {
			closeAll(sinks, nil)
			return nil, fmt.Errorf("listener %q: dead-letter sink: %w", def.Name, err)
		}
	}

	chain, err := filter.NewChain(def.Stages)
	if err != nil {
		closeAll(sinks, deadLetterSink)
		return nil, fmt.Errorf("listener %q: stages: %w", def.Name, err)
	}

	addr := fmt.Sprintf(":%d", def.Source.Port)
	network := def.Source.Network
	if network == "" {
		network = "tcp"
	}

	var ln net.Listener
	var packet net.PacketConn
	if network == "udp" {
		packet, err = net.ListenPacket(network, addr)
	} else {
		ln, err = net.Listen(network, addr)
	}
	if err != nil {
		closeAll(sinks, deadLetterSink)
		return nil, fmt.Errorf("listener %q: bind %s%s: %w", def.Name, network, addr, err)
	}

	slog.Info("listener bound",
		"name", def.Name,
		"addr", addr,
		"framing", def.Framing.Mode,
		"parser", def.Parser.Format,
		"sinks", len(def.Sinks),
		"stages", len(def.Stages),
	)

	return &Listener{
		def:            def,
		ln:             ln,
		packet:         packet,
		sinks:          sinks,
		deadLetterSink: deadLetterSink,
		chain:          chain,
		Tap:            pipeline.NewTap(),
	}, nil
}

func closeAll(sinks []sink.Sink, deadLetterSink sink.Sink) {
	for _, s := range sinks {
		s.Close()
	}
	if deadLetterSink != nil {
		deadLetterSink.Close()
	}
}

// Run accepts connections and processes them until ctx is cancelled.
func (l *Listener) Run(ctx context.Context) {
	stop := context.AfterFunc(ctx, func() { l.closeEndpoint() })
	defer stop()
	if l.packet != nil {
		l.runPackets(ctx)
		return
	}

	for {
		conn, err := l.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("accept error", "listener", l.def.Name, "error", err)
			continue
		}
		go l.handleConn(ctx, conn)
	}
}

func (l *Listener) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	remote := conn.RemoteAddr().String()
	slog.Info("client connected", "listener", l.def.Name, "remote", remote)

	p, err := pipeline.New(l.def, conn, l.sinks, l.deadLetterSink, l.chain, l.Tap)
	if err != nil {
		slog.Error("pipeline build error", "listener", l.def.Name, "error", err)
		return
	}
	p.Run(ctx, remote)
	slog.Info("client disconnected", "listener", l.def.Name, "remote", remote)
}

// Each UDP datagram is an independent stream: raw framing preserves the
// datagram boundary while line/delimiter framing can split its payload.
func (l *Listener) runPackets(ctx context.Context) {
	buf := make([]byte, 65536)
	for {
		n, remote, err := l.packet.ReadFrom(buf)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("UDP read failed", "listener", l.def.Name, "error", err)
			}
			return
		}
		p, err := pipeline.New(l.def, bytes.NewReader(buf[:n]), l.sinks, l.deadLetterSink, l.chain, l.Tap)
		if err != nil {
			slog.Error("pipeline build error", "listener", l.def.Name, "error", err)
			continue
		}
		p.Run(ctx, remote.String())
	}
}

func (l *Listener) closeEndpoint() {
	if l.ln != nil {
		l.ln.Close()
	}
	if l.packet != nil {
		l.packet.Close()
	}
}

// Close releases the bound port, all sink connections, and the dead-letter
// sink connection (if configured).
func (l *Listener) Close() {
	l.closeOnce.Do(func() { l.closeEndpoint(); closeAll(l.sinks, l.deadLetterSink) })
}
