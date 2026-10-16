package sink

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var lokiLabelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type lokiSink struct {
	endpoint string
	labels   map[string]string
	bodyMode string
	headers  map[string]string
	auth     *spec.HTTPAuth
	client   *http.Client
}
type lokiPush struct {
	Streams []lokiStream `json:"streams"`
}
type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"`
}

func ValidateLoki(cfg spec.SinkCfg) error {
	u, e := url.Parse(cfg.URL)
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return fmt.Errorf("loki sink: URL must be HTTP(S) with authentication configured separately")
	}
	if cfg.BodyMode != "" && cfg.BodyMode != "wrapped" && cfg.BodyMode != "raw" {
		return fmt.Errorf("loki sink: bodyMode must be wrapped or raw")
	}
	if len(cfg.Labels) > 30 {
		return fmt.Errorf("loki sink: at most 30 static labels are allowed")
	}
	for name, value := range cfg.Labels {
		if !lokiLabelName.MatchString(name) || strings.HasPrefix(name, "__") || len(name) > 128 || len(value) > 2048 {
			return fmt.Errorf("loki sink: invalid static label name or value")
		}
	}
	if cfg.Auth != nil {
		switch cfg.Auth.Type {
		case "bearer", "basic", "apiKey":
		default:
			return fmt.Errorf("loki sink: auth must use bearer, basic, or apiKey")
		}
	}
	// Reuse static HTTP credential/header checks without constructing a client
	// that makes network requests.
	if err := Validate(spec.SinkCfg{Type: "http", URL: cfg.URL, Method: http.MethodPost, Headers: cfg.Headers, Auth: cfg.Auth, BodyMode: cfg.BodyMode}); err != nil {
		return fmt.Errorf("loki sink: %w", err)
	}
	return nil
}
func newLoki(cfg spec.SinkCfg, listenerName string) (*lokiSink, error) {
	if e := ValidateLoki(cfg); e != nil {
		return nil, e
	}
	u, _ := url.Parse(cfg.URL)
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/loki/api/v1/push") {
		u.Path = strings.TrimRight(u.Path, "/") + "/loki/api/v1/push"
	} else {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	labels := map[string]string{"app": "gofi", "listener": listenerName}
	for k, v := range cfg.Labels {
		labels[k] = v
	}
	headers := map[string]string{}
	for k, v := range cfg.Headers {
		headers[k] = v
	}
	return &lokiSink{endpoint: u.String(), labels: labels, bodyMode: cfg.BodyMode, headers: headers, auth: cfg.Auth, client: &http.Client{Timeout: 10 * time.Second}}, nil
}
func (s *lokiSink) Send(msg *message.Message) error {
	var line string
	if s.bodyMode == "raw" {
		line = string(msg.Raw)
	} else {
		b, e := json.Marshal(map[string]any{"id": msg.ID, "fields": msg.Fields, "meta": msg.Meta})
		if e != nil {
			return fmt.Errorf("loki sink: encode log line: %w", e)
		}
		line = string(b)
	}
	if len(line) > 2<<20 {
		return fmt.Errorf("loki sink: log line exceeds 2 MiB")
	}
	timestamp := time.Now()
	if parsed, e := time.Parse(time.RFC3339Nano, msg.Meta["ts"]); e == nil {
		timestamp = parsed
	}
	payload, e := json.Marshal(lokiPush{Streams: []lokiStream{{Stream: s.labels, Values: [][2]string{{strconv.FormatInt(timestamp.UnixNano(), 10), line}}}}})
	if e != nil {
		return e
	}
	req, e := http.NewRequest(http.MethodPost, s.endpoint, bytes.NewReader(payload))
	if e != nil {
		return fmt.Errorf("loki sink: invalid request")
	}
	req.Header.Set("Content-Type", "application/json")
	if s.auth != nil {
		switch s.auth.Type {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+s.auth.Token)
		case "basic":
			req.SetBasicAuth(s.auth.Username, s.auth.Password)
		case "apiKey":
			name := s.auth.HeaderName
			if name == "" {
				name = "X-API-Key"
			}
			req.Header.Set(name, s.auth.APIKey)
		}
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	resp, e := s.client.Do(req)
	if e != nil {
		var ue *url.Error
		if errors.As(e, &ue) {
			return fmt.Errorf("loki sink: request failed: %w", ue.Err)
		}
		return fmt.Errorf("loki sink: request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("loki sink: push rejected with HTTP %d", resp.StatusCode)
	}
	return nil
}
func (s *lokiSink) Close() error { s.client.CloseIdleConnections(); return nil }
