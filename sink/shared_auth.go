package sink

import (
	"net/http"

	"github.com/nats-io/nats.go"
	"github.com/wcrum/gofi/spec"
)

// NATSOptions returns connection options for NATS auth/TLS settings; flow
// sources subscribing to NATS share them with the NATS sink.
func NATSOptions(auth *spec.NATSAuth) ([]nats.Option, error) { return natsAuthOptions(auth) }

// ApplyHTTPAuth sets the request's authentication from an HTTPAuth (the HTTP
// sink's shape), for flow sources that make HTTP or WebSocket requests.
func ApplyHTTPAuth(header http.Header, auth *spec.HTTPAuth) {
	if auth == nil {
		return
	}
	switch auth.Type {
	case "bearer":
		header.Set("Authorization", "Bearer "+auth.Token)
	case "basic":
		req := http.Request{Header: header}
		req.SetBasicAuth(auth.Username, auth.Password)
	case "apiKey":
		name := auth.HeaderName
		if name == "" {
			name = "X-API-Key"
		}
		header.Set(name, auth.APIKey)
	}
}
