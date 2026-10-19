package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

const testKey = "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----\n"

func secretLadenFlow() FlowDefinition {
	return FlowDefinition{
		Name: "secrets",
		Source: FlowSource{Subscribe: &SubscribeSource{
			Kafka: &KafkaSubscribe{Brokers: []string{"k:9092"}, Topics: []string{"t"}, TLS: &SourceTLS{Enabled: true, CA: "ca-pem", Cert: "cert-pem", Key: testKey}, SASL: &KafkaSASL{Mechanism: "PLAIN", Username: "u", Password: "kafka-pw"}},
		}},
		Framing: Framing{Mode: "line"},
		Parser:  ParserCfg{Format: "json"},
		Sinks: []SinkCfg{
			{Type: "http", URL: "https://user:http-pw@receiver.example/ingest?x=1", Headers: map[string]string{"X-App": "GoFi", "Authorization": "Bearer abc", "x-api-key": "k"}, Auth: &HTTPAuth{Type: "basic", Username: "u", Password: "basic-pw"}},
			{Type: "nats", URL: "nats://broker:4222", NATSAuth: &NATSAuth{Username: "n", Password: "nats-pw", Token: "tok", NKeySeed: "SUACC", Creds: "creds-file", TLSCert: "cert", TLSKey: testKey, TLSCA: "ca"}},
			{Type: NetworkQUIC, URL: "quic://peer:7000", TLS: &TLSConfig{Cert: "cert", Key: testKey, CA: "ca", ServerName: "peer"}},
			{Type: "aster", URL: "http://127.0.0.1:8080", Aster: &AsterCfg{LogicalKey: "lk", Token: "aster-token"}},
		},
		DeadLetterSink: &SinkCfg{Type: "http", URL: "https://dl.example", Auth: &HTTPAuth{Type: "bearer", Token: "dl-token"}},
		Stages:         []StageSpec{{ID: "d", Kind: "dedup", KeyExpr: `fields["key"]`}},
	}
}

func TestRedactReplacesEveryCredentialAndNothingElse(t *testing.T) {
	def := secretLadenFlow()
	before, _ := json.Marshal(def)
	got := Redact(def)
	after, _ := json.Marshal(def)
	if string(before) != string(after) {
		t.Fatal("Redact mutated its input")
	}
	k := got.Source.Subscribe.Kafka
	if k.TLS.Key != Redacted || k.SASL.Password != Redacted {
		t.Fatalf("kafka: %+v %+v", k.TLS, k.SASL)
	}
	if k.TLS.CA != "ca-pem" || k.TLS.Cert != "cert-pem" || k.SASL.Username != "u" {
		t.Fatalf("kafka non-secrets changed: %+v %+v", k.TLS, k.SASL)
	}
	h := got.Sinks[0]
	if h.URL != "https://user:***@receiver.example/ingest?x=1" || h.Auth.Password != Redacted || h.Headers["Authorization"] != Redacted || h.Headers["x-api-key"] != Redacted || h.Headers["X-App"] != "GoFi" || h.Auth.Username != "u" {
		t.Fatalf("http sink: %+v", h)
	}
	n := got.Sinks[1].NATSAuth
	if n.Password != Redacted || n.Token != Redacted || n.NKeySeed != Redacted || n.Creds != Redacted || n.TLSKey != Redacted || n.TLSCert != "cert" || n.TLSCA != "ca" || n.Username != "n" {
		t.Fatalf("nats: %+v", n)
	}
	if q := got.Sinks[2].TLS; q.Key != Redacted || q.Cert != "cert" || q.CA != "ca" || q.ServerName != "peer" {
		t.Fatalf("quic: %+v", q)
	}
	if a := got.Sinks[3].Aster; a.Token != Redacted || a.LogicalKey != "lk" {
		t.Fatalf("aster: %+v", a)
	}
	if got.DeadLetterSink.Auth.Token != Redacted {
		t.Fatalf("dead letter: %+v", got.DeadLetterSink.Auth)
	}
	if got.Stages[0].KeyExpr != `fields["key"]` {
		t.Fatal("a dedup keyExpr is not a secret")
	}
	if !HasRedacted(got) || HasRedacted(def) {
		t.Fatal("HasRedacted")
	}
	// Nothing secret survives in the serialized form.
	data, _ := json.Marshal(got)
	for _, secret := range []string{"kafka-pw", "http-pw", "basic-pw", "Bearer abc", "nats-pw", "SUACC", "creds-file", "aster-token", "dl-token", "BEGIN PRIVATE KEY"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("%q leaked: %s", secret, data)
		}
	}
}

func TestRedactCoversFetchAndOtherSubscribeSources(t *testing.T) {
	cases := map[string]struct {
		def   FlowDefinition
		check func(FlowDefinition) bool
	}{
		"s3": {FlowDefinition{Source: FlowSource{Fetch: &FetchSource{S3: &S3Fetch{Bucket: "b", AccessKeyID: "AKIA", SecretAccessKey: "s", SessionToken: "st", TLSCA: "ca"}}}}, func(d FlowDefinition) bool {
			s := d.Source.Fetch.S3
			return s.SecretAccessKey == Redacted && s.SessionToken == Redacted && s.AccessKeyID == "AKIA" && s.TLSCA == "ca"
		}},
		"sql": {FlowDefinition{Source: FlowSource{Fetch: &FetchSource{SQL: &SQLFetch{Driver: "postgres", DSN: "postgres://u:p@h/db", Query: "select 1"}}}}, func(d FlowDefinition) bool {
			return d.Source.Fetch.SQL.DSN == Redacted && d.Source.Fetch.SQL.Query == "select 1"
		}},
		"sql2": {FlowDefinition{Source: FlowSource{Fetch: &FetchSource{SQL: &SQLFetch{Driver: "mysql", Host: "h", Username: "u", Password: "p", TLS: "require", Query: "select 1"}}}}, func(d FlowDefinition) bool {
			return d.Source.Fetch.SQL.Password == Redacted && d.Source.Fetch.SQL.TLS == "require"
		}},
		"http": {FlowDefinition{Source: FlowSource{Fetch: &FetchSource{HTTP: &HTTPFetch{URL: "https://api", Auth: &HTTPAuth{Type: "apiKey", APIKey: "k", HeaderName: "X-K"}}}}}, func(d FlowDefinition) bool {
			return d.Source.Fetch.HTTP.Auth.APIKey == Redacted && d.Source.Fetch.HTTP.Auth.HeaderName == "X-K"
		}},
		"mqtt": {FlowDefinition{Source: FlowSource{Subscribe: &SubscribeSource{MQTT: &MQTTSubscribe{URL: "tcp://b:1883", Topic: "t", Username: "u", Password: "p"}}}}, func(d FlowDefinition) bool {
			return d.Source.Subscribe.MQTT.Password == Redacted && d.Source.Subscribe.MQTT.Username == "u"
		}},
		"amqp": {FlowDefinition{Source: FlowSource{Subscribe: &SubscribeSource{AMQP: &AMQPSubscribe{URL: "amqp://guest:guest@mq:5672/v", Queue: "q", Password: "p", TLS: &SourceTLS{Cert: "c", Key: "k"}}}}}, func(d FlowDefinition) bool {
			a := d.Source.Subscribe.AMQP
			return a.URL == "amqp://guest:***@mq:5672/v" && a.Password == Redacted && a.TLS.Key == Redacted && a.TLS.Cert == "c"
		}},
		"redis": {FlowDefinition{Source: FlowSource{Subscribe: &SubscribeSource{Redis: &RedisSubscribe{URL: "redis://r:6379", Stream: "s", Password: "p"}}}}, func(d FlowDefinition) bool { return d.Source.Subscribe.Redis.Password == Redacted }},
		"nats":  {FlowDefinition{Source: FlowSource{Subscribe: &SubscribeSource{NATS: &NATSSubscribe{URL: "nats://n", Subject: "s", Auth: &NATSAuth{Creds: "c"}}}}}, func(d FlowDefinition) bool { return d.Source.Subscribe.NATS.Auth.Creds == Redacted }},
		"js":    {FlowDefinition{Source: FlowSource{Subscribe: &SubscribeSource{JetStream: &JetStreamSubscribe{URL: "nats://n", Stream: "s", Auth: &NATSAuth{Token: "t"}}}}}, func(d FlowDefinition) bool { return d.Source.Subscribe.JetStream.Auth.Token == Redacted }},
		"ws": {FlowDefinition{Source: FlowSource{Subscribe: &SubscribeSource{WebSocket: &StreamSubscribe{URL: "wss://w", Headers: map[string]string{"Cookie": "sid=1", "Accept": "x"}, Auth: &HTTPAuth{Type: "bearer", Token: "t"}}}}}, func(d FlowDefinition) bool {
			w := d.Source.Subscribe.WebSocket
			return w.Auth.Token == Redacted && w.Headers["Cookie"] == Redacted && w.Headers["Accept"] == "x"
		}},
		"zenoh": {FlowDefinition{Source: FlowSource{Subscribe: &SubscribeSource{Zenoh: &ZenohSubscribe{URL: "http://z:8000", KeyExpr: "demo/**", Auth: &HTTPAuth{Type: "basic", Username: "u", Password: "p"}}}}}, func(d FlowDefinition) bool {
			return d.Source.Subscribe.Zenoh.Auth.Password == Redacted && d.Source.Subscribe.Zenoh.KeyExpr == "demo/**"
		}},
		"quic-listen": {FlowDefinition{Source: FlowSource{Listen: &ListenSource{Port: 7000, Network: NetworkQUIC, TLS: &TLSConfig{Cert: "c", Key: "k", CA: "ca"}}}}, func(d FlowDefinition) bool {
			l := d.Source.Listen.TLS
			return l.Key == Redacted && l.Cert == "c" && l.CA == "ca"
		}},
	}
	for name, tc := range cases {
		if got := Redact(tc.def); !tc.check(got) {
			data, _ := json.Marshal(got)
			t.Errorf("%s: %s", name, data)
		}
	}
}

func TestRedactCoversPythonNodeConfig(t *testing.T) {
	// Per-node Python config keys are the function's keyword names: an LLM
	// key given as api_key is a credential, the model name and timeout are not.
	config := func() map[string]any {
		return map[string]any{"api_key": "sk-live-1", "apiKey": "sk-live-2", "model": "gpt-4o-mini", "timeout": 20.0}
	}
	def := FlowDefinition{
		Name:   "llm",
		Stages: []StageSpec{{ID: "enrich", Kind: "python", Python: &PythonRef{Library: "llm", Function: "llm_enrich", Config: config()}}},
		Sinks:  []SinkCfg{{Type: "python", Python: &PythonRef{Library: "llm", Function: "llm_agent", Config: config()}}},
	}
	got := Redact(def)
	for name, cfg := range map[string]map[string]any{"stage": got.Stages[0].Python.Config, "sink": got.Sinks[0].Python.Config} {
		if cfg["api_key"] != Redacted || cfg["apiKey"] != Redacted || cfg["model"] != "gpt-4o-mini" || cfg["timeout"] != 20.0 {
			t.Fatalf("%s config: %v", name, cfg)
		}
	}
	if def.Stages[0].Python.Config["api_key"] != "sk-live-1" {
		t.Fatal("Redact mutated its input")
	}
	// A redacted read saved back keeps the stored key and the client's other edits.
	got.Stages[0].Python.Config["model"] = "gpt-4.1-mini"
	merged, err := MergeRedacted(got, def)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := merged.Stages[0].Python.Config; cfg["api_key"] != "sk-live-1" || cfg["apiKey"] != "sk-live-2" || cfg["model"] != "gpt-4.1-mini" {
		t.Fatalf("merged stage config: %v", cfg)
	}
	if cfg := merged.Sinks[0].Python.Config; cfg["api_key"] != "sk-live-1" {
		t.Fatalf("merged sink config: %v", cfg)
	}
	if HasRedacted(merged) {
		t.Fatal("sentinel survived the merge")
	}
}

func TestMergeRedactedRestoresStoredSecretsOnly(t *testing.T) {
	stored := secretLadenFlow()
	shown := Redact(stored)
	// The client edits a non-secret, changes one secret, and sends the rest back as shown.
	edited := shown
	edited.Sinks[0].Headers["X-App"] = "Edited"
	edited.Sinks[1].NATSAuth.Password = "new-nats-pw"
	merged, err := MergeRedacted(edited, stored)
	if err != nil {
		t.Fatal(err)
	}
	want := stored
	want.Sinks[0].Headers["X-App"] = "Edited"
	want.Sinks[1].NATSAuth.Password = "new-nats-pw"
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(merged)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("merge:\n%s\nwant\n%s", gotJSON, wantJSON)
	}
	if HasRedacted(merged) {
		t.Fatal("sentinel survived the merge")
	}
}

func TestMergeRedactedRefusesASentinelWithNothingBehindIt(t *testing.T) {
	incoming := FlowDefinition{Name: "n", Sinks: []SinkCfg{{Type: "http", URL: "https://x", Auth: &HTTPAuth{Type: "bearer", Token: Redacted}}}}
	if _, err := MergeRedacted(incoming, FlowDefinition{Name: "n"}); err == nil || !strings.Contains(err.Error(), "sinks.0.auth.token") {
		t.Fatalf("expected an error naming the field, got %v", err)
	}
	// A URL password sentinel with no stored password is refused too.
	incoming = FlowDefinition{Name: "n", Sinks: []SinkCfg{{Type: "http", URL: "https://u:***@x"}}}
	if _, err := MergeRedacted(incoming, FlowDefinition{Name: "n", Sinks: []SinkCfg{{Type: "http", URL: "https://x"}}}); err == nil {
		t.Fatal("expected an error")
	}
	// A definition without sentinels passes through unchanged.
	plain := FlowDefinition{Name: "n", Sinks: []SinkCfg{{Type: "http", URL: "https://x", Auth: &HTTPAuth{Type: "bearer", Token: "fresh"}}}}
	merged, err := MergeRedacted(plain, FlowDefinition{})
	if err != nil || merged.Sinks[0].Auth.Token != "fresh" {
		t.Fatalf("plain: %v %+v", err, merged)
	}
}
