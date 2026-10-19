package source

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// zenohQueryTimeout bounds the optional storage query made on each connect.
const zenohQueryTimeout = 10 * time.Second

// zenohSource subscribes to a key expression through the Zenoh router's REST
// plugin: GET /<keyExpr> with Accept: text/event-stream declares a
// subscriber, and the router streams each sample as one SSE event whose name
// is the sample kind (PUT or DELETE) and whose data is the JSON sample
// {"key", "value", "encoding", "timestamp"} (docs/zenoh.md).
type zenohSource struct {
	flow   string
	cfg    *spec.ZenohSubscribe
	url    string
	stream *sseStream
	query  *http.Client
	emit   Emit
}

func noRedirects(*http.Request, []*http.Request) error {
	return fmt.Errorf("zenoh REST: router redirects are not supported")
}

func newZenohSource(flow string, cfg *spec.ZenohSubscribe, emit Emit) (*zenohSource, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	path, _ := spec.ZenohKeyPath(cfg.KeyExpr, true)
	z := &zenohSource{flow: flow, cfg: cfg, url: strings.TrimRight(cfg.URL, "/") + "/" + path, emit: emit}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The stream stays open indefinitely, so it has no overall timeout.
	z.stream = &sseStream{
		cfg:    &spec.StreamSubscribe{URL: z.url, Headers: cfg.Headers, Auth: cfg.Auth},
		client: &http.Client{Transport: transport, CheckRedirect: noRedirects},
	}
	z.query = &http.Client{Timeout: zenohQueryTimeout, Transport: transport, CheckRedirect: noRedirects}
	if cfg.Query {
		// The subscriber is declared once the stream answers, so querying
		// afterwards leaves no gap (a value may arrive twice).
		z.stream.onOpen = func(ctx context.Context) error {
			if err := z.queryStorages(ctx); err != nil && ctx.Err() == nil {
				observability.SourceFailure(flow, err)
				slog.Warn("zenoh storage query failed; continuing with the subscription", "flow", flow, "keyExpr", cfg.KeyExpr, "error", err)
			}
			return nil
		}
	}
	return z, nil
}

// run reads the subscription until it ends.
func (z *zenohSource) run(ctx context.Context) error {
	return z.stream.readEvents(ctx, func(event string, data []byte) {
		kind := strings.ToUpper(strings.TrimSpace(event))
		if kind == "" {
			kind = "PUT"
		}
		if !z.cfg.AcceptsKind(kind) {
			return
		}
		var sample zenohSample
		if err := json.Unmarshal(data, &sample); err != nil {
			observability.SourceEvent(z.flow, "zenoh", "error")
			slog.Warn("zenoh sample is not JSON; skipped", "flow", z.flow, "error", err)
			return
		}
		z.deliver(ctx, sample, kind, false)
	})
}

// queryStorages reads current values (GET /<keyExpr> as JSON) and emits each
// reply as a PUT sample.
func (z *zenohSource) queryStorages(ctx context.Context) error {
	if !z.cfg.AcceptsKind("PUT") {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, z.url, nil)
	if err != nil {
		return err
	}
	req.Header = streamHeader(z.stream.cfg)
	req.Header.Set("Accept", "application/json")
	res, err := z.query.Do(req)
	if err != nil {
		return fmt.Errorf("zenoh query: %w", zenohRequestError(err))
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("zenoh query: HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxMessageBytes+1))
	if err != nil {
		return fmt.Errorf("zenoh query: %w", err)
	}
	if len(body) > MaxMessageBytes {
		return fmt.Errorf("zenoh query: reply exceeds %d bytes", MaxMessageBytes)
	}
	var samples []zenohSample
	if err := json.Unmarshal(body, &samples); err != nil {
		return fmt.Errorf("zenoh query: reply is not a JSON array of samples: %w", err)
	}
	for _, sample := range samples {
		if ctx.Err() != nil {
			return nil
		}
		if sample.Key == "ERROR" { // the plugin reports failed replies this way
			observability.SourceEvent(z.flow, "zenoh", "error")
			continue
		}
		z.deliver(ctx, sample, "PUT", true)
	}
	return nil
}

func (z *zenohSource) deliver(ctx context.Context, sample zenohSample, kind string, queried bool) {
	timestamp := sample.timestamp()
	var payload []byte
	if kind == "DELETE" {
		// A deletion has no value; emit a small JSON document so raw framing
		// still yields a frame.
		payload, _ = json.Marshal(map[string]string{"key": sample.Key, "kind": kind, "timestamp": timestamp})
	} else {
		payload = zenohPayload(sample.Encoding, sample.Value)
	}
	meta := map[string]string{"zenoh.key": sample.Key, "zenoh.kind": kind, "zenoh.encoding": sample.Encoding}
	if timestamp != "" {
		meta["zenoh.timestamp"] = timestamp
	}
	if queried {
		meta["zenoh.query"] = "true"
	}
	observability.SourceEvent(z.flow, "zenoh", "ok")
	z.emit(message.WithSourceMeta(ctx, meta), payload, "zenoh:"+z.cfg.KeyExpr)
}

// zenohSample is the REST plugin's JSON sample. Zenoh 1.x sends timestamp;
// 0.x routers sent time (with "None" when absent).
type zenohSample struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Encoding  string          `json:"encoding"`
	Timestamp *string         `json:"timestamp"`
	Time      *string         `json:"time"`
}

func (s zenohSample) timestamp() string {
	for _, value := range []*string{s.Timestamp, s.Time} {
		if value != nil && *value != "" && *value != "None" {
			return *value
		}
	}
	return ""
}

// zenohPayload recovers a sample's bytes from the plugin's JSON value: JSON
// encodings arrive as JSON (kept as JSON text), text encodings as strings,
// and everything else as base64 strings. An empty value is null.
func zenohPayload(encoding string, value json.RawMessage) []byte {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || string(value) == "null" {
		return []byte{}
	}
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(encoding)), ";")
	switch base {
	case "application/json", "text/json", "text/json5":
		return bytes.Clone(value)
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return bytes.Clone(value) // a non-string JSON value: keep its text
	}
	switch base {
	case "text/plain", "zenoh/string":
		return []byte(text)
	}
	if decoded, err := base64.StdEncoding.DecodeString(text); err == nil {
		return decoded
	}
	return []byte(text)
}

// zenohRequestError drops the request URL from a client error; the cause
// (refused, timeout, TLS) is kept.
func zenohRequestError(err error) error {
	if urlErr, ok := err.(*url.Error); ok {
		return urlErr.Err
	}
	return err
}
