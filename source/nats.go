package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

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
	// The client buffers messages the handler has not taken yet; past the
	// pending limits it drops them ("slow consumer") rather than growing
	// without bound. Each drop is counted on gofi_source_slow_consumer_total.
	var lastDropped int64
	var dropMu sync.Mutex
	opts = append(opts, nats.Name("gofi-"+flow), nats.MaxReconnects(-1), nats.ClosedHandler(func(*nats.Conn) { close(closed) }),
		nats.ErrorHandler(func(_ *nats.Conn, sub *nats.Subscription, err error) {
			if errors.Is(err, nats.ErrSlowConsumer) && sub != nil {
				dropped, derr := sub.Dropped()
				dropMu.Lock()
				delta := int64(dropped) - lastDropped
				if derr == nil && delta > 0 {
					lastDropped = int64(dropped)
				}
				dropMu.Unlock()
				if derr == nil && delta > 0 {
					observability.SourceSlowConsumer(flow, "nats", uint64(delta))
				}
				slog.Warn("NATS slow consumer: the pipeline is not keeping up and messages were dropped by the client", "flow", flow, "subject", cfg.Subject, "dropped", dropped)
				observability.Report(flow, observability.SeverityWarning, observability.ComponentSource, "NATS slow consumer: messages dropped because the pipeline fell behind")
				return
			}
			if err != nil {
				slog.Warn("NATS client error", "flow", flow, "error", err)
			}
		}))
	conn, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return err
	}
	defer conn.Close()
	handler := func(msg *nats.Msg) {
		observability.SourceEvent(flow, "nats", "ok")
		emit(ctx, msg.Data, "nats:"+msg.Subject)
	}
	// Replica safety: a queue group delivers each message to one member, so
	// scaled-out replicas split the subject instead of each processing it.
	// The default group is gofi-<flow>; set queue to "-" for plain fan-out.
	queue := NATSQueueGroup(flow, cfg)
	var sub *nats.Subscription
	if queue != "" {
		sub, err = conn.QueueSubscribe(cfg.Subject, queue, handler)
	} else {
		sub, err = conn.Subscribe(cfg.Subject, handler)
	}
	if err != nil {
		return fmt.Errorf("subscribe %q: %w", cfg.Subject, err)
	}
	defer sub.Unsubscribe()
	if err := sub.SetPendingLimits(natsPendingMsgs, natsPendingBytes); err != nil {
		return fmt.Errorf("pending limits: %w", err)
	}
	select {
	case <-ctx.Done():
		return nil
	case <-closed:
		return fmt.Errorf("NATS connection closed")
	}
}

// Pending limits of a core NATS subscription: how much the client buffers
// before it starts dropping (slow consumer). 64 Ki messages / 64 MiB bounds
// the memory one subscription can hold while the pipeline is behind.
const (
	natsPendingMsgs  = 65536
	natsPendingBytes = 64 << 20
)

// NATSQueueGroup is the queue group a flow subscribes in: cfg.Queue, or
// gofi-<flow> when empty, or none when cfg.Queue is "-" (every replica
// receives every message).
func NATSQueueGroup(flow string, cfg *spec.NATSSubscribe) string {
	switch q := cfg.Queue; q {
	case "":
		return "gofi-" + flow
	case "-":
		return ""
	default:
		return q
	}
}
