package quictls

import (
	"crypto/tls"
	"regexp"
	"strings"
	"testing"

	"github.com/wcrum/gofi/spec"
)

func TestServerDefaultsToSelfSignedTLS13(t *testing.T) {
	conf, err := Server(nil, "telemetry")
	if err != nil {
		t.Fatal(err)
	}
	if conf.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %x", conf.MinVersion)
	}
	if len(conf.NextProtos) != 1 || conf.NextProtos[0] != spec.DefaultQUICALPN {
		t.Fatalf("NextProtos = %v", conf.NextProtos)
	}
	if conf.ClientAuth != tls.NoClientCert || conf.ClientCAs != nil {
		t.Fatal("client authentication configured without a CA")
	}
	if len(conf.Certificates) != 1 || conf.Certificates[0].Leaf == nil {
		t.Fatal("no self-signed certificate")
	}
	leaf := conf.Certificates[0].Leaf
	if leaf.Subject.CommonName != "gofi-telemetry" || !contains(leaf.DNSNames, "telemetry") || !contains(leaf.DNSNames, "localhost") || len(leaf.DNSNames) != 3 {
		t.Fatalf("leaf subject %q SANs %v", leaf.Subject.CommonName, leaf.DNSNames)
	}
	if !regexp.MustCompile(`^([0-9A-F]{2}:){31}[0-9A-F]{2}$`).MatchString(Fingerprint(leaf)) {
		t.Fatalf("fingerprint %q", Fingerprint(leaf))
	}
	if Fingerprint(nil) != "" {
		t.Fatal("nil fingerprint")
	}
}

func TestServerUsesConfiguredMaterial(t *testing.T) {
	ca, err := NewCA("test-ca")
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := ca.Issue("gofi", []string{"gofi.example", "127.0.0.1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	conf, err := Server(&spec.TLSConfig{Cert: cert, Key: key, CA: ca.CertPEM(), ALPN: []string{"custom/1"}}, "flow")
	if err != nil {
		t.Fatal(err)
	}
	// A CA on a source always requires client certificates.
	if conf.ClientAuth != tls.RequireAndVerifyClientCert || conf.ClientCAs == nil {
		t.Fatalf("ClientAuth = %v", conf.ClientAuth)
	}
	if conf.NextProtos[0] != "custom/1" {
		t.Fatalf("NextProtos = %v", conf.NextProtos)
	}
	if len(conf.Certificates) != 1 || len(conf.Certificates[0].Certificate) != 1 {
		t.Fatal("configured certificate missing")
	}
	conf, err = Server(&spec.TLSConfig{Cert: cert, Key: key, CA: ca.CertPEM(), RequireClientCert: true}, "flow")
	if err != nil {
		t.Fatal(err)
	}
	if conf.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v", conf.ClientAuth)
	}
	if _, err := Server(&spec.TLSConfig{RequireClientCert: true}, "flow"); err == nil {
		t.Fatal("requireClientCert without ca accepted")
	}
	if _, err := Server(&spec.TLSConfig{Cert: cert}, "flow"); err == nil {
		t.Fatal("cert without key accepted")
	}
}

func TestClientVerifiesByDefault(t *testing.T) {
	conf, err := Client(nil, "dest.example")
	if err != nil {
		t.Fatal(err)
	}
	if conf.InsecureSkipVerify || conf.RootCAs != nil || conf.ServerName != "dest.example" || conf.MinVersion != tls.VersionTLS13 {
		t.Fatalf("default client config %+v", conf)
	}
	ca, _ := NewCA("ca")
	cert, key, _ := ca.Issue("client", nil, true)
	conf, err = Client(&spec.TLSConfig{CA: ca.CertPEM(), Cert: cert, Key: key, ServerName: "override"}, "dest.example")
	if err != nil {
		t.Fatal(err)
	}
	if conf.RootCAs == nil || len(conf.Certificates) != 1 || conf.ServerName != "override" {
		t.Fatalf("client config with CA %+v", conf)
	}
	if _, err := Client(&spec.TLSConfig{CA: ca.CertPEM(), InsecureSkipVerify: true}, "h"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("ca with insecureSkipVerify: %v", err)
	}
	conf, err = Client(&spec.TLSConfig{InsecureSkipVerify: true}, "h")
	if err != nil || !conf.InsecureSkipVerify {
		t.Fatalf("explicit insecureSkipVerify: %v %+v", err, conf)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
