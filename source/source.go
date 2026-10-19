// Package source runs pulled flow sources: GoFi fetches over HTTP or
// subscribes to NATS, MQTT, WebSocket, Server-Sent Events, Kafka, AMQP,
// NATS JetStream, or Redis, or calls a Python @gofi.fetcher/@gofi.streamer
// in its library's isolated worker, and hands each response or message to the
// flow's pipeline as one stream of bytes. Framing
// still applies; "raw" (the default) makes each message exactly one frame.
//
// Delivery is at-most-once for fetch, NATS, MQTT, WebSocket, SSE, and Redis
// Pub/Sub: there is no durable cursor. Kafka, AMQP, JetStream, and Redis
// Streams acknowledge each message only after its pipeline run returns, so
// a crash redelivers unacknowledged messages (at-least-once into the
// pipeline; sink failures are still not retried). Sources reconnect
// with capped exponential backoff and never run in the controller process.
package source

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// MaxMessageBytes caps one fetched body or received message.
const MaxMessageBytes = 16 << 20

// Emit runs one received message through the flow pipeline. remote describes
// where it came from (for live traffic), e.g. "nats:sensors.a".
type Emit func(ctx context.Context, data []byte, remote string)

// Run runs a fetch or subscribe source until ctx ends.
func Run(ctx context.Context, flow string, src spec.FlowSource, emit Emit) error {
	switch {
	case src.Fetch != nil && src.Fetch.HTTP != nil:
		return runFetch(ctx, flow, src.Fetch, emit)
	case src.Fetch != nil && src.Fetch.S3 != nil:
		return runS3(ctx, flow, src.Fetch, emit)
	case src.Fetch != nil && src.Fetch.SQL != nil:
		return runSQL(ctx, flow, src.Fetch, emit)
	case src.Subscribe != nil && src.Subscribe.NATS != nil:
		return reconnect(ctx, flow, "nats", func(ctx context.Context) error { return runNATS(ctx, flow, src.Subscribe.NATS, emit) })
	case src.Subscribe != nil && src.Subscribe.MQTT != nil:
		return reconnect(ctx, flow, "mqtt", func(ctx context.Context) error { return runMQTT(ctx, flow, src.Subscribe.MQTT, emit) })
	case src.Subscribe != nil && src.Subscribe.WebSocket != nil:
		return reconnect(ctx, flow, "websocket", func(ctx context.Context) error { return runWebSocket(ctx, flow, src.Subscribe.WebSocket, emit) })
	case src.Subscribe != nil && src.Subscribe.SSE != nil:
		stream := &sseStream{cfg: src.Subscribe.SSE}
		return reconnect(ctx, flow, "sse", func(ctx context.Context) error { return stream.run(ctx, flow, emit) })
	case src.Subscribe != nil && src.Subscribe.Zenoh != nil:
		z, err := newZenohSource(flow, src.Subscribe.Zenoh, emit)
		if err != nil {
			return err
		}
		return reconnect(ctx, flow, "zenoh", z.run)
	case brokerKind(src.Subscribe) != "":
		return reconnect(ctx, flow, brokerKind(src.Subscribe), brokerConnect(flow, src.Subscribe, emit))
	case src.Python != nil:
		return runPython(ctx, flow, src.Python, emit)
	}
	return fmt.Errorf("flow %q has no fetch, subscribe, or python source", flow)
}

// Describe is a short label for logs: "fetch https://…" or "subscribe nats".
func Describe(src spec.FlowSource) string {
	switch {
	case src.Fetch != nil && src.Fetch.HTTP != nil:
		return "fetch " + src.Fetch.HTTP.URL
	case src.Fetch != nil && src.Fetch.S3 != nil:
		return "fetch s3://" + src.Fetch.S3.Bucket + "/" + src.Fetch.S3.Prefix
	case src.Fetch != nil && src.Fetch.SQL != nil:
		return "fetch " + sqlOrigin(src.Fetch.SQL)
	case src.Subscribe != nil && src.Subscribe.NATS != nil:
		return "subscribe nats " + src.Subscribe.NATS.Subject
	case src.Subscribe != nil && src.Subscribe.MQTT != nil:
		return "subscribe mqtt " + src.Subscribe.MQTT.Topic
	case src.Subscribe != nil && src.Subscribe.WebSocket != nil:
		return "subscribe websocket " + src.Subscribe.WebSocket.URL
	case src.Subscribe != nil && src.Subscribe.SSE != nil:
		return "subscribe sse " + src.Subscribe.SSE.URL
	case src.Subscribe != nil && src.Subscribe.Zenoh != nil:
		return "subscribe zenoh " + src.Subscribe.Zenoh.KeyExpr
	case brokerKind(src.Subscribe) != "":
		return describeBroker(src.Subscribe)
	case src.Python != nil:
		return "python " + src.Python.Library + "." + src.Python.Function
	}
	return src.Kind()
}

// backoff bounds reconnect delays.
var backoff = struct{ min, max, healthy time.Duration }{time.Second, 30 * time.Second, 30 * time.Second}

// reconnect runs connect until ctx ends. connect blocks while connected and
// returns when the connection ends; a connection that stayed up for a while
// resets the delay.
func reconnect(ctx context.Context, flow, kind string, connect func(context.Context) error) error {
	delay := backoff.min
	for ctx.Err() == nil {
		started := time.Now()
		err := connect(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) >= backoff.healthy {
			delay = backoff.min
		}
		observability.SourceEvent(flow, kind, "reconnect")
		observability.SourceFailure(flow, err)
		slog.Warn("flow source disconnected; reconnecting", "flow", flow, "source", kind, "error", err, "retryIn", delay)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(delay*2, backoff.max)
	}
	return nil
}
