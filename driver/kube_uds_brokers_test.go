package driver

import (
	"testing"

	"github.com/wcrum/gofi/spec"
)

func TestUDSAllowsCoverBrokerSources(t *testing.T) {
	defs := []spec.FlowDefinition{
		{Name: "orders", Source: spec.FlowSource{Subscribe: &spec.SubscribeSource{Kafka: &spec.KafkaSubscribe{
			Brokers: []string{"b0.kafka.example.com:9093", "b1.kafka.example.com:9093"}, Topics: []string{"o"}, TLS: &spec.SourceTLS{Enabled: true}}}}},
		{Name: "jobs", Source: spec.FlowSource{Subscribe: &spec.SubscribeSource{AMQP: &spec.AMQPSubscribe{URL: "amqp://u:p@rabbit.mq.svc.cluster.local/", Queue: "jobs"}}}},
		{Name: "cache", Source: spec.FlowSource{Subscribe: &spec.SubscribeSource{Redis: &spec.RedisSubscribe{URL: "rediss://redis.example.com", Stream: "s"}}}},
		{Name: "js", Source: spec.FlowSource{Subscribe: &spec.SubscribeSource{JetStream: &spec.JetStreamSubscribe{URL: "nats://10.0.0.9", Stream: "S"}}}},
	}
	allows := allowsByDescription(defs)
	for _, host := range []string{"b0.kafka.example.com", "b1.kafka.example.com"} {
		if a := allows["GoFi flow orders source to "+host]; a["remoteHost"] != host || a["remoteProtocol"] != "TLS" || a["port"] != int64(9093) {
			t.Errorf("kafka broker %s: %v", host, a)
		}
	}
	if a := allows["GoFi flow jobs source to rabbit.mq.svc.cluster.local"]; a["remoteNamespace"] != "mq" || a["port"] != int64(5672) {
		t.Errorf("amqp: %v", a)
	}
	if a := allows["GoFi flow cache source to redis.example.com"]; a["remoteHost"] != "redis.example.com" || a["remoteProtocol"] != "TLS" || a["port"] != int64(6379) {
		t.Errorf("redis: %v", a)
	}
	if a := allows["GoFi flow js source to 10.0.0.9"]; a["remoteCidr"] != "10.0.0.9/32" || a["port"] != int64(4222) {
		t.Errorf("jetstream: %v", a)
	}
	if got := sourceURL(defs[1].Source); got != "amqp://rabbit.mq.svc.cluster.local:5672" {
		t.Errorf("connectivity url %q", got)
	}
}
