package sink

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

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
		c:           &http.Client{Timeout: sendTimeout, Transport: newTransport()},
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
		return fmt.Errorf("http sink: do: %w", withoutURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, statusBodyPeek))
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))
		return fmt.Errorf("http sink: server returned %w", NewStatusError(resp.StatusCode, body))
	}
	// Drain a little so the connection can be reused; never read an endless body.
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))
	return nil
}

// statusBodyPeek is how much of an error response StatusError inspects;
// maxDrain is how much of any response is read and discarded.
const (
	statusBodyPeek = 256
	maxDrain       = 64 << 10
)

// sendTimeout bounds one HTTP send, so a destination that never answers (or
// never finishes its body) cannot stall the flow.
const sendTimeout = 30 * time.Second

// withoutURL drops the request URL from a client error: its query string may
// carry credentials (?token=), and the error reaches logs and route health.
// The cause (refused, timeout, TLS) is kept.
func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// StatusError is an HTTP error response. It records whether the response is
// an Istio authorization denial (a waypoint answers 403 "RBAC: access
// denied"), so a failed route can be told apart from an application error.
type StatusError struct {
	Code   int
	denied bool
}

// NewStatusError builds a StatusError from the status code and the start of
// the response body.
func NewStatusError(code int, body []byte) *StatusError {
	if len(body) > statusBodyPeek {
		body = body[:statusBodyPeek]
	}
	return &StatusError{Code: code, denied: code == http.StatusForbidden && bytes.HasPrefix(bytes.TrimSpace(body), []byte("RBAC: access denied"))}
}

func (e *StatusError) Error() string {
	if e.denied {
		return fmt.Sprintf("HTTP %d (RBAC: access denied)", e.Code)
	}
	return fmt.Sprintf("HTTP %d", e.Code)
}

// HTTPStatus and PolicyDenied are read by observability.ClassifyError.
func (e *StatusError) HTTPStatus() int    { return e.Code }
func (e *StatusError) PolicyDenied() bool { return e.denied }

func (s *httpSink) Close() error { s.c.CloseIdleConnections(); return nil }
