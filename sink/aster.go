package sink

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

const asterPublishPath = "/aster.application.v1alpha1.AsterApplicationService/PublishEvent"

type asterSink struct {
	cfg      spec.AsterCfg
	url      string
	bodyMode string
	client   *http.Client
}

// JSON byte slices use base64, as required by protobuf JSON. uint64 values
// in the response use decimal strings, as emitted by the Aster API.
type asterPublication struct {
	OperationKey []byte `json:"operationKey"`
	Topic        string `json:"topic"`
	Scope        string `json:"scope"`
	Priority     string `json:"priority"`
	LogicalKey   []byte `json:"logicalKey"`
	Payload      []byte `json:"payload"`
}

type asterReceipt struct {
	ID               []byte `json:"id"`
	Publisher        []byte `json:"publisher"`
	PublisherCounter uint64 `json:"publisherCounter,string"`
	EventSequence    uint64 `json:"eventSequence,string"`
	Priority         string `json:"priority"`
	AcceptanceMarker uint64 `json:"acceptanceMarker,string"`
}

func newAster(cfg spec.SinkCfg) (*asterSink, error) {
	s, err := configureAster(cfg)
	if err != nil {
		return nil, err
	}
	if s.cfg.TokenFile == "" && s.cfg.Token == "" {
		s.cfg.TokenFile = os.Getenv("GOFI_ASTER_TOKEN_FILE")
	}
	if s.cfg.TokenFile != "" && !filepath.IsAbs(s.cfg.TokenFile) {
		return nil, fmt.Errorf("aster sink: tokenFile must be an absolute mounted path")
	}
	if _, err := s.token(); err != nil {
		return nil, err
	}
	return s, nil
}

// configureAster validates only configuration; credentials are read by the
// listener process where the agent's token volume is mounted.
func configureAster(cfg spec.SinkCfg) (*asterSink, error) {
	if cfg.Aster == nil {
		return nil, fmt.Errorf("aster sink: aster configuration is required")
	}
	a := *cfg.Aster
	if strings.TrimSpace(a.Topic) == "" || strings.TrimSpace(a.Scope) == "" {
		return nil, fmt.Errorf("aster sink: topic and scope are required")
	}
	if a.LogicalKey != "" && a.LogicalKeyField != "" {
		return nil, fmt.Errorf("aster sink: choose logicalKey or logicalKeyField")
	}
	if a.Priority == "" {
		a.Priority = "routine"
	}
	switch a.Priority {
	case "routine", "priority", "immediate", "flash":
	default:
		return nil, fmt.Errorf("aster sink: unsupported priority")
	}
	if a.TimeoutSeconds == 0 {
		a.TimeoutSeconds = 10
	}
	if a.TimeoutSeconds < 1 || a.TimeoutSeconds > 30 {
		return nil, fmt.Errorf("aster sink: timeoutSeconds must be between 1 and 30")
	}
	if a.Token != "" && a.TokenFile != "" {
		return nil, fmt.Errorf("aster sink: choose token or tokenFile")
	}
	if a.TokenFile != "" && !filepath.IsAbs(a.TokenFile) {
		return nil, fmt.Errorf("aster sink: tokenFile must be an absolute mounted path")
	}
	baseURL := cfg.URL
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8181"
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("aster sink: URL must be an HTTP loopback origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() || u.Port() == "" {
		return nil, fmt.Errorf("aster sink: URL must use a literal loopback IP and explicit port")
	}
	if port, err := net.LookupPort("tcp", u.Port()); err != nil || port == 0 {
		return nil, fmt.Errorf("aster sink: invalid agent port")
	}
	mode := cfg.BodyMode
	if mode == "" {
		mode = "raw"
	}
	if mode != "raw" && mode != "wrapped" {
		return nil, fmt.Errorf("aster sink: bodyMode must be raw or wrapped")
	}
	s := &asterSink{
		cfg: a, url: strings.TrimRight(baseURL, "/") + asterPublishPath, bodyMode: mode,
		client: &http.Client{
			Timeout: time.Duration(a.TimeoutSeconds) * time.Second,
			// Dedicated local traffic never uses environment-configured proxies.
			Transport: &http.Transport{Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("aster sink: agent redirects are forbidden")
			},
		},
	}
	if a.Token != "" && (len(a.Token) > 256 || strings.ContainsAny(a.Token, " \t\r\n")) {
		return nil, fmt.Errorf("aster sink: a valid bearer token is required")
	}
	return s, nil
}

func (s *asterSink) token() (string, error) {
	token := s.cfg.Token
	if s.cfg.TokenFile != "" {
		file, err := os.Open(s.cfg.TokenFile)
		if err != nil {
			return "", fmt.Errorf("aster sink: token file unavailable")
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 258))
		if err != nil || len(data) > 257 {
			return "", fmt.Errorf("aster sink: invalid token file")
		}
		token = strings.TrimSpace(string(data))
	}
	if token == "" || len(token) > 256 || strings.ContainsAny(token, " \t\r\n") {
		return "", fmt.Errorf("aster sink: a valid bearer token is required")
	}
	return token, nil
}

func (s *asterSink) Send(msg *message.Message) error {
	if msg.ID == "" {
		return fmt.Errorf("aster sink: message identity is required")
	}
	logicalKey := s.cfg.LogicalKey
	if s.cfg.LogicalKeyField != "" {
		value, ok := msg.Fields[s.cfg.LogicalKeyField].(string)
		if !ok {
			return fmt.Errorf("aster sink: logical key field must exist and be a string")
		}
		logicalKey = value
	}
	payload := msg.Raw
	if s.bodyMode == "wrapped" {
		var err error
		payload, err = json.Marshal(map[string]interface{}{"fields": msg.Fields, "meta": msg.Meta})
		if err != nil {
			return fmt.Errorf("aster sink: payload encoding failed")
		}
	}
	// One operation key per ingest event and destination selector, independent
	// of RPC attempt. A retry with changed content intentionally conflicts at
	// the agent. GoFi has no restart-safe outbox or automatic send retries.
	identity, _ := json.Marshal([]string{msg.ID, s.url, s.cfg.Topic, s.cfg.Scope, s.cfg.LogicalKey, s.cfg.LogicalKeyField, s.cfg.Priority, s.bodyMode})
	operationKey := sha256.Sum256(identity)
	priority := "PRIORITY_" + strings.ToUpper(s.cfg.Priority)
	body, err := json.Marshal(asterPublication{
		OperationKey: operationKey[:], Topic: s.cfg.Topic, Scope: s.cfg.Scope,
		Priority: priority, LogicalKey: []byte(logicalKey), Payload: payload,
	})
	if err != nil {
		return fmt.Errorf("aster sink: request encoding failed")
	}
	token, err := s.token() // read each time so an atomically replaced mount can rotate
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("aster sink: request construction failed")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Connect-Timeout-Ms", fmt.Sprint(s.cfg.TimeoutSeconds*1000))
	response, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("aster sink: agent request failed; publication outcome may be unknown")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// The response may contain confidential remote diagnostics. Expose
		// only status, never raw body, bearer credentials, or request values.
		return fmt.Errorf("aster sink: agent rejected publication (HTTP %d)", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return fmt.Errorf("aster sink: invalid publication receipt; outcome may be unknown")
	}
	var receipt asterReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&receipt); err != nil {
		return fmt.Errorf("aster sink: invalid publication receipt; outcome may be unknown")
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF || len(receipt.ID) != 32 || len(receipt.Publisher) != 32 || receipt.PublisherCounter == 0 || receipt.EventSequence == 0 || receipt.AcceptanceMarker == 0 || receipt.Priority != priority {
		return fmt.Errorf("aster sink: incomplete publication receipt; outcome may be unknown")
	}
	return nil
}

func (s *asterSink) Close() error {
	s.client.CloseIdleConnections()
	return nil
}
