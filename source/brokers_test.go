//go:build !gofi_nokafka

package source

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// metaCollector records each emit's data, origin, and source metadata.
type metaCollector struct {
	mu      sync.Mutex
	frames  []string
	remotes []string
	metas   []map[string]string
	got     chan struct{}
}

func newMetaCollector() *metaCollector { return &metaCollector{got: make(chan struct{}, 100)} }
func (c *metaCollector) emit(ctx context.Context, data []byte, remote string) {
	c.mu.Lock()
	c.frames = append(c.frames, string(data))
	c.remotes = append(c.remotes, remote)
	c.metas = append(c.metas, message.SourceMeta(ctx))
	c.mu.Unlock()
	c.got <- struct{}{}
}
func (c *metaCollector) wait(t *testing.T, n int) ([]string, []string, []map[string]string) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-c.got:
		case <-time.After(10 * time.Second):
			t.Fatalf("got %d of %d frames: %q", i, n, c.frames)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.frames...), append([]string(nil), c.remotes...), append([]map[string]string(nil), c.metas...)
}

// runSource runs src until the returned stop is called and Run returns.
func runSource(t *testing.T, flow string, src spec.FlowSource, emit Emit) (stop func()) {
	t.Helper()
	if err := src.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); Run(ctx, flow, src, emit) }()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("source did not stop")
		}
	}
}

func TestKafkaConsumerGroupWithSCRAMCommitsAfterPipeline(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(2, "events"), kfake.EnableSASL(), kfake.Superuser("SCRAM-SHA-512", "gofi", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	auth := scram.Auth{User: "gofi", Pass: "secret"}.AsSha512Mechanism()
	producer, err := kgo.NewClient(kgo.SeedBrokers(cluster.ListenAddrs()...), kgo.SASL(auth), kgo.DefaultProduceTopic("events"))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	produce := func(value string) {
		t.Helper()
		record := &kgo.Record{Key: []byte("k-" + value), Value: []byte(value), Headers: []kgo.RecordHeader{{Key: "trace", Value: []byte("t1")}}}
		if err := producer.ProduceSync(context.Background(), record).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
	produce("one")
	produce("two")

	src := spec.FlowSource{Subscribe: &spec.SubscribeSource{Kafka: &spec.KafkaSubscribe{
		Brokers: cluster.ListenAddrs(), Topics: []string{"events"}, StartOffset: "earliest",
		SASL: &spec.KafkaSASL{Mechanism: "SCRAM-SHA-512", Username: "gofi", Password: "secret"},
	}}}
	c := newMetaCollector()
	stop := runSource(t, "orders", src, c.emit)
	frames, remotes, metas := c.wait(t, 2)
	stop()
	if !sameSet(frames, "one", "two") || remotes[0] != "kafka:events" {
		t.Fatalf("frames %q remotes %q", frames, remotes)
	}
	if m := metas[0]; m["kafka.topic"] != "events" || m["kafka.key"] != "k-"+frames[0] || m["kafka.header.trace"] != "t1" || m["kafka.offset"] == "" || m["kafka.partition"] == "" {
		t.Fatalf("meta %v", m)
	}

	// The same group resumes after the committed offsets: only new records.
	produce("three")
	again := newMetaCollector()
	stop = runSource(t, "orders", src, again.emit)
	defer stop()
	if frames, _, _ := again.wait(t, 1); frames[0] != "three" {
		t.Fatalf("after restart got %q, want only the new record", frames)
	}
	select {
	case <-again.got:
		t.Fatalf("redelivered: %q", again.frames)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestKafkaOptionsRejectBadTLS(t *testing.T) {
	if _, err := kafkaOptions("f", &spec.KafkaSubscribe{Brokers: []string{"b:9092"}, Topics: []string{"t"}, TLS: &spec.SourceTLS{Enabled: true, CA: "not pem"}}); err == nil {
		t.Fatal("expected a CA error")
	}
	if _, err := kafkaOptions("f", &spec.KafkaSubscribe{Brokers: []string{"b:9092"}, Topics: []string{"t"}, TLS: &spec.SourceTLS{Enabled: true}, SASL: &spec.KafkaSASL{Mechanism: "plain", Username: "u", Password: "p"}}); err != nil {
		t.Fatal(err)
	}
}

func sameSet(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			return false
		}
		seen[w]--
	}
	return true
}

func startJetStream(t *testing.T) string {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(server.Shutdown)
	return server.ClientURL()
}

func TestJetStreamDurableConsumerAcksAfterPipeline(t *testing.T) {
	url := startJetStream(t)
	conn, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	js, _ := jetstream.New(conn)
	ctx := context.Background()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "ORDERS", Subjects: []string{"orders.>"}}); err != nil {
		t.Fatal(err)
	}
	publish := func(subject, data string) {
		msg := nats.NewMsg(subject)
		msg.Data = []byte(data)
		msg.Header.Set("Trace", "t1")
		if _, err := js.PublishMsg(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	publish("orders.a", "one")
	publish("orders.b", "skipped by filter")
	publish("orders.a", "two")

	src := spec.FlowSource{Subscribe: &spec.SubscribeSource{JetStream: &spec.JetStreamSubscribe{URL: url, Stream: "ORDERS", Subject: "orders.a", AckWait: "2s"}}}
	c := newMetaCollector()
	stop := runSource(t, "orders", src, c.emit)
	frames, remotes, metas := c.wait(t, 2)
	stop()
	if frames[0] != "one" || frames[1] != "two" || remotes[0] != "jetstream:orders.a" {
		t.Fatalf("frames %q remotes %q", frames, remotes)
	}
	if m := metas[1]; m["jetstream.stream"] != "ORDERS" || m["jetstream.sequence"] != "3" || m["jetstream.consumer"] != "gofi-orders" || m["jetstream.header.Trace"] != "t1" {
		t.Fatalf("meta %v", m)
	}
	info, err := js.Consumer(ctx, "ORDERS", "gofi-orders")
	if err != nil {
		t.Fatal(err)
	}
	if state, err := info.Info(ctx); err != nil || state.NumAckPending != 0 || state.AckFloor.Stream != 3 {
		t.Fatalf("consumer state %+v %v", state, err)
	}

	publish("orders.a", "three")
	again := newMetaCollector()
	stop = runSource(t, "orders", src, again.emit)
	defer stop()
	if frames, _, _ := again.wait(t, 1); frames[0] != "three" {
		t.Fatalf("after restart got %q", frames)
	}
}

func TestRedisStreamConsumerGroupAcksAndRecoversPending(t *testing.T) {
	redisStream.block = 100 * time.Millisecond
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()
	// An entry delivered to this consumer before a "crash" and never acked.
	if err := client.XGroupCreateMkStream(ctx, "events", "gofi-sensors", "0").Err(); err != nil {
		t.Fatal(err)
	}
	client.XAdd(ctx, &redis.XAddArgs{Stream: "events", Values: map[string]any{"v": "pending"}})
	if err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "gofi-sensors", Consumer: "pod-a", Streams: []string{"events", ">"}, Count: 1, Block: -1}).Err(); err != nil {
		t.Fatal(err)
	}
	client.XAdd(ctx, &redis.XAddArgs{Stream: "events", Values: map[string]any{"v": "new"}})

	src := spec.FlowSource{Subscribe: &spec.SubscribeSource{Redis: &spec.RedisSubscribe{URL: "redis://" + server.Addr(), Stream: "events", Consumer: "pod-a", Field: "v"}}}
	c := newMetaCollector()
	stop := runSource(t, "sensors", src, c.emit)
	defer stop()
	frames, remotes, metas := c.wait(t, 2)
	if frames[0] != "pending" || frames[1] != "new" || remotes[0] != "redis:events" || metas[0]["redis.id"] == "" {
		t.Fatalf("frames %q remotes %q metas %v", frames, remotes, metas)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		pending, err := client.XPending(ctx, "events", "gofi-sensors").Result()
		if err == nil && pending.Count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("entries still pending: %+v %v", pending, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRedisEntryDataEncodesAllFieldsAsJSON(t *testing.T) {
	data, err := redisEntryData(redis.XMessage{ID: "1-0", Values: map[string]any{"a": "1", "b": "x"}}, "")
	if err != nil || string(data) != `{"a":"1","b":"x"}` {
		t.Fatalf("%s %v", data, err)
	}
	if _, err := redisEntryData(redis.XMessage{ID: "1-0", Values: map[string]any{"a": "1"}}, "missing"); err == nil {
		t.Fatal("expected a missing-field error")
	}
}

func TestRedisPubSubPatterns(t *testing.T) {
	server := miniredis.RunT(t)
	src := spec.FlowSource{Subscribe: &spec.SubscribeSource{Redis: &spec.RedisSubscribe{URL: "redis://" + server.Addr(), Channels: []string{"sensors.*"}, Pattern: true}}}
	c := newMetaCollector()
	stop := runSource(t, "s", src, c.emit)
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for len(c.got) == 0 && time.Now().Before(deadline) {
		server.Publish("sensors.a", "reading")
		time.Sleep(50 * time.Millisecond)
	}
	frames, remotes, metas := c.wait(t, 1)
	if frames[0] != "reading" || remotes[0] != "redis:sensors.a" || metas[0]["redis.pattern"] != "sensors.*" {
		t.Fatalf("frames %q remotes %q metas %v", frames, remotes, metas)
	}
}

func TestAMQPConfigAndMeta(t *testing.T) {
	config, err := amqpConfig("f", &spec.AMQPSubscribe{URL: "amqps://user:pw@mq.example/vhost", Queue: "q", Username: "gofi", Password: "s"})
	if err != nil || config.TLSClientConfig == nil || len(config.SASL) != 1 || config.Properties["connection_name"] != "gofi-f" {
		t.Fatalf("config %+v %v", config, err)
	}
	if config, _ := amqpConfig("f", &spec.AMQPSubscribe{URL: "amqp://mq:5672", Queue: "q"}); config.TLSClientConfig != nil || config.SASL != nil {
		t.Fatalf("plain amqp got TLS or SASL: %+v", config)
	}
	if got := redactURL("amqp://user:pw@mq:5672/v"); got != "amqp://mq:5672/v" {
		t.Fatalf("redacted %q", got)
	}
	d := amqp.Delivery{Exchange: "events", RoutingKey: "sensor.a", MessageId: "m1", Redelivered: true, Headers: amqp.Table{"trace": "t1", "n": int32(3)}}
	meta := amqpMeta(d)
	if meta["amqp.routingKey"] != "sensor.a" || meta["amqp.messageId"] != "m1" || meta["amqp.redelivered"] != "true" || meta["amqp.header.trace"] != "t1" || meta["amqp.header.n"] != "3" {
		t.Fatalf("meta %v", meta)
	}
	if got := amqpOrigin(&spec.AMQPSubscribe{Exchange: "events"}, "amq.gen-1", d); got != "amqp:events/sensor.a" {
		t.Fatalf("origin %q", got)
	}
}

// Needs RabbitMQ: GOFI_TEST_AMQP_URL=amqp://guest:guest@127.0.0.1:5672/ go test ./source
func TestAMQPQueueAcksAfterPipeline(t *testing.T) {
	url := os.Getenv("GOFI_TEST_AMQP_URL")
	if url == "" {
		t.Skip("set GOFI_TEST_AMQP_URL to run against an AMQP broker")
	}
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, _ := conn.Channel()
	q, err := ch.QueueDeclare("gofi-test", false, true, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch.Publish("", q.Name, false, false, amqp.Publishing{Body: []byte("hello")})
	c := newMetaCollector()
	stop := runSource(t, "a", spec.FlowSource{Subscribe: &spec.SubscribeSource{AMQP: &spec.AMQPSubscribe{URL: url, Queue: q.Name}}}, c.emit)
	defer stop()
	if frames, remotes, _ := c.wait(t, 1); frames[0] != "hello" || remotes[0] != "amqp:gofi-test" {
		t.Fatalf("frames %q remotes %q", frames, remotes)
	}
}
