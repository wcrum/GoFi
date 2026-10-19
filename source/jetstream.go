package source

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// jetStreamConsumerConfig is the durable pull consumer GoFi creates or
// updates. Deliver policy is fixed when the consumer is first created.
func jetStreamConsumerConfig(flow string, cfg *spec.JetStreamSubscribe) jetstream.ConsumerConfig {
	name := cfg.Consumer
	if name == "" {
		name = "gofi-" + flow
	}
	policy := map[string]jetstream.DeliverPolicy{
		"": jetstream.DeliverAllPolicy, "all": jetstream.DeliverAllPolicy, "last": jetstream.DeliverLastPolicy,
		"new": jetstream.DeliverNewPolicy, "last_per_subject": jetstream.DeliverLastPerSubjectPolicy,
	}[cfg.DeliverPolicy]
	return jetstream.ConsumerConfig{
		Durable:       name,
		Description:   "GoFi flow " + flow,
		FilterSubject: cfg.Subject,
		DeliverPolicy: policy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       cfg.AckWaitDuration(),
	}
}

// runJetStream pulls from a durable consumer until ctx ends or the
// iterator fails. Each message is one emit and is acked after emit returns
// (the pipeline run, sinks included); a message not acked within ackWait is
// redelivered: at-least-once into the pipeline. Replicas sharing the
// consumer name share the stream's messages.
func runJetStream(ctx context.Context, flow string, cfg *spec.JetStreamSubscribe, emit Emit) error {
	opts, err := sink.NATSOptions(cfg.Auth)
	if err != nil {
		return err
	}
	opts = append(opts, nats.Name("gofi-"+flow), nats.MaxReconnects(-1))
	conn, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return err
	}
	defer conn.Close()
	js, err := jetstream.New(conn)
	if err != nil {
		return err
	}
	setupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	consumerCfg := jetStreamConsumerConfig(flow, cfg)
	consumer, err := js.CreateOrUpdateConsumer(setupCtx, cfg.Stream, consumerCfg)
	cancel()
	if err != nil {
		return fmt.Errorf("consumer %q on stream %q: %w", consumerCfg.Durable, cfg.Stream, err)
	}
	messages, err := consumer.Messages()
	if err != nil {
		return err
	}
	defer messages.Stop()
	for {
		msg, err := messages.Next(jetstream.NextContext(ctx))
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("consumer %q: %w", consumerCfg.Durable, err)
		}
		meta := jetStreamMeta(msg)
		if len(msg.Data()) > MaxMessageBytes {
			observability.SourceEvent(flow, "jetstream", "error")
			slog.Warn("jetstream: message over the size cap terminated", "flow", flow, "subject", msg.Subject(), "sequence", meta["jetstream.sequence"])
			_ = msg.Term()
			continue
		}
		observability.SourceEvent(flow, "jetstream", "ok")
		emit(message.WithSourceMeta(ctx, meta), msg.Data(), "jetstream:"+msg.Subject())
		if ctx.Err() != nil {
			return nil // unacked: redelivered after ackWait
		}
		if err := msg.Ack(); err != nil {
			observability.SourceEvent(flow, "jetstream", "error")
			slog.Warn("jetstream: ack failed; the message will be redelivered", "flow", flow, "error", err)
		}
	}
}

// jetStreamMeta is a message's metadata for meta["jetstream.*"].
func jetStreamMeta(msg jetstream.Msg) map[string]string {
	meta := map[string]string{"jetstream.subject": msg.Subject()}
	if md, err := msg.Metadata(); err == nil {
		meta["jetstream.stream"] = md.Stream
		meta["jetstream.consumer"] = md.Consumer
		meta["jetstream.sequence"] = strconv.FormatUint(md.Sequence.Stream, 10)
		meta["jetstream.delivered"] = strconv.FormatUint(md.NumDelivered, 10)
		meta["jetstream.timestamp"] = md.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	for key, values := range msg.Headers() {
		if len(values) > 0 {
			meta["jetstream.header."+key] = values[0]
		}
	}
	return meta
}
