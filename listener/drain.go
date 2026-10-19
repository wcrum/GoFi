package listener

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wcrum/gofi/sink"
)

// DefaultDrainTimeout bounds a graceful stop: how long in-flight frames may
// take to finish and sinks to flush before the listener is stopped hard.
const DefaultDrainTimeout = 20 * time.Second

// closeFlushTimeout bounds the sink flush of a hard Close.
const closeFlushTimeout = 5 * time.Second

// hardStopGrace is how long Shutdown waits for processing to end after its
// deadline passed and it stopped the listener hard.
const hardStopGrace = 2 * time.Second

// lifecycle is a Listener's stop machinery. Two contexts split a stop in two:
// accepting ends intake (the bound socket, a pulled source), processing ends
// the work already taken in. A graceful Shutdown ends only intake and waits;
// a hard stop (Run's context, Close, or an expired drain) ends both.
type lifecycle struct {
	accepting     context.Context
	stopAccepting context.CancelFunc
	processing    context.Context
	stopProcess   context.CancelFunc

	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	inflight sync.WaitGroup
	finished bool // no new work may start (guarded by mu)
	draining bool // read deadlines apply to open connections (guarded by mu)
	flushCtx context.Context

	started   atomic.Bool
	done      chan struct{}
	sinksOnce sync.Once
}

func newListener(l *Listener) *Listener {
	l.accepting, l.stopAccepting = context.WithCancel(context.Background())
	l.processing, l.stopProcess = context.WithCancel(context.Background())
	l.conns = map[net.Conn]struct{}{}
	l.done = make(chan struct{})
	return l
}

// track registers one unit of in-flight work (a connection, or a pulled
// message when conn is nil). It fails once the listener finished.
func (l *Listener) track(conn net.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return false
	}
	l.inflight.Add(1)
	if conn != nil {
		l.conns[conn] = struct{}{}
		if l.draining {
			_ = conn.SetReadDeadline(time.Now())
		}
	}
	return true
}

func (l *Listener) untrack(conn net.Conn) {
	if conn != nil {
		l.mu.Lock()
		delete(l.conns, conn)
		l.mu.Unlock()
	}
	l.inflight.Done()
}

// waitInflight ends intake for good and waits for the work already started.
func (l *Listener) waitInflight() {
	l.mu.Lock()
	l.finished = true
	l.mu.Unlock()
	l.inflight.Wait()
}

// hardStop stops intake and abandons work in progress: open connections
// close and pipelines stop before their next frame.
func (l *Listener) hardStop() {
	l.stopAccepting()
	l.closeEndpoint()
	l.stopProcess()
}

// Shutdown stops the listener gracefully. It stops accepting connections,
// datagrams, and pulled messages, lets frames already received finish the
// pipeline, flushes the sinks, and closes them. Frames still unread on an open
// connection are not waited for: senders are expected to reconnect to the
// replacement runtime. When ctx ends first, the listener is stopped hard and
// ctx's error is returned.
func (l *Listener) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	l.draining = true
	l.flushCtx = ctx
	// A read in progress returns at once; frames the framer already buffered
	// still come out before the read error ends the stream.
	for conn := range l.conns {
		_ = conn.SetReadDeadline(time.Now())
	}
	l.mu.Unlock()
	l.stopAccepting()
	l.closeEndpoint()

	var err error
	if l.started.Load() {
		select {
		case <-l.done:
		case <-ctx.Done():
			err = ctx.Err()
			slog.Warn("drain deadline passed; stopping flow hard", "listener", l.def.Name)
			l.stopProcess()
			select {
			case <-l.done:
			case <-time.After(hardStopGrace):
			}
		}
	}
	l.closeSinks(ctx)
	if err == nil {
		err = ctx.Err()
	}
	return err
}

// Close stops the listener hard, then flushes and closes its sinks.
func (l *Listener) Close() {
	l.hardStop()
	l.mu.Lock()
	ctx := l.flushCtx
	l.mu.Unlock()
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), closeFlushTimeout)
		defer cancel()
	}
	l.closeSinks(ctx)
}

// closeSinks flushes buffering sinks within ctx, then closes every sink once.
func (l *Listener) closeSinks(ctx context.Context) {
	l.sinksOnce.Do(func() {
		all := append([]sink.Sink(nil), l.sinks...)
		if l.deadLetterSink != nil {
			all = append(all, l.deadLetterSink)
		}
		for _, s := range all {
			if err := sink.Flush(ctx, s); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("sink flush failed", "listener", l.def.Name, "error", err)
			}
		}
		closeAll(l.sinks, l.deadLetterSink)
	})
}
