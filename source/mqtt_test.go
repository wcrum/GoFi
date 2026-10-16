package source

import (
	"context"
	"os"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/wcrum/gofi/spec"
)

// Needs an MQTT broker: GOFI_TEST_MQTT_URL=tcp://127.0.0.1:1883 go test ./source
func TestMQTTSubscribe(t *testing.T) {
	url := os.Getenv("GOFI_TEST_MQTT_URL")
	if url == "" {
		t.Skip("set GOFI_TEST_MQTT_URL to run against an MQTT broker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	go Run(ctx, "m", spec.FlowSource{Subscribe: &spec.SubscribeSource{MQTT: &spec.MQTTSubscribe{URL: url, Topic: "gofi/test/+", QoS: 1}}}, c.emit)
	pub := mqtt.NewClient(mqtt.NewClientOptions().AddBroker(url).SetClientID("gofi-test-pub"))
	if token := pub.Connect(); token.Wait() && token.Error() != nil {
		t.Fatal(token.Error())
	}
	defer pub.Disconnect(100)
	deadline := time.Now().Add(5 * time.Second)
	for len(c.got) == 0 && time.Now().Before(deadline) {
		pub.Publish("gofi/test/a", 1, false, []byte("reading")).Wait()
		time.Sleep(100 * time.Millisecond)
	}
	if frames := c.wait(t, 1); frames[0] != "reading" {
		t.Fatalf("frames %q", frames)
	}
}
