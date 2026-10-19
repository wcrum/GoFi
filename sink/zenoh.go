package sink

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// zenohSink publishes through the Zenoh router's REST plugin. It intentionally
// shares HTTP auth/body handling while fixing the operation to PUT (or
// DELETE, which removes the key) on one concrete key.
type zenohSink struct{ *httpSink }

func newZenoh(cfg spec.SinkCfg) (*zenohSink, error) {
	normalized, err := zenohHTTPConfig(cfg)
	if err != nil {
		return nil, err
	}
	if err := Validate(normalized); err != nil {
		return nil, err
	}
	sink, err := newHTTP(normalized)
	if err != nil {
		return nil, err
	}
	sink.c = &http.Client{
		Timeout:   10 * time.Second,
		Transport: newTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("zenoh REST: router redirects are not supported")
		},
	}
	return &zenohSink{sink}, nil
}
func (s *zenohSink) Close() error { s.c.CloseIdleConnections(); return nil }

// Send publishes the message with PUT, or removes the key with DELETE (no
// body: the router ignores one).
func (s *zenohSink) Send(msg *message.Message) error {
	if s.method == http.MethodDelete {
		empty := *msg
		empty.Payload = []byte{}
		msg = &empty
	}
	return s.httpSink.Send(msg)
}

func zenohHTTPConfig(cfg spec.SinkCfg) (spec.SinkCfg, error) {
	if err := spec.ValidateZenohOrigin(cfg.URL); err != nil {
		return cfg, fmt.Errorf("zenoh REST: URL %w", err)
	}
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodPut
	}
	if method != http.MethodPut && method != http.MethodDelete {
		return cfg, fmt.Errorf("zenoh REST: method must be PUT or DELETE")
	}
	path, err := spec.ZenohKeyPath(cfg.Subject, false)
	if err != nil {
		return cfg, fmt.Errorf("zenoh REST: subject %w", err)
	}
	cfg.Type = "http"
	cfg.URL = strings.TrimRight(cfg.URL, "/") + "/" + path
	cfg.Subject = ""
	cfg.Method = method
	if cfg.BodyMode == "" {
		cfg.BodyMode = "raw"
	}
	if cfg.ContentType == "" {
		cfg.ContentType = "application/octet-stream"
	}
	return cfg, nil
}
