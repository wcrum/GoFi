package spec

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBrokerSubscribeSourcesValidate(t *testing.T) {
	valid := []SubscribeSource{
		{Kafka: &KafkaSubscribe{Brokers: []string{"kafka:9092", "10.0.0.2:9093"}, Topics: []string{"orders"}, StartOffset: "earliest", TLS: &SourceTLS{Enabled: true}, SASL: &KafkaSASL{Mechanism: "SCRAM-SHA-512", Username: "u", Password: "p"}}},
		{AMQP: &AMQPSubscribe{URL: "amqp://rabbit:5672/", Queue: "orders"}},
		{AMQP: &AMQPSubscribe{URL: "amqps://rabbit.example", Exchange: "events", RoutingKey: "sensor.#", Prefetch: 10, Username: "u", Password: "p"}},
		{JetStream: &JetStreamSubscribe{URL: "nats://nats:4222", Stream: "ORDERS", Consumer: "gofi", Subject: "orders.>", DeliverPolicy: "new", AckWait: "1m"}},
		{Redis: &RedisSubscribe{URL: "redis://redis:6379/0", Stream: "events", Start: "earliest", Field: "data"}},
		{Redis: &RedisSubscribe{URL: "rediss://redis.example", Channels: []string{"a.*"}, Pattern: true}},
	}
	for i, s := range valid {
		if err := (FlowSource{Subscribe: &s}).Validate(); err != nil {
			t.Errorf("valid[%d]: %v", i, err)
		}
	}
	invalid := []struct {
		source SubscribeSource
		want   string
	}{
		{SubscribeSource{Kafka: &KafkaSubscribe{Topics: []string{"t"}}}, "kafka.brokers needs at least one"},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"kafka"}, Topics: []string{"t"}}}, "must be host:port"},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"kafka:0"}, Topics: []string{"t"}}}, "port must be"},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"k:9092"}}}, "kafka.topics needs at least one"},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"k:9092"}, Topics: []string{"a b"}}}, "not a valid topic"},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"k:9092"}, Topics: []string{"t"}, StartOffset: "oldest"}}, "startOffset must be latest, earliest"},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"k:9092"}, Topics: []string{"t"}, SASL: &KafkaSASL{Mechanism: "GSSAPI"}}}, "sasl.mechanism"},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"k:9092"}, Topics: []string{"t"}, SASL: &KafkaSASL{Mechanism: "PLAIN"}}}, "username and password"},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"k:9092"}, Topics: []string{"t"}, TLS: &SourceTLS{CA: "x"}}}, "tls.ca is not a valid PEM"},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"k:9092"}, Topics: []string{"t"}, TLS: &SourceTLS{Cert: "x"}}}, "both cert and key"},
		{SubscribeSource{AMQP: &AMQPSubscribe{URL: "http://rabbit", Queue: "q"}}, "amqp.url must use amqp, amqps"},
		{SubscribeSource{AMQP: &AMQPSubscribe{URL: "amqp://rabbit"}}, "needs a queue, or an exchange"},
		{SubscribeSource{AMQP: &AMQPSubscribe{URL: "amqp://rabbit", Queue: "q", Exchange: "e"}}, "not both"},
		{SubscribeSource{AMQP: &AMQPSubscribe{URL: "amqp://rabbit", Queue: "q", RoutingKey: "k"}}, "routingKey applies only"},
		{SubscribeSource{AMQP: &AMQPSubscribe{URL: "amqp://rabbit", Queue: "q", Prefetch: -1}}, "prefetch"},
		{SubscribeSource{AMQP: &AMQPSubscribe{URL: "amqp://rabbit", Queue: "q", Username: "u"}}, "both username and password"},
		{SubscribeSource{JetStream: &JetStreamSubscribe{URL: "nats://n"}}, "jetstream.stream is required"},
		{SubscribeSource{JetStream: &JetStreamSubscribe{URL: "nats://n", Stream: "S", Consumer: "a.b"}}, "consumer cannot contain"},
		{SubscribeSource{JetStream: &JetStreamSubscribe{URL: "nats://n", Stream: "S", DeliverPolicy: "first"}}, "deliverPolicy must be all, last, new"},
		{SubscribeSource{JetStream: &JetStreamSubscribe{URL: "nats://n", Stream: "S", AckWait: "10ms"}}, "ackWait"},
		{SubscribeSource{Redis: &RedisSubscribe{URL: "redis://r"}}, "needs a stream"},
		{SubscribeSource{Redis: &RedisSubscribe{URL: "redis://r", Stream: "s", Channels: []string{"c"}}}, "not both"},
		{SubscribeSource{Redis: &RedisSubscribe{URL: "redis://r", Stream: "s", Pattern: true}}, "pattern applies only"},
		{SubscribeSource{Redis: &RedisSubscribe{URL: "redis://r", Channels: []string{"c"}, Group: "g"}}, "apply only to a stream"},
		{SubscribeSource{Redis: &RedisSubscribe{URL: "redis://r", Channels: []string{" "}}}, "channels[0] is empty"},
		{SubscribeSource{Redis: &RedisSubscribe{URL: "tcp://r", Stream: "s"}}, "redis.url must use redis, rediss"},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"k:1"}, Topics: []string{"t"}}, Redis: &RedisSubscribe{URL: "redis://r", Stream: "s"}}, "exactly one"},
	}
	for _, tc := range invalid {
		err := (FlowSource{Subscribe: &tc.source}).Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: got %v, want %q", tc.source, err, tc.want)
		}
	}
}

func TestBrokerEndpointsCarryPortsAndDropCredentials(t *testing.T) {
	for _, tc := range []struct {
		source SubscribeSource
		want   []string
	}{
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"a:9092", " b:9093"}}}, []string{"kafka://a:9092", "kafka://b:9093"}},
		{SubscribeSource{Kafka: &KafkaSubscribe{Brokers: []string{"a:9093"}, TLS: &SourceTLS{Enabled: true}}}, []string{"tls://a:9093"}},
		{SubscribeSource{AMQP: &AMQPSubscribe{URL: "amqps://u:p@mq.example/vhost"}}, []string{"amqps://mq.example:5671"}},
		{SubscribeSource{AMQP: &AMQPSubscribe{URL: "amqp://mq"}}, []string{"amqp://mq:5672"}},
		{SubscribeSource{JetStream: &JetStreamSubscribe{URL: "nats://nats"}}, []string{"nats://nats:4222"}},
		{SubscribeSource{Redis: &RedisSubscribe{URL: "rediss://:pw@redis.example:6380/1"}}, []string{"rediss://redis.example:6380"}},
		{SubscribeSource{NATS: &NATSSubscribe{URL: "nats://n"}}, nil},
	} {
		if got := tc.source.Endpoints(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("got %v, want %v", got, tc.want)
		}
	}
}

func TestBrokerSourceJSONMatchesProtoNames(t *testing.T) {
	var source FlowSource
	raw := `{"subscribe":{"kafka":{"brokers":["k:9092"],"topics":["t"],"startOffset":"earliest","clientId":"c","tls":{"enabled":true,"insecureSkipVerify":true},"sasl":{"mechanism":"PLAIN","username":"u","password":"p"}}}}`
	if err := json.Unmarshal([]byte(raw), &source); err != nil {
		t.Fatal(err)
	}
	k := source.Subscribe.Kafka
	if k.StartOffset != "earliest" || k.ClientID != "c" || !k.TLS.InsecureSkipVerify || k.SASL.Username != "u" {
		t.Fatalf("%+v", k)
	}
	raw = `{"subscribe":{"jetstream":{"url":"nats://n","stream":"S","deliverPolicy":"new","ackWait":"5s"}}}`
	if err := json.Unmarshal([]byte(raw), &source); err != nil || source.Subscribe.JetStream.DeliverPolicy != "new" {
		t.Fatalf("%v %+v", err, source.Subscribe)
	}
}
