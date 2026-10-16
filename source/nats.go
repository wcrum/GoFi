package source

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// runNATS holds a core NATS subscription (optionally in a queue group) until
// ctx ends or the connection closes. The client's own reconnects keep the
// subscription; a closed connection returns so reconnect can start over.
func runNATS(ctx context.Context, flow string, cfg *spec.NATSSubscribe, emit Emit) error {
	opts, err := sink.NATSOptions(cfg.Auth)
	if err != nil {
		return err
	}
	closed := make(chan struct{})
	opts = append(opts, nats.Name("gofi-"+flow), nats.MaxReconnects(-1), nats.ClosedHandler(func(*nats.Conn) { close(closed) }))
	conn, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return err
	}
	defer conn.Close()
	handler := func(msg *nats.Msg) {
		observability.SourceEvent(flow, "nats", "ok")
		emit(ctx, msg.Data, "nats:"+msg.Subject)
	}
	var sub *nats.Subscription
	if cfg.Queue != "" {
		sub, err = conn.QueueSubscribe(cfg.Subject, cfg.Queue, handler)
	} else {
		sub, err = conn.Subscribe(cfg.Subject, handler)
	}
	if err != nil {
		return fmt.Errorf("subscribe %q: %w", cfg.Subject, err)
	}
	defer sub.Unsubscribe()
	select {
	case <-ctx.Done():
		return nil
	case <-closed:
		return fmt.Errorf("NATS connection closed")
	}
}
