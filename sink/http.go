package sink

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// httpMethods is the allow-list of methods accepted by newHTTP.
var httpMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

type httpSink struct {
	url         string
	method      string
	contentType string
	bodyMode    string
	headers     map[string]string
	auth        *spec.HTTPAuth
	c           *http.Client
}

func newHTTP(cfg spec.SinkCfg) (*httpSink, error) {
	url := strings.TrimRight(cfg.URL, "/")
	if cfg.Subject != "" {
		url = url + "/" + strings.TrimLeft(cfg.Subject, "/")
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodPost
	}
	if !httpMethods[method] {
		return nil, fmt.Errorf("http sink: unsupported method %q", cfg.Method)
	}

	contentType := cfg.ContentType
	if contentType == "" {
		contentType = "application/json"
	}

	bodyMode := cfg.BodyMode
	if bodyMode == "" {
		bodyMode = "wrapped"
	}
	if bodyMode != "wrapped" && bodyMode != "raw" {
		return nil, fmt.Errorf("http sink: unsupported bodyMode %q", cfg.BodyMode)
	}

	if cfg.Auth != nil {
		switch cfg.Auth.Type {
		case "bearer", "basic", "apiKey":
			// ok
		default:
			return nil, fmt.Errorf("http sink: unsupported auth type %q", cfg.Auth.Type)
		}
	}

	return &httpSink{
		url:         url,
		method:      method,
		contentType: contentType,
		bodyMode:    bodyMode,
		headers:     cfg.Headers,
		auth:        cfg.Auth,
		c:           &http.Client{},
	}, nil
}

func (s *httpSink) Send(msg *message.Message) error {
	var body []byte
	if msg.Payload != nil {
		body = msg.Payload
	} else if s.bodyMode == "raw" {
		body = msg.Raw
	} else {
		payload := map[string]interface{}{
			"fields": msg.Fields,
			"meta":   msg.Meta,
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("http sink: marshal: %w", err)
		}
		body = data
	}

	req, err := http.NewRequest(s.method, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http sink: new request: %w", err)
	}
	req.Header.Set("Content-Type", s.contentType)

	switch {
	case s.auth == nil:
		// no auth
	case s.auth.Type == "bearer":
		req.Header.Set("Authorization", "Bearer "+s.auth.Token)
	case s.auth.Type == "basic":
		req.SetBasicAuth(s.auth.Username, s.auth.Password)
	case s.auth.Type == "apiKey":
		headerName := s.auth.HeaderName
		if headerName == "" {
			headerName = "X-API-Key"
		}
		req.Header.Set(headerName, s.auth.APIKey)
	}

	// User-supplied headers are applied last so they can override the
	// Content-Type or auth headers set above if the user explicitly
	// configured a conflicting header.
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	resp, err := s.c.Do(req)
	if err != nil {
		return fmt.Errorf("http sink: do: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("http sink: server returned %d", resp.StatusCode)
	}
	return nil
}

func (s *httpSink) Close() error { return nil }
