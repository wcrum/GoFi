package spec

import "testing"

func TestNodePortRangeParsing(t *testing.T) {
	for value, want := range map[string][2]int{"": {30000, 32767}, "1-65535": {1, 65535}, " 20000 - 40000 ": {20000, 40000}, "9-3": {30000, 32767}, "x": {30000, 32767}} {
		if min, max := nodePortRange(value); min != want[0] || max != want[1] {
			t.Errorf("%q: got %d-%d", value, min, max)
		}
	}
}

func TestSourceEndpointValidation(t *testing.T) {
	for _, s := range []ListenSource{
		{Port: -1}, {Port: 65536}, {Port: 9000, Network: "sctp"},
		{Port: 9000, Service: &ServiceExposure{Type: "ExternalName"}},
		{Port: 9000, Service: &ServiceExposure{Type: "ClusterIP", NodePort: 31000}},
		{Port: 9000, Service: &ServiceExposure{Type: "NodePort", Port: 65536}},
		{Port: 9000, Service: &ServiceExposure{Type: "NodePort", NodePort: 56769}},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("accepted invalid source %+v", s)
		}
	}
	for _, s := range []ListenSource{{}, {Port: 9000, Network: "udp", Service: &ServiceExposure{Type: "NodePort"}}, {Port: 9000, Service: &ServiceExposure{Type: "ClusterIP", Port: 8080}}, {Port: 9000, Service: &ServiceExposure{Type: "LoadBalancer", NodePort: 31000}}} {
		if err := s.Validate(); err != nil {
			t.Errorf("rejected source %+v: %v", s, err)
		}
	}
}

func TestPulledSourceValidation(t *testing.T) {
	valid := []FlowSource{
		{Fetch: &FetchSource{HTTP: &HTTPFetch{URL: "https://api.example/items", Every: "10s"}, Split: "$.items[*]"}},
		{Fetch: &FetchSource{HTTP: &HTTPFetch{URL: "http://x", Method: "post"}, Split: "$[*]"}},
		{Subscribe: &SubscribeSource{NATS: &NATSSubscribe{URL: "nats://nats:4222", Subject: "sensors.>"}}},
		{Subscribe: &SubscribeSource{MQTT: &MQTTSubscribe{URL: "tcp://broker:1883", Topic: "devices/+/t", QoS: 1}}},
		{Subscribe: &SubscribeSource{WebSocket: &StreamSubscribe{URL: "wss://feed/ws"}}},
		{Subscribe: &SubscribeSource{SSE: &StreamSubscribe{URL: "https://feed/events"}}},
	}
	for _, s := range valid {
		if err := s.Validate(); err != nil {
			t.Errorf("rejected %+v: %v", s, err)
		}
	}
	invalid := []FlowSource{
		{},
		{Listen: &ListenSource{Port: 9000}, Fetch: &FetchSource{HTTP: &HTTPFetch{URL: "http://x"}}},
		{Fetch: &FetchSource{}},
		{Fetch: &FetchSource{HTTP: &HTTPFetch{URL: "ftp://x"}}},
		{Fetch: &FetchSource{HTTP: &HTTPFetch{URL: "http://x", Every: "100ms"}}},
		{Fetch: &FetchSource{HTTP: &HTTPFetch{URL: "http://x", Timeout: "nope"}}},
		{Fetch: &FetchSource{HTTP: &HTTPFetch{URL: "http://x"}, Split: "items[*]"}},
		{Fetch: &FetchSource{HTTP: &HTTPFetch{URL: "http://x"}, Split: "$.a..b[*]"}},
		{Subscribe: &SubscribeSource{}},
		{Subscribe: &SubscribeSource{NATS: &NATSSubscribe{URL: "nats://n"}}},
		{Subscribe: &SubscribeSource{MQTT: &MQTTSubscribe{URL: "tcp://b", Topic: "t", QoS: 2}}},
		{Subscribe: &SubscribeSource{WebSocket: &StreamSubscribe{URL: "https://x"}}},
		{Subscribe: &SubscribeSource{SSE: &StreamSubscribe{URL: "https://x"}, WebSocket: &StreamSubscribe{URL: "ws://x"}}},
	}
	for _, s := range invalid {
		if err := s.Validate(); err == nil {
			t.Errorf("accepted %+v", s)
		}
	}
}
