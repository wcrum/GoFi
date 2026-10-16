package sink

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wcrum/gofi/spec"
)

// zenohSink publishes through the Zenoh router's REST plugin. It intentionally
// shares HTTP auth/body handling while fixing the operation to PUT on one key.
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
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("zenoh REST: router redirects are not supported")
		},
	}
	return &zenohSink{sink}, nil
}
func (s *zenohSink) Close() error { s.c.CloseIdleConnections(); return nil }

func zenohHTTPConfig(cfg spec.SinkCfg) (spec.SinkCfg, error) {
	origin, err := url.Parse(cfg.URL)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Hostname() == "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		return cfg, fmt.Errorf("zenoh REST: URL must be an absolute HTTP or HTTPS router origin")
	}
	if cfg.Method != "" && strings.ToUpper(strings.TrimSpace(cfg.Method)) != http.MethodPut {
		return cfg, fmt.Errorf("zenoh REST: method must be PUT")
	}
	key := cfg.Subject
	if key == "" || !utf8.ValidString(key) || strings.ContainsAny(key, "*$?#@\\%") {
		return cfg, fmt.Errorf("zenoh REST: subject must be a concrete key without wildcards, query syntax, or admin paths")
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return cfg, fmt.Errorf("zenoh REST: key contains a control character")
		}
	}
	segments := strings.Split(key, "/")
	escaped := make([]string, len(segments))
	for i, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return cfg, fmt.Errorf("zenoh REST: key segments must be nonempty and cannot be . or ..")
		}
		escaped[i] = url.PathEscape(segment)
	}
	cfg.Type = "http"
	cfg.URL = strings.TrimRight(cfg.URL, "/") + "/" + strings.Join(escaped, "/")
	cfg.Subject = ""
	cfg.Method = http.MethodPut
	if cfg.BodyMode == "" {
		cfg.BodyMode = "raw"
	}
	if cfg.ContentType == "" {
		cfg.ContentType = "application/octet-stream"
	}
	return cfg, nil
}
