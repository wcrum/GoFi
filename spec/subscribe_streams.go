package spec

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Streaming broker sources under subscribe: Kafka, AMQP 0-9-1, NATS
// JetStream, and Redis. Each acknowledges a message only after its pipeline
// run returns (docs/flows.md).

// SourceTLS configures TLS for a broker connection. Kafka needs Enabled;
// AMQP and Redis turn TLS on with their amqps:// and rediss:// schemes and
// use the remaining fields.
type SourceTLS struct {
	Enabled            bool   `json:"enabled,omitempty"`
	CA                 string `json:"ca,omitempty"`   // inline PEM CA bundle
	Cert               string `json:"cert,omitempty"` // inline PEM client certificate
	Key                string `json:"key,omitempty"`  // inline PEM client key
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
}

// KafkaSubscribe joins a consumer group on one or more topics.
type KafkaSubscribe struct {
	Brokers     []string   `json:"brokers"` // host:port seed brokers
	Topics      []string   `json:"topics"`
	Group       string     `json:"group,omitempty"`       // default gofi-<flow>
	StartOffset string     `json:"startOffset,omitempty"` // latest (default) | earliest
	TLS         *SourceTLS `json:"tls,omitempty"`
	SASL        *KafkaSASL `json:"sasl,omitempty"`
	ClientID    string     `json:"clientId,omitempty"` // default gofi-<flow>
}

// KafkaSASL authenticates to Kafka.
type KafkaSASL struct {
	Mechanism string `json:"mechanism"` // PLAIN | SCRAM-SHA-256 | SCRAM-SHA-512
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// AMQPSubscribe consumes an existing queue, or binds a server-named,
// exclusive, auto-delete queue to Exchange with RoutingKey.
type AMQPSubscribe struct {
	URL        string     `json:"url"` // amqp:// or amqps://
	Queue      string     `json:"queue,omitempty"`
	Exchange   string     `json:"exchange,omitempty"`
	RoutingKey string     `json:"routingKey,omitempty"`
	Prefetch   int        `json:"prefetch,omitempty"` // default 100
	Username   string     `json:"username,omitempty"` // overrides URL userinfo
	Password   string     `json:"password,omitempty"`
	TLS        *SourceTLS `json:"tls,omitempty"`
}

// JetStreamSubscribe consumes a stream through a durable pull consumer.
type JetStreamSubscribe struct {
	URL           string    `json:"url"`
	Stream        string    `json:"stream"`
	Consumer      string    `json:"consumer,omitempty"`      // durable name; default gofi-<flow>
	Subject       string    `json:"subject,omitempty"`       // optional filter subject
	DeliverPolicy string    `json:"deliverPolicy,omitempty"` // all (default) | last | new | last_per_subject
	AckWait       string    `json:"ackWait,omitempty"`       // Go duration; default 30s
	Auth          *NATSAuth `json:"auth,omitempty"`          // same options as the NATS sink
}

// RedisSubscribe reads a stream with a consumer group, or subscribes to
// Pub/Sub channels. Exactly one of Stream or Channels is set.
type RedisSubscribe struct {
	URL      string     `json:"url"` // redis:// or rediss://
	Stream   string     `json:"stream,omitempty"`
	Group    string     `json:"group,omitempty"`    // default gofi-<flow>
	Consumer string     `json:"consumer,omitempty"` // default the runtime hostname
	Start    string     `json:"start,omitempty"`    // latest (default) | earliest
	Field    string     `json:"field,omitempty"`    // emit only this entry field
	Channels []string   `json:"channels,omitempty"`
	Pattern  bool       `json:"pattern,omitempty"` // PSUBSCRIBE glob patterns
	Username string     `json:"username,omitempty"`
	Password string     `json:"password,omitempty"`
	TLS      *SourceTLS `json:"tls,omitempty"`
}

// Defaults for broker sources.
const (
	DefaultAMQPPrefetch     = 100
	DefaultJetStreamAckWait = 30 * time.Second
	kafkaField              = "source.subscribe.kafka"
	amqpField               = "source.subscribe.amqp"
	jetStreamField          = "source.subscribe.jetstream"
	redisField              = "source.subscribe.redis"
	maxAMQPPrefetch         = 65535
	maxJetStreamAckWait     = time.Hour
	minJetStreamAckWait     = time.Second
	defaultRedisPort        = "6379"
	defaultAMQPPort         = "5672"
	defaultAMQPSPort        = "5671"
)

// Validate checks a Kafka source.
func (k *KafkaSubscribe) Validate() error {
	if len(k.Brokers) == 0 {
		return fmt.Errorf("%s.brokers needs at least one host:port", kafkaField)
	}
	for i, broker := range k.Brokers {
		if err := hostPort(strings.TrimSpace(broker)); err != nil {
			return fmt.Errorf("%s.brokers[%d] %q: %v", kafkaField, i, broker, err)
		}
	}
	if len(k.Topics) == 0 {
		return fmt.Errorf("%s.topics needs at least one topic", kafkaField)
	}
	for i, topic := range k.Topics {
		if strings.TrimSpace(topic) == "" || strings.ContainsAny(topic, " /\\,") || len(topic) > 249 {
			return fmt.Errorf("%s.topics[%d] %q is not a valid topic name", kafkaField, i, topic)
		}
	}
	if err := oneOf(kafkaField+".startOffset", k.StartOffset, "", "latest", "earliest"); err != nil {
		return err
	}
	if k.SASL != nil {
		switch strings.ToUpper(k.SASL.Mechanism) {
		case "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512":
		default:
			return fmt.Errorf("%s.sasl.mechanism must be PLAIN, SCRAM-SHA-256, or SCRAM-SHA-512", kafkaField)
		}
		if k.SASL.Username == "" || k.SASL.Password == "" {
			return fmt.Errorf("%s.sasl needs a username and password", kafkaField)
		}
	}
	return k.TLS.validate(kafkaField + ".tls")
}

// Validate checks an AMQP source.
func (a *AMQPSubscribe) Validate() error {
	if err := pulledURL(amqpField+".url", a.URL, "amqp", "amqps"); err != nil {
		return err
	}
	queue, exchange := strings.TrimSpace(a.Queue), strings.TrimSpace(a.Exchange)
	switch {
	case queue == "" && exchange == "":
		return fmt.Errorf("%s needs a queue, or an exchange to bind a temporary queue to", amqpField)
	case queue != "" && exchange != "":
		return fmt.Errorf("%s sets queue or exchange, not both (bind the queue to the exchange on the broker)", amqpField)
	case queue != "" && a.RoutingKey != "":
		return fmt.Errorf("%s.routingKey applies only with exchange", amqpField)
	}
	if a.Prefetch < 0 || a.Prefetch > maxAMQPPrefetch {
		return fmt.Errorf("%s.prefetch must be between 0 (default %d) and %d", amqpField, DefaultAMQPPrefetch, maxAMQPPrefetch)
	}
	if (a.Username == "") != (a.Password == "") {
		return fmt.Errorf("%s needs both username and password, or neither", amqpField)
	}
	return a.TLS.validate(amqpField + ".tls")
}

// Validate checks a JetStream source.
func (j *JetStreamSubscribe) Validate() error {
	if err := pulledURL(jetStreamField+".url", j.URL, "nats", "tls"); err != nil {
		return err
	}
	if name := strings.TrimSpace(j.Stream); name == "" || strings.ContainsAny(name, " .*>\t") {
		return fmt.Errorf("%s.stream is required and cannot contain spaces, '.', '*', or '>'", jetStreamField)
	}
	if strings.ContainsAny(j.Consumer, " .*>\t") {
		return fmt.Errorf("%s.consumer cannot contain spaces, '.', '*', or '>'", jetStreamField)
	}
	if err := oneOf(jetStreamField+".deliverPolicy", j.DeliverPolicy, "", "all", "last", "new", "last_per_subject"); err != nil {
		return err
	}
	if wait, err := durationOr(j.AckWait, DefaultJetStreamAckWait); err != nil || wait < minJetStreamAckWait || wait > maxJetStreamAckWait {
		return fmt.Errorf("%s.ackWait must be a duration from 1s to 1h", jetStreamField)
	}
	return nil
}

// AckWaitDuration returns the consumer's ack wait.
func (j *JetStreamSubscribe) AckWaitDuration() time.Duration {
	wait, _ := durationOr(j.AckWait, DefaultJetStreamAckWait)
	return wait
}

// Validate checks a Redis source.
func (r *RedisSubscribe) Validate() error {
	if err := pulledURL(redisField+".url", r.URL, "redis", "rediss"); err != nil {
		return err
	}
	stream := strings.TrimSpace(r.Stream)
	switch {
	case stream == "" && len(r.Channels) == 0:
		return fmt.Errorf("%s needs a stream (consumer group) or channels (Pub/Sub)", redisField)
	case stream != "" && len(r.Channels) > 0:
		return fmt.Errorf("%s sets stream or channels, not both", redisField)
	case stream != "":
		if err := oneOf(redisField+".start", r.Start, "", "latest", "earliest"); err != nil {
			return err
		}
		if r.Pattern {
			return fmt.Errorf("%s.pattern applies only to channels", redisField)
		}
	default:
		for i, channel := range r.Channels {
			if strings.TrimSpace(channel) == "" {
				return fmt.Errorf("%s.channels[%d] is empty", redisField, i)
			}
		}
		if r.Group != "" || r.Consumer != "" || r.Start != "" || r.Field != "" {
			return fmt.Errorf("%s.group, consumer, start, and field apply only to a stream", redisField)
		}
	}
	return r.TLS.validate(redisField + ".tls")
}

// Endpoints lists the URLs a broker subscribe source connects to, with
// explicit ports, for egress allowances and connectivity checks. Kafka
// brokers appear as kafka://host:port (tls:// with TLS on). Only the seed
// brokers are known: a Kafka cluster may advertise further broker hosts.
func (s *SubscribeSource) Endpoints() []string {
	switch {
	case s == nil:
		return nil
	case s.Kafka != nil:
		scheme := "kafka"
		if s.Kafka.TLS != nil && s.Kafka.TLS.Enabled {
			scheme = "tls"
		}
		out := make([]string, 0, len(s.Kafka.Brokers))
		for _, broker := range s.Kafka.Brokers {
			out = append(out, scheme+"://"+strings.TrimSpace(broker))
		}
		return out
	case s.AMQP != nil:
		return withPort(s.AMQP.URL, map[string]string{"amqp": defaultAMQPPort, "amqps": defaultAMQPSPort})
	case s.JetStream != nil:
		return withPort(s.JetStream.URL, map[string]string{"nats": "4222", "tls": "4222"})
	case s.Redis != nil:
		return withPort(s.Redis.URL, map[string]string{"redis": defaultRedisPort, "rediss": defaultRedisPort})
	}
	return nil
}

// withPort returns rawURL as scheme://host:port without credentials or path.
func withPort(rawURL string, ports map[string]string) []string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" {
		return nil
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		port = ports[scheme]
	}
	return []string{scheme + "://" + net.JoinHostPort(u.Hostname(), port)}
}

// Config builds a tls.Config; nil when t is nil and on is false.
func (t *SourceTLS) Config(on bool) (*tls.Config, error) {
	if t == nil {
		if !on {
			return nil, nil
		}
		t = &SourceTLS{}
	}
	if !on && !t.Enabled {
		return nil, nil
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: t.InsecureSkipVerify}
	if t.CA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(t.CA)) {
			return nil, fmt.Errorf("tls.ca is not a valid PEM certificate")
		}
		config.RootCAs = pool
	}
	if t.Cert != "" || t.Key != "" {
		cert, err := tls.X509KeyPair([]byte(t.Cert), []byte(t.Key))
		if err != nil {
			return nil, fmt.Errorf("tls.cert/key: %w", err)
		}
		config.Certificates = []tls.Certificate{cert}
	}
	return config, nil
}

func (t *SourceTLS) validate(field string) error {
	if t == nil {
		return nil
	}
	if t.CA != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(t.CA)) {
		return fmt.Errorf("%s.ca is not a valid PEM certificate", field)
	}
	if (t.Cert == "") != (t.Key == "") {
		return fmt.Errorf("%s needs both cert and key, or neither", field)
	}
	if t.Cert != "" {
		if _, err := tls.X509KeyPair([]byte(t.Cert), []byte(t.Key)); err != nil {
			return fmt.Errorf("%s.cert/key: %v", field, err)
		}
	}
	return nil
}

func hostPort(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return fmt.Errorf("must be host:port")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port must be 1-65535")
	}
	return nil
}

func oneOf(field, value string, allowed ...string) error {
	for _, option := range allowed {
		if value == option {
			return nil
		}
	}
	shown := make([]string, 0, len(allowed))
	for _, option := range allowed {
		if option != "" {
			shown = append(shown, option)
		}
	}
	return fmt.Errorf("%s must be %s", field, strings.Join(shown, ", "))
}
