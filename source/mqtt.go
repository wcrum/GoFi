package source

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// runMQTT subscribes to a topic. Subscribing in OnConnect restores the
// subscription after the client's automatic reconnects; a failed first
// connection returns so reconnect retries with backoff.
func runMQTT(ctx context.Context, flow string, cfg *spec.MQTTSubscribe, emit Emit) error {
	clientID := cfg.ClientID
	if clientID == "" {
		clientID = defaultMQTTClientID(flow)
	}
	topic := cfg.SubscribeTopic()
	handler := func(_ mqtt.Client, msg mqtt.Message) {
		observability.SourceEvent(flow, "mqtt", "ok")
		emit(ctx, msg.Payload(), "mqtt:"+msg.Topic())
	}
	subscribed := make(chan error, 1)
	opts := mqtt.NewClientOptions().AddBroker(cfg.URL).SetClientID(clientID).SetUsername(cfg.Username).SetPassword(cfg.Password).
		SetAutoReconnect(true).SetMaxReconnectInterval(30 * time.Second).SetConnectTimeout(10 * time.Second).SetOrderMatters(true).
		SetOnConnectHandler(func(client mqtt.Client) {
			token := client.Subscribe(topic, byte(cfg.QoS), handler)
			token.Wait()
			select {
			case subscribed <- token.Error():
			default:
			}
		})
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	defer client.Disconnect(250)
	select {
	case err := <-subscribed:
		if err != nil {
			return fmt.Errorf("subscribe %q: %w", topic, err)
		}
	case <-ctx.Done():
		return nil
	}
	<-ctx.Done()
	return nil
}

// defaultMQTTClientID is gofi-<flow>-<hostname>. A broker disconnects the
// previous session when a client ID connects again, so two replicas sharing
// "gofi-<flow>" would keep kicking each other off; the hostname (the Pod name
// in Kubernetes) makes each replica's ID distinct. MQTT 3.1 brokers may cap
// IDs at 23 bytes; configure clientId explicitly for those.
func defaultMQTTClientID(flow string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "gofi-" + flow
	}
	return "gofi-" + flow + "-" + strings.ToLower(host)
}
