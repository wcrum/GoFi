package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// FlowSpecEnv carries a runtime's flow definition as JSON.
const FlowSpecEnv = "FLOW_SPEC"

// FlowSource says how data arrives in a flow. Exactly one source is set
// (docs/flows.md; Python sources are planned).
type FlowSource struct {
	Listen    *ListenSource    `json:"listen,omitempty"`
	Fetch     *FetchSource     `json:"fetch,omitempty"`
	Subscribe *SubscribeSource `json:"subscribe,omitempty"`
}

// FetchSource pulls on an interval. Each response body is one frame, or one
// frame per element of the JSON array Split selects.
type FetchSource struct {
	HTTP  *HTTPFetch `json:"http,omitempty"`
	Split string     `json:"split,omitempty"` // "$.items[*]", "$[*]"
}

// HTTPFetch requests a URL every interval; polls never overlap.
type HTTPFetch struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"` // default GET
	Headers map[string]string `json:"headers,omitempty"`
	Auth    *HTTPAuth         `json:"auth,omitempty"`    // same shape as the HTTP sink
	Every   string            `json:"every,omitempty"`   // Go duration; default 30s
	Timeout string            `json:"timeout,omitempty"` // Go duration; default 10s
	Body    string            `json:"body,omitempty"`
}

// SubscribeSource attaches to a stream; exactly one field is set. Each
// received message or event becomes one frame.
type SubscribeSource struct {
	NATS      *NATSSubscribe   `json:"nats,omitempty"`
	MQTT      *MQTTSubscribe   `json:"mqtt,omitempty"`
	WebSocket *StreamSubscribe `json:"websocket,omitempty"`
	SSE       *StreamSubscribe `json:"sse,omitempty"`
}

// NATSSubscribe is a core NATS subscription (no JetStream cursor).
type NATSSubscribe struct {
	URL     string    `json:"url"`
	Subject string    `json:"subject"`
	Queue   string    `json:"queue,omitempty"`
	Auth    *NATSAuth `json:"auth,omitempty"` // same options as the NATS sink
}

// MQTTSubscribe subscribes to an MQTT topic (QoS 0 or 1).
type MQTTSubscribe struct {
	URL      string `json:"url"` // tcp://, ssl://, ws://, or wss://
	Topic    string `json:"topic"`
	QoS      int    `json:"qos,omitempty"`
	ClientID string `json:"clientId,omitempty"` // default gofi-<flow>
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// StreamSubscribe is a WebSocket or Server-Sent Events stream.
type StreamSubscribe struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Auth    *HTTPAuth         `json:"auth,omitempty"`
	Send    string            `json:"send,omitempty"` // WebSocket only: sent after connecting
}

// flatSourceFields maps each field of the removed flat Listener source to
// its place under source.listen.
var flatSourceFields = map[string]string{"port": "port", "network": "network", "service": "service", "python": "hook"}

// UnmarshalJSON accepts only the current shape ({"listen": {...}}). The flat
// Listener shape is rejected with an explicit error rather than an unknown
// field message, so old manifests fail clearly.
func (s *FlowSource) UnmarshalJSON(data []byte) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	for flat, current := range flatSourceFields {
		if _, ok := keys[flat]; ok {
			return fmt.Errorf("source.%s: the flat listener source is not supported; use source.listen.%s (docs/flows.md)", flat, current)
		}
	}
	type current FlowSource // drops this method, avoiding recursion
	var decoded current
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	*s = FlowSource(decoded)
	return nil
}

// Listening returns the listen source, or its zero value when the flow has
// none.
func (s FlowSource) Listening() ListenSource {
	if s.Listen == nil {
		return ListenSource{}
	}
	return *s.Listen
}

// Kind names the configured source: "listen", "fetch", "subscribe", or "".
func (s FlowSource) Kind() string {
	switch {
	case s.Listen != nil:
		return "listen"
	case s.Fetch != nil:
		return "fetch"
	case s.Subscribe != nil:
		return "subscribe"
	}
	return ""
}

// Validate requires exactly one configured source.
func (s FlowSource) Validate() error {
	set := 0
	for _, present := range []bool{s.Listen != nil, s.Fetch != nil, s.Subscribe != nil} {
		if present {
			set++
		}
	}
	if set != 1 {
		return fmt.Errorf("source must set exactly one of listen, fetch, or subscribe")
	}
	switch {
	case s.Listen != nil:
		return s.Listen.Validate()
	case s.Fetch != nil:
		return s.Fetch.Validate()
	default:
		return s.Subscribe.Validate()
	}
}

// Validate checks a fetch source.
func (f *FetchSource) Validate() error {
	if f.HTTP == nil {
		return fmt.Errorf("source.fetch.http is required")
	}
	h := f.HTTP
	if err := pulledURL("source.fetch.http.url", h.URL, "http", "https"); err != nil {
		return err
	}
	switch strings.ToUpper(h.Method) {
	case "", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return fmt.Errorf("source.fetch.http.method must be GET, POST, PUT, PATCH, DELETE, or HEAD")
	}
	every, err := durationOr(h.Every, DefaultFetchEvery)
	if err != nil || every < time.Second {
		return fmt.Errorf("source.fetch.http.every must be a duration of at least 1s")
	}
	timeout, err := durationOr(h.Timeout, DefaultFetchTimeout)
	if err != nil || timeout <= 0 || timeout > 5*time.Minute {
		return fmt.Errorf("source.fetch.http.timeout must be a positive duration up to 5m")
	}
	if _, err := ParseSplit(f.Split); err != nil {
		return err
	}
	return nil
}

// Validate checks a subscribe source.
func (s *SubscribeSource) Validate() error {
	set := 0
	for _, present := range []bool{s.NATS != nil, s.MQTT != nil, s.WebSocket != nil, s.SSE != nil} {
		if present {
			set++
		}
	}
	if set != 1 {
		return fmt.Errorf("source.subscribe must set exactly one of nats, mqtt, websocket, or sse")
	}
	switch {
	case s.NATS != nil:
		if err := pulledURL("source.subscribe.nats.url", s.NATS.URL, "nats", "tls"); err != nil {
			return err
		}
		if strings.TrimSpace(s.NATS.Subject) == "" {
			return fmt.Errorf("source.subscribe.nats.subject is required")
		}
	case s.MQTT != nil:
		if err := pulledURL("source.subscribe.mqtt.url", s.MQTT.URL, "tcp", "ssl", "tls", "mqtt", "mqtts", "ws", "wss"); err != nil {
			return err
		}
		if strings.TrimSpace(s.MQTT.Topic) == "" {
			return fmt.Errorf("source.subscribe.mqtt.topic is required")
		}
		if s.MQTT.QoS < 0 || s.MQTT.QoS > 1 {
			return fmt.Errorf("source.subscribe.mqtt.qos must be 0 or 1")
		}
	case s.WebSocket != nil:
		return pulledURL("source.subscribe.websocket.url", s.WebSocket.URL, "ws", "wss")
	default:
		return pulledURL("source.subscribe.sse.url", s.SSE.URL, "http", "https")
	}
	return nil
}

// Defaults for fetch sources.
const (
	DefaultFetchEvery   = 30 * time.Second
	DefaultFetchTimeout = 10 * time.Second
)

// FetchTimings returns a fetch source's interval and request timeout.
func (h *HTTPFetch) FetchTimings() (every, timeout time.Duration) {
	every, _ = durationOr(h.Every, DefaultFetchEvery)
	timeout, _ = durationOr(h.Timeout, DefaultFetchTimeout)
	return every, timeout
}

func durationOr(value string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	return time.ParseDuration(value)
}

func pulledURL(field, raw string, schemes ...string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s must be an absolute URL", field)
	}
	for _, scheme := range schemes {
		if strings.EqualFold(u.Scheme, scheme) {
			return nil
		}
	}
	return fmt.Errorf("%s must use %s", field, strings.Join(schemes, ", "))
}

// ParseSplit parses a split path: "" (no split), "$[*]", or "$.a.b[*]".
// It returns the object keys to descend before the array.
func ParseSplit(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "$") || !strings.HasSuffix(path, "[*]") {
		return nil, fmt.Errorf(`source.fetch.split must look like "$[*]" or "$.items[*]"`)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(path, "$"), "[*]")
	if inner == "" {
		return []string{}, nil
	}
	if !strings.HasPrefix(inner, ".") {
		return nil, fmt.Errorf(`source.fetch.split must look like "$[*]" or "$.items[*]"`)
	}
	keys := strings.Split(strings.TrimPrefix(inner, "."), ".")
	for _, key := range keys {
		if key == "" || strings.ContainsAny(key, "[]*$") {
			return nil, fmt.Errorf(`source.fetch.split must look like "$[*]" or "$.items[*]"`)
		}
	}
	return keys, nil
}
