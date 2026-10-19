package source

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// amqpConfig builds the connection config: TLS for amqps://, and explicit
// credentials, which replace any in the URL.
func amqpConfig(flow string, cfg *spec.AMQPSubscribe) (amqp.Config, error) {
	config := amqp.Config{
		Heartbeat:  10 * time.Second,
		Locale:     "en_US",
		Properties: amqp.Table{"connection_name": "gofi-" + flow},
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return config, fmt.Errorf("amqp url: %w", err)
	}
	tlsConfig, err := cfg.TLS.Config(strings.EqualFold(u.Scheme, "amqps"))
	if err != nil {
		return config, fmt.Errorf("amqp %w", err)
	}
	config.TLSClientConfig = tlsConfig
	if cfg.Username != "" {
		config.SASL = []amqp.Authentication{&amqp.PlainAuth{Username: cfg.Username, Password: cfg.Password}}
	}
	return config, nil
}

// runAMQP consumes with manual acks until ctx ends or the connection or
// channel closes. Each delivery is one emit and is acked after emit returns
// (the pipeline run, sinks included); deliveries unacked when the
// connection drops are redelivered by the broker: at-least-once into the
// pipeline. prefetch bounds deliveries in flight.
func runAMQP(ctx context.Context, flow string, cfg *spec.AMQPSubscribe, emit Emit) error {
	config, err := amqpConfig(flow, cfg)
	if err != nil {
		return err
	}
	conn, err := amqp.DialConfig(cfg.URL, config)
	if err != nil {
		return fmt.Errorf("connect %s: %w", redactURL(cfg.URL), err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	prefetch := cfg.Prefetch
	if prefetch == 0 {
		prefetch = spec.DefaultAMQPPrefetch
	}
	if err := ch.Qos(prefetch, 0, false); err != nil {
		return fmt.Errorf("qos: %w", err)
	}
	queue := cfg.Queue
	if queue == "" {
		// A server-named queue that exists only while this consumer does.
		declared, err := ch.QueueDeclare("", false, true, true, false, nil)
		if err != nil {
			return fmt.Errorf("declare queue: %w", err)
		}
		if err := ch.QueueBind(declared.Name, cfg.RoutingKey, cfg.Exchange, false, nil); err != nil {
			return fmt.Errorf("bind to exchange %q: %w", cfg.Exchange, err)
		}
		queue = declared.Name
	}
	deliveries, err := ch.ConsumeWithContext(ctx, queue, "gofi-"+flow, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume %q: %w", queue, err)
	}
	closed := conn.NotifyClose(make(chan *amqp.Error, 1))
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-closed:
			if err == nil {
				return fmt.Errorf("AMQP connection closed")
			}
			return err
		case d, ok := <-deliveries:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("AMQP consumer on %q cancelled", queue)
			}
			if len(d.Body) > MaxMessageBytes {
				observability.SourceEvent(flow, "amqp", "error")
				slog.Warn("amqp: delivery over the size cap rejected", "flow", flow, "queue", queue, "routingKey", d.RoutingKey)
				_ = d.Reject(false) // dead-lettered by the broker if the queue has a DLX
				continue
			}
			observability.SourceEvent(flow, "amqp", "ok")
			emit(message.WithSourceMeta(ctx, amqpMeta(d)), d.Body, amqpOrigin(cfg, queue, d))
			if ctx.Err() != nil {
				return nil // unacked: the broker redelivers it
			}
			if err := d.Ack(false); err != nil {
				return fmt.Errorf("ack: %w", err)
			}
		}
	}
}

func amqpOrigin(cfg *spec.AMQPSubscribe, queue string, d amqp.Delivery) string {
	if cfg.Queue != "" {
		return "amqp:" + queue
	}
	return "amqp:" + cfg.Exchange + "/" + d.RoutingKey
}

// amqpMeta is a delivery's metadata for meta["amqp.*"].
func amqpMeta(d amqp.Delivery) map[string]string {
	meta := map[string]string{
		"amqp.exchange":    d.Exchange,
		"amqp.routingKey":  d.RoutingKey,
		"amqp.redelivered": strconv.FormatBool(d.Redelivered),
	}
	for key, value := range map[string]string{"amqp.messageId": d.MessageId, "amqp.contentType": d.ContentType, "amqp.correlationId": d.CorrelationId, "amqp.type": d.Type, "amqp.appId": d.AppId} {
		if value != "" {
			meta[key] = value
		}
	}
	if !d.Timestamp.IsZero() {
		meta["amqp.timestamp"] = d.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	for key, value := range d.Headers {
		switch v := value.(type) {
		case string:
			meta["amqp.header."+key] = v
		case []byte:
			meta["amqp.header."+key] = string(v)
		default:
			meta["amqp.header."+key] = fmt.Sprint(v)
		}
	}
	return meta
}

// redactURL drops userinfo so errors and logs never carry credentials.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid URL)"
	}
	u.User = nil
	return u.Redacted()
}
