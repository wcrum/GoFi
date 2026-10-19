package spec

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
)

// NetworkQUIC is the ListenSource.Network and SinkCfg.Type value for QUIC.
// QUIC runs over UDP on the configured port; every QUIC stream is one byte
// stream through the pipeline, exactly like one TCP connection.
const NetworkQUIC = "quic"

// DefaultQUICALPN is the application protocol GoFi's QUIC endpoints
// negotiate when TLSConfig.ALPN is empty.
const DefaultQUICALPN = "gofi/1"

// TLSConfig is the TLS material of a QUIC listen source or QUIC sink. QUIC
// always runs over TLS 1.3; a source without Cert and Key generates an
// ephemeral self-signed certificate at startup and logs its SHA-256
// fingerprint once.
//
// Every field is INLINE CONTENT (PEM text), never a filesystem path: a
// FlowDefinition travels as one self-contained JSON document (FLOW_SPEC) to
// whichever process runs it, which may not share the controller's
// filesystem (see NATSAuth).
type TLSConfig struct {
	Cert string `json:"cert,omitempty"` // inline PEM certificate (chain)
	Key  string `json:"key,omitempty"`  // inline PEM private key; set together with Cert
	// CA is an inline PEM bundle. A sink verifies the server against it (in
	// place of the system roots). On a source it authenticates senders:
	// every peer must present a client certificate chaining to it (there is
	// no "verify only if offered" mode, which would admit anyone offering
	// none). Without CA a source accepts any peer.
	CA string `json:"ca,omitempty"`
	// InsecureSkipVerify disables server certificate verification on a sink.
	// Off by default; it cannot be combined with CA and is not accepted on a
	// source, where it has no meaning.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
	// ServerName is the SNI and verification name a sink presents; it
	// defaults to the host of the sink URL. Not accepted on a source.
	ServerName string `json:"serverName,omitempty"`
	// ALPN lists the application protocols to negotiate; empty means
	// [DefaultQUICALPN].
	ALPN []string `json:"alpn,omitempty"`
	// RequireClientCert states explicitly that a source requires client
	// certificates. Setting CA already does that; this flag documents the
	// intent and is rejected without CA. Source only.
	RequireClientCert bool `json:"requireClientCert,omitempty"`
}

// ALPNProtocols returns the ALPN list to negotiate, defaulting to
// [DefaultQUICALPN]. It is nil-safe.
func (t *TLSConfig) ALPNProtocols() []string {
	if t == nil || len(t.ALPN) == 0 {
		return []string{DefaultQUICALPN}
	}
	return append([]string(nil), t.ALPN...)
}

// HasKeyPair reports whether the configuration carries a certificate and key.
func (t *TLSConfig) HasKeyPair() bool {
	return t != nil && strings.TrimSpace(t.Cert) != "" && strings.TrimSpace(t.Key) != ""
}

// TLSRole says which side of a QUIC connection a TLSConfig configures.
type TLSRole int

const (
	// TLSServer is a QUIC listen source.
	TLSServer TLSRole = iota
	// TLSClient is a QUIC sink.
	TLSClient
)

// Validate checks t for role without touching the network or the
// filesystem. field prefixes error messages ("source.listen.tls", "sink tls").
// A nil TLSConfig is valid: a source then uses an ephemeral self-signed
// certificate and a sink the system roots.
func (t *TLSConfig) Validate(field string, role TLSRole) error {
	if t == nil {
		return nil
	}
	cert, key := strings.TrimSpace(t.Cert), strings.TrimSpace(t.Key)
	if (cert == "") != (key == "") {
		return fmt.Errorf("%s: cert and key must be set together", field)
	}
	if cert != "" {
		if _, err := tls.X509KeyPair([]byte(cert), []byte(key)); err != nil {
			return fmt.Errorf("%s: cert and key must be a matching PEM certificate and private key: %w", field, err)
		}
	}
	if ca := strings.TrimSpace(t.CA); ca != "" {
		if !x509.NewCertPool().AppendCertsFromPEM([]byte(ca)) {
			return fmt.Errorf("%s.ca must be a PEM certificate bundle", field)
		}
	}
	for _, proto := range t.ALPN {
		if proto == "" || len(proto) > 255 || strings.ContainsAny(proto, " \t\r\n,") {
			return fmt.Errorf("%s.alpn entries must be non-empty protocol names without whitespace", field)
		}
	}
	switch role {
	case TLSServer:
		if t.InsecureSkipVerify {
			return fmt.Errorf("%s.insecureSkipVerify applies to QUIC sinks only", field)
		}
		if t.ServerName != "" {
			return fmt.Errorf("%s.serverName applies to QUIC sinks only", field)
		}
		if t.RequireClientCert && strings.TrimSpace(t.CA) == "" {
			return fmt.Errorf("%s.requireClientCert requires ca, the bundle client certificates must chain to", field)
		}
	case TLSClient:
		if t.RequireClientCert {
			return fmt.Errorf("%s.requireClientCert applies to QUIC listen sources only", field)
		}
		if t.InsecureSkipVerify && strings.TrimSpace(t.CA) != "" {
			return fmt.Errorf("%s: choose ca or insecureSkipVerify, not both (insecureSkipVerify would disable the ca check)", field)
		}
		if strings.ContainsAny(t.ServerName, " \t\r\n/:") {
			return fmt.Errorf("%s.serverName must be a host name", field)
		}
	}
	return nil
}

// NetworkOrDefault returns the listen network, "tcp" when unset.
func (s ListenSource) NetworkOrDefault() string {
	if s.Network == "" {
		return "tcp"
	}
	return s.Network
}

// Protocol returns the layer-4 protocol a listen source binds: "tcp", or
// "udp" for udp and quic. Kubernetes Services, Gateway routes, and network
// policies are expressed in this protocol.
func (s ListenSource) Protocol() string {
	switch s.NetworkOrDefault() {
	case "udp", NetworkQUIC:
		return "udp"
	default:
		return "tcp"
	}
}

// validateQUIC checks the QUIC-specific parts of a listen source. The
// network and port range were checked by Validate.
func (s ListenSource) validateQUIC() error {
	if s.Network != NetworkQUIC {
		if s.TLS != nil {
			return fmt.Errorf("source.listen.tls requires network quic")
		}
		return nil
	}
	if s.Port == 0 {
		return fmt.Errorf("source.listen.port is required for network quic")
	}
	return s.TLS.Validate("source.listen.tls", TLSServer)
}
