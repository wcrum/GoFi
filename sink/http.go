package sink

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cnak-us/gogo/message"
	"github.com/cnak-us/gogo/spec"
)

type httpSink struct {
	url string
	c   *http.Client
}

func newHTTP(cfg spec.SinkCfg) *httpSink {
	url := strings.TrimRight(cfg.URL, "/")
	if cfg.Subject != "" {
		url = url + "/" + strings.TrimLeft(cfg.Subject, "/")
	}
	return &httpSink{url: url, c: &http.Client{}}
}

func (s *httpSink) Send(msg *message.Message) error {
	payload := map[string]interface{}{
		"fields": msg.Fields,
		"meta":   msg.Meta,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("http sink: marshal: %w", err)
	}
	resp, err := s.c.Post(s.url, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("http sink: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("http sink: server returned %d", resp.StatusCode)
	}
	return nil
}

func (s *httpSink) Close() error { return nil }
