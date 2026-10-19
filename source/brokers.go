package source

import (
	"context"
	"strings"

	"github.com/wcrum/gofi/spec"
)

// brokerKind names a broker subscribe source (kafka, amqp, jetstream,
// redis), or "" for any other source.
func brokerKind(s *spec.SubscribeSource) string {
	switch {
	case s == nil:
		return ""
	case s.Kafka != nil:
		return "kafka"
	case s.AMQP != nil:
		return "amqp"
	case s.JetStream != nil:
		return "jetstream"
	case s.Redis != nil:
		return "redis"
	}
	return ""
}

// brokerConnect is one connection attempt for a broker source, run under
// reconnect.
func brokerConnect(flow string, s *spec.SubscribeSource, emit Emit) func(context.Context) error {
	switch brokerKind(s) {
	case "kafka":
		return func(ctx context.Context) error { return runKafka(ctx, flow, s.Kafka, emit) }
	case "amqp":
		return func(ctx context.Context) error { return runAMQP(ctx, flow, s.AMQP, emit) }
	case "jetstream":
		return func(ctx context.Context) error { return runJetStream(ctx, flow, s.JetStream, emit) }
	case "redis":
		return func(ctx context.Context) error { return runRedis(ctx, flow, s.Redis, emit) }
	}
	return nil
}

// describeBroker is Describe for broker sources; it never includes
// credentials.
func describeBroker(s *spec.SubscribeSource) string {
	switch brokerKind(s) {
	case "kafka":
		return "subscribe kafka " + strings.Join(s.Kafka.Topics, ",")
	case "amqp":
		if s.AMQP.Queue != "" {
			return "subscribe amqp queue " + s.AMQP.Queue
		}
		return "subscribe amqp exchange " + s.AMQP.Exchange
	case "jetstream":
		return "subscribe jetstream " + s.JetStream.Stream
	case "redis":
		if s.Redis.Stream != "" {
			return "subscribe redis stream " + s.Redis.Stream
		}
		return "subscribe redis " + strings.Join(s.Redis.Channels, ",")
	}
	return ""
}
