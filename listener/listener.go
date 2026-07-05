// Package listener opens a TCP or UDP network port and runs a pipeline
// for each incoming connection. Each Listener owns its sinks (NATS conn, etc.)
// and its stage chain independently — the controller creates and cancels
// listeners without any shared state between them.
package listener

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// Listener manages a bound network port and dispatches connections to pipelines.
type Listener struct {
	def spec.ListenerDefinition
	ln  net.Listener

	sinks          []sink.Sink
	deadLetterSink sink.Sink // nil if not configured

	// chain is built exactly ONCE per listener (not per connection) so that
	// stateful stages — rate-limit's counter, dedup's seen-keys map — are
	// shared across every connection this listener ever accepts, rather than
	// silently resetting each time a client reconnects.
	chain *filter.Chain

	Tap *pipeline.Tap
}

// New creates a Listener: connects the sink(s), builds the stage chain,
// binds the port. Fails fast if any operation fails so the controller gets
// an immediate error.
func New(def spec.ListenerDefinition) (*Listener, error) {
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

	ln, err := net.Listen(network, addr)
	if err != nil {
		closeAll(sinks, deadLetterSink)
		return nil, fmt.Errorf("listener %q: bind %s%s: %w", def.Name, network, addr, err)
	}

	slog.Info("listener bound",
		"name", def.Name,
		"addr", ln.Addr(),
		"framing", def.Framing.Mode,
		"parser", def.Parser.Format,
		"sinks", len(def.Sinks),
		"stages", len(def.Stages),
	)

	return &Listener{
		def:            def,
		ln:             ln,
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
	go func() {
		<-ctx.Done()
		l.ln.Close()
	}()

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

// Close releases the bound port, all sink connections, and the dead-letter
// sink connection (if configured).
func (l *Listener) Close() {
	l.ln.Close()
	closeAll(l.sinks, l.deadLetterSink)
}
