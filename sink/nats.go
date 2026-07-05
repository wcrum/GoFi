package sink

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

type natsSink struct {
	nc      *nats.Conn
	subject string
}

// natsAuthOptions builds the []nats.Option slice implied by an *spec.NATSAuth
// (auth + TLS only — not the Name/MaxReconnects/ReconnectWait options). It is
// separated from newNATS so the selection logic can be unit-tested without a
// live NATS server.
//
// Precedence when multiple credential fields are set (most-specific wins):
// Creds > NKeySeed > Token > Username/Password.
func natsAuthOptions(auth *spec.NATSAuth) ([]nats.Option, error) {
	if auth == nil {
		return nil, nil
	}

	var opts []nats.Option

	switch {
	case auth.Creds != "":
		opts = append(opts, nats.UserCredentialBytes([]byte(auth.Creds)))
	case auth.NKeySeed != "":
		kp, err := nkeys.FromSeed([]byte(auth.NKeySeed))
		if err != nil {
			return nil, fmt.Errorf("nats sink: parse nkey seed: %w", err)
		}
		pub, err := kp.PublicKey()
		if err != nil {
			return nil, fmt.Errorf("nats sink: derive nkey public key: %w", err)
		}
		sigCB := func(nonce []byte) ([]byte, error) {
			return kp.Sign(nonce)
		}
		opts = append(opts, nats.Nkey(pub, sigCB))
	case auth.Token != "":
		opts = append(opts, nats.Token(auth.Token))
	case auth.Username != "" || auth.Password != "":
		opts = append(opts, nats.UserInfo(auth.Username, auth.Password))
	}

	if auth.TLSCA != "" || (auth.TLSCert != "" && auth.TLSKey != "") || auth.InsecureSkipVerify {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: auth.InsecureSkipVerify,
		}
		if auth.TLSCert != "" && auth.TLSKey != "" {
			cert, err := tls.X509KeyPair([]byte(auth.TLSCert), []byte(auth.TLSKey))
			if err != nil {
				return nil, fmt.Errorf("nats sink: parse client cert/key: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
		if auth.TLSCA != "" {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(auth.TLSCA)) {
				return nil, fmt.Errorf("nats sink: parse CA certificate: invalid PEM")
			}
			tlsConfig.RootCAs = pool
		}
		opts = append(opts, nats.Secure(tlsConfig))
	}

	return opts, nil
}

func newNATS(cfg spec.SinkCfg, listenerName string) (*natsSink, error) {
	url := cfg.URL
	if url == "" {
		url = nats.DefaultURL
	}

	opts := []nats.Option{
		nats.Name("gofi:" + listenerName),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
	}

	authOpts, err := natsAuthOptions(cfg.NATSAuth)
	if err != nil {
		return nil, err
	}
	opts = append(opts, authOpts...)

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats connect %s: %w", url, err)
	}
	subj := cfg.Subject
	if subj == "" {
		subj = "ingress." + listenerName
	}
	return &natsSink{nc: nc, subject: subj}, nil
}

// Send publishes the message as JSON containing both fields and meta.
func (s *natsSink) Send(msg *message.Message) error {
	payload := map[string]interface{}{
		"fields": msg.Fields,
		"meta":   msg.Meta,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("nats sink: marshal: %w", err)
	}
	return s.nc.Publish(s.subject, data)
}

func (s *natsSink) Close() error {
	s.nc.Drain()
	return nil
}
