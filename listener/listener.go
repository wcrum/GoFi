// Package listener opens a TCP or UDP network port and runs a pipeline
// for each incoming connection. Each Listener owns its sink (NATS conn, etc.)
// independently — the controller creates and cancels listeners without any
// shared state between them.
package listener

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/cnak-us/gogo/pipeline"
	"github.com/cnak-us/gogo/sink"
	"github.com/cnak-us/gogo/spec"
)

// Listener manages a bound network port and dispatches connections to pipelines.
type Listener struct {
	def spec.ListenerDefinition
	ln  net.Listener
	sk  sink.Sink
	Tap *pipeline.Tap
}

// New creates a Listener: connects the sink, binds the port. Fails fast if
// either operation fails so the controller gets an immediate error.
func New(def spec.ListenerDefinition) (*Listener, error) {
	sk, err := sink.New(def.Sink, def.Name)
	if err != nil {
		return nil, fmt.Errorf("listener %q: sink: %w", def.Name, err)
	}

	addr := fmt.Sprintf(":%d", def.Source.Port)
	network := def.Source.Network
	if network == "" {
		network = "tcp"
	}

	ln, err := net.Listen(network, addr)
	if err != nil {
		sk.Close()
		return nil, fmt.Errorf("listener %q: bind %s%s: %w", def.Name, network, addr, err)
	}

	slog.Info("listener bound",
		"name", def.Name,
		"addr", ln.Addr(),
		"framing", def.Framing.Mode,
		"parser", def.Parser.Format,
		"sink", def.Sink.Type,
	)

	return &Listener{def: def, ln: ln, sk: sk, Tap: pipeline.NewTap()}, nil
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

	p, err := pipeline.New(l.def, conn, l.sk, l.Tap)
	if err != nil {
		slog.Error("pipeline build error", "listener", l.def.Name, "error", err)
		return
	}
	p.Run(ctx, remote)
	slog.Info("client disconnected", "listener", l.def.Name, "remote", remote)
}

// Close releases the bound port and the sink connection.
func (l *Listener) Close() {
	l.ln.Close()
	l.sk.Close()
}
