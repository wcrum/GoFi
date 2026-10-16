package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// runFetch polls on an interval. Polls run one at a time, so a slow response
// delays the next poll instead of overlapping it.
func runFetch(ctx context.Context, flow string, f *spec.FetchSource, emit Emit) error {
	keys, err := spec.ParseSplit(f.Split)
	if err != nil {
		return err
	}
	every, timeout := f.HTTP.FetchTimings()
	client := &http.Client{Timeout: timeout}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if err := poll(ctx, client, f.HTTP, keys, f.Split != "", emit); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			observability.SourceEvent(flow, "fetch", "error")
			slog.Warn("fetch failed; retrying at the next interval", "flow", flow, "url", f.HTTP.URL, "error", err, "every", every)
		} else {
			observability.SourceEvent(flow, "fetch", "ok")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func poll(ctx context.Context, client *http.Client, h *spec.HTTPFetch, keys []string, split bool, emit Emit) error {
	method := strings.ToUpper(h.Method)
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if h.Body != "" {
		body = strings.NewReader(h.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.URL, body)
	if err != nil {
		return err
	}
	sink.ApplyHTTPAuth(req.Header, h.Auth)
	for name, value := range h.Headers {
		req.Header.Set(name, value)
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxMessageBytes+1))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if len(data) > MaxMessageBytes {
		return fmt.Errorf("response exceeds %d bytes", MaxMessageBytes)
	}
	remote := "fetch:" + h.URL
	if !split {
		emit(ctx, data, remote)
		return nil
	}
	items, err := SplitJSON(data, keys)
	if err != nil {
		return err
	}
	for _, item := range items {
		emit(ctx, item, remote)
	}
	return nil
}

// SplitJSON returns each element of the array found by descending keys from
// the document root, re-encoded as JSON.
func SplitJSON(data []byte, keys []string) ([][]byte, error) {
	var node any
	if err := json.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("split: response is not JSON: %w", err)
	}
	path := "$"
	for _, key := range keys {
		object, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("split: %s is not an object", path)
		}
		path += "." + key
		if node, ok = object[key]; !ok {
			return nil, fmt.Errorf("split: %s is missing", path)
		}
	}
	array, ok := node.([]any)
	if !ok {
		return nil, fmt.Errorf("split: %s is not an array", path)
	}
	out := make([][]byte, 0, len(array))
	for _, item := range array {
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		out = append(out, encoded)
	}
	return out, nil
}
