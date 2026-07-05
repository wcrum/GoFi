package sink

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/wcrum/gofi/spec"
)

// applyOptions runs a slice of nats.Option against a fresh nats.Options
// struct and returns the populated struct, without ever dialing a server.
// nats.Option funcs (Nkey, UserInfo, Token, Secure, ...) only mutate fields
// on the struct they're given; only UserJWT-based options ("Creds" path)
// additionally invoke a "smoke test" callback, which for well-formed input
// does not require network access either.
func applyOptions(t *testing.T, opts []nats.Option) nats.Options {
	t.Helper()
	var o nats.Options
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			t.Fatalf("applying option: %v", err)
		}
	}
	return o
}

func TestNATSAuthOptionsNil(t *testing.T) {
	opts, err := natsAuthOptions(nil)
	if err != nil {
		t.Fatalf("natsAuthOptions(nil): %v", err)
	}
	if len(opts) != 0 {
		t.Errorf("expected no options for nil auth, got %d", len(opts))
	}
}

func TestNATSAuthOptionsUserPassword(t *testing.T) {
	opts, err := natsAuthOptions(&spec.NATSAuth{Username: "alice", Password: "secret"})
	if err != nil {
		t.Fatalf("natsAuthOptions: %v", err)
	}
	o := applyOptions(t, opts)
	if o.User != "alice" || o.Password != "secret" {
		t.Errorf("got user=%q password=%q, want alice/secret", o.User, o.Password)
	}
}

func TestNATSAuthOptionsToken(t *testing.T) {
	opts, err := natsAuthOptions(&spec.NATSAuth{Token: "tok-abc"})
	if err != nil {
		t.Fatalf("natsAuthOptions: %v", err)
	}
	o := applyOptions(t, opts)
	if o.Token != "tok-abc" {
		t.Errorf("got token=%q, want tok-abc", o.Token)
	}
}

func TestNATSAuthOptionsPrecedenceTokenOverUserPass(t *testing.T) {
	// Token set alongside username/password: token should win (more specific).
	opts, err := natsAuthOptions(&spec.NATSAuth{Token: "tok-abc", Username: "alice", Password: "secret"})
	if err != nil {
		t.Fatalf("natsAuthOptions: %v", err)
	}
	o := applyOptions(t, opts)
	if o.Token != "tok-abc" {
		t.Errorf("expected token to win, got token=%q", o.Token)
	}
	if o.User != "" || o.Password != "" {
		t.Errorf("expected user/password to be unset when token wins, got user=%q password=%q", o.User, o.Password)
	}
}

func TestNATSAuthOptionsNKeySeed(t *testing.T) {
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	wantPub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}

	opts, err := natsAuthOptions(&spec.NATSAuth{NKeySeed: string(seed)})
	if err != nil {
		t.Fatalf("natsAuthOptions: %v", err)
	}
	o := applyOptions(t, opts)
	if o.Nkey != wantPub {
		t.Errorf("got nkey pub=%q, want %q", o.Nkey, wantPub)
	}
	if o.SignatureCB == nil {
		t.Fatal("expected SignatureCB to be set")
	}
	// Verify the signature callback actually produces a valid signature
	// for the derived public key over an arbitrary nonce, matching what
	// the real NATS handshake would require.
	nonce := []byte("test-nonce-1234")
	sig, err := o.SignatureCB(nonce)
	if err != nil {
		t.Fatalf("SignatureCB: %v", err)
	}
	pub, err := nkeys.FromPublicKey(wantPub)
	if err != nil {
		t.Fatalf("FromPublicKey: %v", err)
	}
	if err := pub.Verify(nonce, sig); err != nil {
		t.Errorf("signature did not verify: %v", err)
	}
}

func TestNATSAuthOptionsPrecedenceNKeyOverToken(t *testing.T) {
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	seed, _ := kp.Seed()
	wantPub, _ := kp.PublicKey()

	opts, err := natsAuthOptions(&spec.NATSAuth{NKeySeed: string(seed), Token: "should-be-ignored"})
	if err != nil {
		t.Fatalf("natsAuthOptions: %v", err)
	}
	o := applyOptions(t, opts)
	if o.Nkey != wantPub {
		t.Errorf("expected nkey to win over token, got nkey=%q", o.Nkey)
	}
	if o.Token != "" {
		t.Errorf("expected token to be unset when nkey wins, got %q", o.Token)
	}
}

func TestNATSAuthOptionsMalformedNKeySeed(t *testing.T) {
	_, err := natsAuthOptions(&spec.NATSAuth{NKeySeed: "not-a-valid-seed"})
	if err == nil {
		t.Fatal("expected error for malformed nkey seed, got nil")
	}
}

func TestNATSAuthOptionsCredsSelectedOverEverythingElse(t *testing.T) {
	// A syntactically-permissive "creds" blob: nkeys.ParseDecoratedJWT falls
	// back to treating the whole content as the JWT if it finds no
	// "-----BEGIN...-----" decorated blocks, so this does not error at
	// option-application time (see TestNATSCredsMalformedContentIsLenient
	// below for the documented limitation this implies).
	opts, err := natsAuthOptions(&spec.NATSAuth{
		Creds:    "some-inline-creds-blob",
		NKeySeed: "SUALUCWSDBIZXQYZOPMVQLWOTSO72Y7MFM5VWVTTL2ENPO574UFEK5XJL4",
		Token:    "ignored-token",
		Username: "ignored-user",
	})
	if err != nil {
		t.Fatalf("natsAuthOptions: %v", err)
	}
	o := applyOptions(t, opts)
	if o.UserJWT == nil {
		t.Fatal("expected UserJWT callback to be set when Creds is provided")
	}
	if o.Nkey != "" || o.Token != "" || o.User != "" {
		t.Errorf("expected only the Creds-based option to be applied, got nkey=%q token=%q user=%q", o.Nkey, o.Token, o.User)
	}
}

// TestNATSCredsMalformedContentIsLenient documents a limitation we could not
// fully close: nats.go's UserCredentialBytes (via nkeys.ParseDecoratedJWT)
// does not eagerly validate that its input looks like a real JWT+seed
// creds file — a garbage string is accepted as "the JWT" at option-apply
// time, and only fails once a live server actually challenges the
// connection for a signature. We cannot exercise that failure path without
// a live NATS server, so it's left untested here.
func TestNATSCredsMalformedContentIsLenient(t *testing.T) {
	opts, err := natsAuthOptions(&spec.NATSAuth{Creds: "not a creds file at all"})
	if err != nil {
		t.Fatalf("natsAuthOptions: %v", err)
	}
	o := applyOptions(t, opts)
	if o.UserJWT == nil {
		t.Fatal("expected UserJWT to be set")
	}
	jwt, err := o.UserJWT()
	if err != nil {
		t.Fatalf("did not expect UserJWT() to fail for garbage input: %v", err)
	}
	if jwt != "not a creds file at all" {
		t.Errorf("expected ParseDecoratedJWT to fall back to raw content, got %q", jwt)
	}
}

// generateTestCertKeyPair creates a throwaway self-signed certificate and
// its PEM-encoded key, purely for exercising the PEM-parsing code paths.
func generateTestCertKeyPair(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"gofi-test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	certBuf := &bytes.Buffer{}
	if err := pem.Encode(certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("pem.Encode cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	keyBuf := &bytes.Buffer{}
	if err := pem.Encode(keyBuf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatalf("pem.Encode key: %v", err)
	}
	return certBuf.String(), keyBuf.String()
}

func TestNATSAuthOptionsTLSMalformedCA(t *testing.T) {
	_, err := natsAuthOptions(&spec.NATSAuth{TLSCA: "not a real PEM"})
	if err == nil {
		t.Fatal("expected error for malformed CA PEM, got nil")
	}
}

func TestNATSAuthOptionsTLSMalformedCertKey(t *testing.T) {
	_, err := natsAuthOptions(&spec.NATSAuth{TLSCert: "not a cert", TLSKey: "not a key"})
	if err == nil {
		t.Fatal("expected error for malformed client cert/key, got nil")
	}
}

func TestNATSAuthOptionsInsecureSkipVerify(t *testing.T) {
	opts, err := natsAuthOptions(&spec.NATSAuth{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("natsAuthOptions: %v", err)
	}
	o := applyOptions(t, opts)
	if o.TLSConfig == nil {
		t.Fatal("expected TLSConfig to be set")
	}
	if !o.TLSConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify to be true")
	}
}

func TestNATSAuthOptionsValidCAPEM(t *testing.T) {
	// Generate a real self-signed cert/key pair and CA at test time so the
	// test doesn't depend on a hardcoded (and easily malformed) PEM blob.
	certPEM, keyPEM := generateTestCertKeyPair(t)

	opts, err := natsAuthOptions(&spec.NATSAuth{TLSCA: certPEM, TLSCert: certPEM, TLSKey: keyPEM})
	if err != nil {
		t.Fatalf("natsAuthOptions: %v", err)
	}
	o := applyOptions(t, opts)
	if o.TLSConfig == nil {
		t.Fatal("expected TLSConfig to be set")
	}
	if o.TLSConfig.RootCAs == nil {
		t.Error("expected RootCAs to be populated")
	}
	if len(o.TLSConfig.Certificates) != 1 {
		t.Errorf("expected 1 client certificate, got %d", len(o.TLSConfig.Certificates))
	}
}

func TestNoAuthNoTLSProducesNoOptions(t *testing.T) {
	opts, err := natsAuthOptions(&spec.NATSAuth{})
	if err != nil {
		t.Fatalf("natsAuthOptions: %v", err)
	}
	if len(opts) != 0 {
		t.Errorf("expected no options for empty NATSAuth, got %d", len(opts))
	}
}

func TestNewNATSDefaultsURLWithoutAuth(t *testing.T) {
	// Sanity: without any NATSAuth configured, newNATS should behave exactly
	// as before (this will fail to connect since there's no live server at
	// the default URL in this test environment, but the error must be a
	// connection error, not an auth-construction error).
	_, err := newNATS(spec.SinkCfg{URL: "nats://127.0.0.1:4222"}, "test-listener")
	if err == nil {
		t.Skip("a NATS server happens to be running locally; skipping negative-path assertion")
	}
	if !strings.Contains(err.Error(), "nats connect") {
		t.Errorf("expected a connect error, got: %v", err)
	}
}
