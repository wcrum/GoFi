package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// runFetch polls an HTTP endpoint on an interval (pollLoop: no overlapping
// polls).
func runFetch(ctx context.Context, flow string, f *spec.FetchSource, emit Emit) error {
	keys, err := spec.ParseSplit(f.Split)
	if err != nil {
		return err
	}
	every, timeout := f.HTTP.FetchTimings()
	warnLeaderOnly(flow, f)
	client := &http.Client{Timeout: timeout}
	return pollLoop(ctx, flow, "fetch", f.HTTP.URL, every, func(ctx context.Context) error {
		return poll(ctx, client, f.HTTP, keys, f.Split != "", emit)
	})
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
		// Without the URL: its query string may carry credentials.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Errorf("%s: %w", req.Method, urlErr.Err)
		}
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxMessageBytes+1))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return sink.NewStatusError(res.StatusCode, data)
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

// warnLeaderOnly notes a fetch source that asked for leader-only polling.
// Leader election between replicas is not implemented: every replica polls,
// and a scaled-out fetch flow emits each response once per replica.
func warnLeaderOnly(flow string, f *spec.FetchSource) {
	if f.LeaderOnly {
		slog.Warn("fetch source is marked leaderOnly, but leader election is not implemented: every replica of this flow polls", "flow", flow)
	}
}
