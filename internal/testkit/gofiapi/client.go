// Package gofiapi is a thin test client for a running GoFi controller: the
// generated ConnectRPC clients plus the HTTP infrastructure endpoints
// (/readyz, /metrics), with helpers to convert spec.FlowDefinition to and
// from the protobuf Flow the API speaks.
package gofiapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/spec"
)

// Client talks to one controller.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Flows   gofiv1connect.GoFiServiceClient
	Ops     gofiv1connect.OperationsServiceClient
	History gofiv1connect.FlowHistoryServiceClient
}

// New returns a client for baseURL (for example http://127.0.0.1:9090).
func New(baseURL string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 8}}
	// Streams must not be cut by the client timeout.
	streamClient := &http.Client{Transport: httpClient.Transport}
	return &Client{
		BaseURL: baseURL,
		HTTP:    httpClient,
		Flows:   gofiv1connect.NewGoFiServiceClient(streamClient, baseURL),
		Ops:     gofiv1connect.NewOperationsServiceClient(httpClient, baseURL),
		History: gofiv1connect.NewFlowHistoryServiceClient(httpClient, baseURL),
	}
}

// ToProto converts a definition the way the API does (JSON field names are
// shared between spec and the protobuf contract).
func ToProto(def spec.FlowDefinition) (*gofiv1.Flow, error) {
	data, err := json.Marshal(def)
	if err != nil {
		return nil, err
	}
	out := &gofiv1.Flow{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("flow to proto: %w", err)
	}
	return out, nil
}

// FromProto converts an API Flow back to a definition.
func FromProto(flow *gofiv1.Flow) (spec.FlowDefinition, error) {
	var def spec.FlowDefinition
	if flow == nil {
		return def, errors.New("nil flow")
	}
	data, err := protojson.Marshal(flow)
	if err != nil {
		return def, err
	}
	if err := json.Unmarshal(data, &def); err != nil {
		return def, fmt.Errorf("flow from proto: %w", err)
	}
	return def, nil
}

// Upsert saves a flow (create or replace).
func (c *Client) Upsert(ctx context.Context, def spec.FlowDefinition) error {
	flow, err := ToProto(def)
	if err != nil {
		return err
	}
	_, err = c.Flows.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flow}))
	return err
}

// Validate runs every check a save runs without saving, and returns the
// controller's warnings (for example blocked paths).
func (c *Client) Validate(ctx context.Context, def spec.FlowDefinition) ([]string, error) {
	flow, err := ToProto(def)
	if err != nil {
		return nil, err
	}
	resp, err := c.Flows.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flow, ValidateOnly: true}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Warnings, nil
}

// Apply saves a flow with a revision message and returns the controller's
// warnings.
func (c *Client) Apply(ctx context.Context, def spec.FlowDefinition, message string) ([]string, error) {
	flow, err := ToProto(def)
	if err != nil {
		return nil, err
	}
	resp, err := c.Flows.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flow, Message: message}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Warnings, nil
}

// Mode reports the controller's deployment mode and driver.
func (c *Client) Mode(ctx context.Context) (*gofiv1.GetModeResponse, error) {
	resp, err := c.Flows.GetMode(ctx, connect.NewRequest(&gofiv1.GetModeRequest{}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Delete removes a flow.
func (c *Client) Delete(ctx context.Context, name string) error {
	_, err := c.Flows.DeleteFlow(ctx, connect.NewRequest(&gofiv1.DeleteFlowRequest{Name: name}))
	return err
}

// List returns every stored flow.
func (c *Client) List(ctx context.Context) ([]spec.FlowDefinition, error) {
	resp, err := c.Flows.ListFlows(ctx, connect.NewRequest(&gofiv1.ListFlowsRequest{}))
	if err != nil {
		return nil, err
	}
	out := make([]spec.FlowDefinition, 0, len(resp.Msg.Flows))
	for _, flow := range resp.Msg.Flows {
		def, err := FromProto(flow)
		if err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, nil
}

// Get returns one flow.
func (c *Client) Get(ctx context.Context, name string) (spec.FlowDefinition, error) {
	resp, err := c.Flows.GetFlow(ctx, connect.NewRequest(&gofiv1.GetFlowRequest{Name: name}))
	if err != nil {
		return spec.FlowDefinition{}, err
	}
	return FromProto(resp.Msg.Flow)
}

// Runtime reports the compute backing a flow (nil runtime: none).
func (c *Client) Runtime(ctx context.Context, name string) (*gofiv1.RuntimeInfo, error) {
	resp, err := c.Flows.GetRuntimeStatus(ctx, connect.NewRequest(&gofiv1.GetRuntimeStatusRequest{Name: name}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Runtime, nil
}

// ListStatus returns every flow's run state.
func (c *Client) ListStatus(ctx context.Context) ([]*gofiv1.FlowStatus, error) {
	resp, err := c.Ops.ListFlowStatus(ctx, connect.NewRequest(&gofiv1.ListFlowStatusRequest{}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Flows, nil
}

// Status returns one flow's run state.
func (c *Client) Status(ctx context.Context, name string) (*gofiv1.FlowStatus, error) {
	resp, err := c.Ops.ListFlowStatus(ctx, connect.NewRequest(&gofiv1.ListFlowStatusRequest{Name: name}))
	if err != nil {
		return nil, err
	}
	for _, status := range resp.Msg.Flows {
		if status.Name == name {
			return status, nil
		}
	}
	return nil, fmt.Errorf("flow %q has no status", name)
}

func (c *Client) Start(ctx context.Context, name string) error {
	_, err := c.Ops.StartFlow(ctx, connect.NewRequest(&gofiv1.StartFlowRequest{Name: name}))
	return err
}
func (c *Client) Stop(ctx context.Context, name string) error {
	_, err := c.Ops.StopFlow(ctx, connect.NewRequest(&gofiv1.StopFlowRequest{Name: name}))
	return err
}
func (c *Client) Restart(ctx context.Context, name string) error {
	_, err := c.Ops.RestartFlow(ctx, connect.NewRequest(&gofiv1.RestartFlowRequest{Name: name}))
	return err
}

// Revisions lists a flow's revision summaries, newest first.
func (c *Client) Revisions(ctx context.Context, flow string) ([]*gofiv1.FlowRevision, error) {
	resp, err := c.History.ListFlowRevisions(ctx, connect.NewRequest(&gofiv1.ListFlowRevisionsRequest{Flow: flow}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Revisions, nil
}

// Revision returns one revision with its definition.
func (c *Client) Revision(ctx context.Context, flow string, number int64) (*gofiv1.FlowRevision, error) {
	resp, err := c.History.GetFlowRevision(ctx, connect.NewRequest(&gofiv1.GetFlowRevisionRequest{Flow: flow, Revision: number}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Revision, nil
}

// Rollback restores a revision through the normal save path.
func (c *Client) Rollback(ctx context.Context, flow string, number int64) error {
	_, err := c.RollbackWithMessage(ctx, flow, number, "")
	return err
}

// RollbackWithMessage is Rollback with a revision message; the response says
// whether a new revision was recorded.
func (c *Client) RollbackWithMessage(ctx context.Context, flow string, number int64, message string) (*gofiv1.RollbackFlowResponse, error) {
	resp, err := c.History.RollbackFlow(ctx, connect.NewRequest(&gofiv1.RollbackFlowRequest{Flow: flow, Revision: number, Message: message}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Ready reports whether /readyz answers 200.
func (c *Client) Ready(ctx context.Context) bool { return c.probe(ctx, "/readyz") }

// Healthy reports whether /healthz answers 200.
func (c *Client) Healthy(ctx context.Context) bool { return c.probe(ctx, "/healthz") }

func (c *Client) probe(ctx context.Context, path string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return false
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK
}

// WaitReady polls /readyz until it answers or ctx ends.
func (c *Client) WaitReady(ctx context.Context) error {
	for {
		if c.Ready(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("controller at %s not ready: %w", c.BaseURL, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// WaitPhase polls until the flow reports phase (for example "running").
func (c *Client) WaitPhase(ctx context.Context, name, phase string) error {
	var last string
	for {
		status, err := c.Status(ctx, name)
		if err == nil && status.Phase == phase {
			return nil
		}
		if status != nil {
			last = status.Phase + " " + status.Message
		} else if err != nil {
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("flow %q did not reach %s (last: %s): %w", name, phase, last, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// WaitAllPhase polls until every named flow reports phase.
func (c *Client) WaitAllPhase(ctx context.Context, names []string, phase string) error {
	for {
		statuses, err := c.ListStatus(ctx)
		if err == nil {
			byName := map[string]string{}
			for _, s := range statuses {
				byName[s.Name] = s.Phase
			}
			missing := 0
			for _, name := range names {
				if byName[name] != phase {
					missing++
				}
			}
			if missing == 0 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("flows did not all reach %s: %w", phase, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Metrics scrapes /metrics into sample -> value, keyed as the exposition
// line prefix (name{labels} with label order as served).
func (c *Client) Metrics(ctx context.Context) (Samples, error) {
	return ScrapeMetrics(ctx, c.HTTP, c.BaseURL+"/metrics")
}

// Samples is a flat scrape.
type Samples map[string]float64

// Value sums every sample of name whose labels contain all of match.
func (s Samples) Value(name string, match map[string]string) float64 {
	total := 0.0
	for key, value := range s {
		metric, labels := splitSample(key)
		if metric != name {
			continue
		}
		ok := true
		for k, v := range match {
			if labels[k] != v {
				ok = false
				break
			}
		}
		if ok {
			total += value
		}
	}
	return total
}

// Each calls fn with the labels and value of every sample of name.
func (s Samples) Each(name string, fn func(labels map[string]string, value float64)) {
	for key, value := range s {
		if metric, labels := splitSample(key); metric == name {
			fn(labels, value)
		}
	}
}

// Has reports whether any sample of name exists.
func (s Samples) Has(name string) bool {
	for key := range s {
		if metric, _ := splitSample(key); metric == name {
			return true
		}
	}
	return false
}

func splitSample(key string) (string, map[string]string) {
	labels := map[string]string{}
	open := strings.IndexByte(key, '{')
	if open < 0 {
		return key, labels
	}
	name := key[:open]
	body := strings.TrimSuffix(key[open+1:], "}")
	for _, pair := range splitLabels(body) {
		k, v, ok := strings.Cut(pair, "=")
		if ok {
			labels[k] = strings.Trim(v, `"`)
		}
	}
	return name, labels
}

func splitLabels(body string) []string {
	var out []string
	var current strings.Builder
	inQuote := false
	for i := 0; i < len(body); i++ {
		ch := body[i]
		switch {
		case ch == '\\' && inQuote && i+1 < len(body):
			current.WriteByte(ch)
			i++
			current.WriteByte(body[i])
		case ch == '"':
			inQuote = !inQuote
			current.WriteByte(ch)
		case ch == ',' && !inQuote:
			out = append(out, current.String())
			current.Reset()
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

// ScrapeMetrics fetches a Prometheus text exposition.
func ScrapeMetrics(ctx context.Context, client *http.Client, url string) (Samples, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	out := Samples{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		space := strings.LastIndexByte(line, ' ')
		if space < 0 {
			continue
		}
		key, raw := line[:space], line[space+1:]
		// Histograms with timestamps have two trailing fields; take the value.
		if fields := strings.Fields(line); len(fields) >= 3 && !strings.HasSuffix(fields[0], "}") && !strings.Contains(fields[0], "{") {
			key, raw = fields[0], fields[1]
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		out[key] = value
	}
	return out, scanner.Err()
}
