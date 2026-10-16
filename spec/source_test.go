package spec

import "testing"

func TestSourceEndpointValidation(t *testing.T) {
	for _, s := range []Source{
		{Port: -1}, {Port: 65536}, {Port: 9000, Network: "sctp"},
		{Port: 9000, Service: &ServiceExposure{Type: "LoadBalancer"}},
		{Port: 9000, Service: &ServiceExposure{Type: "ClusterIP", NodePort: 31000}},
		{Port: 9000, Service: &ServiceExposure{Type: "NodePort", Port: 65536}},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("accepted invalid source %+v", s)
		}
	}
	for _, s := range []Source{{}, {Port: 9000, Network: "udp", Service: &ServiceExposure{Type: "NodePort"}}, {Port: 9000, Service: &ServiceExposure{Type: "ClusterIP", Port: 8080}}} {
		if err := s.Validate(); err != nil {
			t.Errorf("rejected source %+v: %v", s, err)
		}
	}
}
