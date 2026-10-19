// Package quictls builds crypto/tls configurations for GoFi's QUIC listen
// source and QUIC sink from the inline PEM material of a spec.TLSConfig.
// Pure Go, no cgo. QUIC requires TLS 1.3, so both sides pin that minimum.
package quictls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/wcrum/gofi/spec"
)

// selfSignedValidity is how long an ephemeral listener certificate is valid.
// Nothing verifies it against a trust store (peers pin or skip verification),
// so the bound only matters to clients that still check expiry.
const selfSignedValidity = 365 * 24 * time.Hour

// Server returns the tls.Config of a QUIC listen source for flow. With no
// certificate configured it generates an ephemeral self-signed certificate
// and logs its SHA-256 fingerprint once. cfg may be nil.
func Server(cfg *spec.TLSConfig, flow string) (*tls.Config, error) {
	if err := cfg.Validate("source.listen.tls", spec.TLSServer); err != nil {
		return nil, err
	}
	out := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: cfg.ALPNProtocols(),
	}
	if cfg.HasKeyPair() {
		pair, err := tls.X509KeyPair([]byte(cfg.Cert), []byte(cfg.Key))
		if err != nil {
			return nil, fmt.Errorf("source.listen.tls: %w", err)
		}
		out.Certificates = []tls.Certificate{pair}
	} else {
		pair, err := SelfSigned(flow)
		if err != nil {
			return nil, fmt.Errorf("source.listen.tls: generate certificate: %w", err)
		}
		out.Certificates = []tls.Certificate{pair}
		slog.Info("QUIC listener using an ephemeral self-signed certificate; peers must pin it or skip verification",
			"flow", flow, "sha256", Fingerprint(pair.Leaf), "notAfter", pair.Leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	if cfg != nil && strings.TrimSpace(cfg.CA) != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(cfg.CA)) {
			return nil, errors.New("source.listen.tls.ca must be a PEM certificate bundle")
		}
		out.ClientCAs = pool
		// A CA on a source authenticates senders: every peer must present a
		// certificate chaining to it. (Verifying only when a certificate
		// happens to be offered would let anyone in by offering none.)
		out.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return out, nil
}

// Client returns the tls.Config of a QUIC sink dialing host (the host part
// of the sink URL, used as the default ServerName). Servers are verified
// against cfg.CA when set, otherwise the system roots; InsecureSkipVerify
// is honoured only when explicitly configured, and logged. cfg may be nil.
func Client(cfg *spec.TLSConfig, host string) (*tls.Config, error) {
	if err := cfg.Validate("quic sink: tls", spec.TLSClient); err != nil {
		return nil, err
	}
	out := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: cfg.ALPNProtocols(),
		ServerName: host,
	}
	if cfg == nil {
		return out, nil
	}
	if cfg.ServerName != "" {
		out.ServerName = cfg.ServerName
	}
	if cfg.HasKeyPair() {
		pair, err := tls.X509KeyPair([]byte(cfg.Cert), []byte(cfg.Key))
		if err != nil {
			return nil, fmt.Errorf("quic sink: tls: %w", err)
		}
		out.Certificates = []tls.Certificate{pair}
	}
	if ca := strings.TrimSpace(cfg.CA); ca != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(ca)) {
			return nil, errors.New("quic sink: tls.ca must be a PEM certificate bundle")
		}
		out.RootCAs = pool
	}
	if cfg.InsecureSkipVerify {
		out.InsecureSkipVerify = true
		slog.Warn("QUIC sink skips server certificate verification (tls.insecureSkipVerify); the destination is not authenticated", "host", host)
	}
	return out, nil
}

// SelfSigned generates an ephemeral ECDSA P-256 certificate for a listener
// named name. Its SANs cover "gofi", "localhost", the loopback addresses,
// and name when that is a valid DNS label. Leaf is populated.
func SelfSigned(name string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "gofi-" + name, Organization: []string{"GoFi"}},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(selfSignedValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"gofi", "localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	if isDNSLabel(name) {
		template.DNSNames = append(template.DNSNames, name)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

// Fingerprint returns the SHA-256 fingerprint of cert's DER encoding as
// colon-separated hex, the form browsers and openssl print.
func Fingerprint(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	hexed := hex.EncodeToString(sum[:])
	parts := make([]string, 0, len(hexed)/2)
	for i := 0; i+2 <= len(hexed); i += 2 {
		parts = append(parts, strings.ToUpper(hexed[i:i+2]))
	}
	return strings.Join(parts, ":")
}

func isDNSLabel(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}
