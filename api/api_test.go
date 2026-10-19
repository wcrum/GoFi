package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/spec"
)

// Invoke the generated HTTP client through the actual handler and codecs without
// requiring a listening socket for unary tests. Streaming tests use real HTTP.
type handlerClient struct{ handler http.Handler }

func (c handlerClient) Do(req *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, req)
	return recorder.Result(), nil
}
func newTestClient() (gofiv1connect.GoFiServiceClient, *controller.Controller, http.Handler) {
	ctrl := controller.New(driver.NewInProcess())
	handler := New(ctrl, nil, deploymode.ModeBinary)
	return gofiv1connect.NewGoFiServiceClient(handlerClient{handler}, "http://gofi.test"), ctrl, handler
}
func flowMessage(t *testing.T, def spec.FlowDefinition) *gofiv1.Flow {
	t.Helper()
	value := &gofiv1.Flow{}
	if err := toProto(def, value); err != nil {
		t.Fatal(err)
	}
	return value
}
func basicDefinition() spec.FlowDefinition {
	return spec.FlowDefinition{Name: "route", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000, Network: "tcp"}}, Framing: spec.Framing{Mode: "line"}, Parser: spec.ParserCfg{Format: "json"}}
}
func TestConnectBinaryDiscoveryAndConfiguration(t *testing.T) {
	client, _, _ := newTestClient()
	ctx := context.Background()
	endpoints, err := client.ListEndpoints(ctx, connect.NewRequest(&gofiv1.ListEndpointsRequest{}))
	if err != nil || len(endpoints.Msg.Endpoints) != 0 {
		t.Fatalf("discovery: %v %v", endpoints, err)
	}
	mode, err := client.GetMode(ctx, connect.NewRequest(&gofiv1.GetModeRequest{}))
	if err != nil || mode.Msg.Mode != "binary" || mode.Msg.DriverKind != "inprocess" || mode.Msg.ProcessRole != "controller" {
		t.Fatalf("mode: %v %v", mode, err)
	}
	configuration, err := client.GetConfiguration(ctx, connect.NewRequest(&gofiv1.GetConfigurationRequest{}))
	if err != nil || configuration.Msg.Configuration.ApiVersion != desired.APIVersion || configuration.Msg.Configuration.Source != "memory" {
		t.Fatalf("configuration: %v %v", configuration, err)
	}
}
func TestConnectCRUDRoundTripsFullTypedDefinition(t *testing.T) {
	client, ctrl, _ := newTestClient()
	ctx := context.Background()
	def := basicDefinition()
	def.Decode = &spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: "base64"}}}
	def.Parser.ErrorPolicy = "drop"
	def.Stages = []spec.StageSpec{{ID: "app", Kind: "filter", Type: "regex", Field: "app", Expression: "^payments$"}, {ID: "status", Kind: "filter", Type: "cel", Expression: `fields["status"] >= 200`}}
	def.Sinks = []spec.SinkCfg{{Type: "http", URL: "https://receiver.example/ingest", Headers: map[string]string{"X-App": "GoFi"}, Auth: &spec.HTTPAuth{Type: "apiKey", HeaderName: "X-Token", APIKey: "example"}, When: `fields["app"] == "payments"`}}
	def.DeadLetterSink = &spec.SinkCfg{Type: "stdout", When: `meta["droppedBy"] != ""`}
	def.TestCases = []spec.TestCase{{ID: "sample", Name: "Sample event", Raw: `{"app":"payments"}`}}
	saved, err := client.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, def)}))
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip spec.FlowDefinition
	if err := fromProto(saved.Msg.Flow, &roundTrip); err != nil {
		t.Fatal(err)
	}
	// Responses show credentials as the sentinel; everything else is intact.
	if !reflect.DeepEqual(spec.Redact(def), roundTrip) {
		t.Fatalf("typed contract lost fields:\nwant %+v\ngot %+v", spec.Redact(def), roundTrip)
	}
	if roundTrip.Sinks[0].Auth.APIKey != spec.Redacted {
		t.Fatalf("API key shown in clear: %+v", roundTrip.Sinks[0].Auth)
	}
	if stored, _ := ctrl.Get(def.Name); stored.Sinks[0].Auth.APIKey != "example" {
		t.Fatalf("stored definition redacted: %+v", stored.Sinks[0].Auth)
	}
	got, err := client.GetFlow(ctx, connect.NewRequest(&gofiv1.GetFlowRequest{Name: def.Name}))
	if err != nil || len(got.Msg.Flow.Stages) != 2 || got.Msg.Flow.Stages[1].Expression != def.Stages[1].Expression {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.Msg.Flow.Sinks[0].Auth.ApiKey != spec.Redacted {
		t.Fatalf("GetFlow shows the API key: %v", got.Msg.Flow.Sinks[0].Auth)
	}
	// Saving back what a read showed keeps the stored credential; an edit
	// elsewhere goes through.
	got.Msg.Flow.Sinks[0].Headers["X-App"] = "Edited"
	if _, err := client.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: got.Msg.Flow})); err != nil {
		t.Fatal(err)
	}
	if stored, _ := ctrl.Get(def.Name); stored.Sinks[0].Auth.APIKey != "example" || stored.Sinks[0].Headers["X-App"] != "Edited" {
		t.Fatalf("redacted round trip changed the credential or lost the edit: %+v", stored.Sinks[0])
	}
	// A sentinel on a new flow has nothing to keep: refused before saving.
	fresh := basicDefinition()
	fresh.Name = "fresh"
	fresh.Sinks = []spec.SinkCfg{{Type: "http", URL: "https://receiver.example", Auth: &spec.HTTPAuth{Type: "bearer", Token: spec.Redacted}}}
	if _, err := client.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, fresh)})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("sentinel without a stored value: %v", err)
	}
	list, err := client.ListFlows(ctx, connect.NewRequest(&gofiv1.ListFlowsRequest{}))
	if err != nil || len(list.Msg.Flows) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if _, err := client.GetRuntimeStatus(ctx, connect.NewRequest(&gofiv1.GetRuntimeStatusRequest{Name: def.Name})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("runtime not yet reconciled must be NotFound: %v", err)
	}
	if _, err := client.DeleteFlow(ctx, connect.NewRequest(&gofiv1.DeleteFlowRequest{Name: def.Name})); err != nil {
		t.Fatal(err)
	}
	if len(ctrl.List()) != 0 {
		t.Fatal("delete retained desired resource")
	}
	if _, err := client.GetFlow(ctx, connect.NewRequest(&gofiv1.GetFlowRequest{Name: def.Name})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("deleted resource get: %v", err)
	}
}
func TestConnectRejectsInvalidConfigurationBeforePersistence(t *testing.T) {
	invalid := map[string]func(*spec.FlowDefinition){
		"empty name":            func(d *spec.FlowDefinition) { d.Name = "" },
		"invalid resource name": func(d *spec.FlowDefinition) { d.Name = "has spaces" },
		"missing port":          func(d *spec.FlowDefinition) { d.Source.Listen.Port = 0 },
		"negative port":         func(d *spec.FlowDefinition) { d.Source.Listen.Port = -1 },
		"oversized port":        func(d *spec.FlowDefinition) { d.Source.Listen.Port = 65536 },
		"invalid network":       func(d *spec.FlowDefinition) { d.Source.Listen.Network = "sctp" },
		"binary ClusterIP":      func(d *spec.FlowDefinition) { d.Source.Listen.Service = &spec.ServiceExposure{Type: "ClusterIP"} },
		"unknown parser":        func(d *spec.FlowDefinition) { d.Parser.Format = "unknown" },
		"invalid decoder": func(d *spec.FlowDefinition) {
			d.Decode = &spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: "unknown"}}}
		},
		"invalid framing": func(d *spec.FlowDefinition) { d.Framing.Mode = "unknown" },
		"duplicate stage IDs": func(d *spec.FlowDefinition) {
			d.Stages = []spec.StageSpec{{ID: "same", Kind: "filter", Type: "cel", Expression: "true"}, {ID: "same", Kind: "filter", Type: "cel", Expression: "false"}}
		},
		"unknown stage": func(d *spec.FlowDefinition) {
			d.Stages = []spec.StageSpec{{ID: "bad", Kind: "filter", Type: "unknown"}}
		},
		"invalid sink type":    func(d *spec.FlowDefinition) { d.Sinks = []spec.SinkCfg{{Type: "unknown"}} },
		"invalid sink address": func(d *spec.FlowDefinition) { d.Sinks = []spec.SinkCfg{{Type: "tcp", URL: "tcp://receiver"}} },
		"invalid HTTP address": func(d *spec.FlowDefinition) { d.Sinks = []spec.SinkCfg{{Type: "http", URL: "relative"}} },
		"invalid HTTP method": func(d *spec.FlowDefinition) {
			d.Sinks = []spec.SinkCfg{{Type: "http", URL: "https://receiver.example", Method: "INVALID"}}
		},
		"invalid sink condition": func(d *spec.FlowDefinition) { d.Sinks = []spec.SinkCfg{{Type: "stdout", When: "invalid syntax !"}} },
		"invalid dead letter": func(d *spec.FlowDefinition) {
			d.DeadLetterSink = &spec.SinkCfg{Type: "stdout", When: "invalid syntax !"}
		},
		"invalid python sink": func(d *spec.FlowDefinition) {
			d.Sinks = []spec.SinkCfg{{Type: "python", Python: &spec.PythonRef{Library: "library"}}}
		},
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			client, ctrl, _ := newTestClient()
			def := basicDefinition()
			mutate(&def)
			_, err := client.UpsertFlow(context.Background(), connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, def)}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("expected invalid_argument: %v", err)
			}
			if len(ctrl.List()) != 0 {
				t.Fatal("invalid request mutated desired state")
			}
		})
	}
}
func TestConnectPersistsResourcesAndValidatesAsterWithoutMountedSecret(t *testing.T) {
	ctrl := controller.New(driver.NewInProcess())
	file := &desired.File{Path: filepath.Join(t.TempDir(), "listeners.yaml")}
	if err := ctrl.WithStore(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	client := gofiv1connect.NewGoFiServiceClient(handlerClient{New(ctrl, nil, deploymode.ModeBinary)}, "http://gofi.test")
	def := basicDefinition()
	def.Sinks = []spec.SinkCfg{{Type: "aster", URL: "http://127.0.0.1:8181", Aster: &spec.AsterCfg{Topic: "events", Scope: "test", TokenFile: "/worker-only-mount/token"}}}
	if _, err := client.UpsertFlow(context.Background(), connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, def)})); err != nil {
		t.Fatal(err)
	}
	defs, err := file.Load(context.Background())
	if err != nil || len(defs) != 1 || defs[0].Sinks[0].Aster.TokenFile != "/worker-only-mount/token" {
		t.Fatalf("durable resource: %+v %v", defs, err)
	}
}
func TestConnectSimulationRunsUnsavedDraftWithoutDelivery(t *testing.T) {
	for _, match := range []bool{true, false} {
		t.Run(map[bool]string{true: "pass", false: "drop"}[match], func(t *testing.T) {
			client, ctrl, _ := newTestClient()
			def := basicDefinition()
			def.Stages = []spec.StageSpec{{ID: "selected", Kind: "filter", Type: "cel", Expression: `fields["app"] == "payments"`}}
			def.Sinks = []spec.SinkCfg{{Type: "http", URL: "http://nonexistent.invalid"}}
			def.DeadLetterSink = &spec.SinkCfg{Type: "stdout", When: `meta["droppedBy"] == "selected"`}
			raw := `{"app":"other"}`
			if match {
				raw = `{"app":"payments"}`
			}
			response, err := client.Simulate(context.Background(), connect.NewRequest(&gofiv1.SimulateRequest{Flow: flowMessage(t, def), Raw: raw}))
			if err != nil {
				t.Fatal(err)
			}
			result := response.Msg.Result
			if result.Dropped == match {
				t.Fatalf("unexpected simulation result: %+v", result)
			}
			if match && (len(result.Sinks) != 1 || !result.Sinks[0].Sent) {
				t.Fatalf("passing sink: %+v", result)
			}
			if !match && (!result.DeadLetter || result.DroppedBy != "selected") {
				t.Fatalf("dead letter result: %+v", result)
			}
			if len(ctrl.List()) != 0 {
				t.Fatal("simulation registered a listener")
			}
		})
	}
}
func TestConnectRejectsMalformedOrMissingRequests(t *testing.T) {
	client, _, handler := newTestClient()
	if _, err := client.UpsertFlow(context.Background(), connect.NewRequest(&gofiv1.UpsertFlowRequest{})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("missing listener: %v", err)
	}
	if _, err := client.Simulate(context.Background(), connect.NewRequest(&gofiv1.SimulateRequest{})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("missing simulate listener: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, gofiv1connect.GoFiServiceSimulateProcedure, bytes.NewBufferString(`{not json`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed Connect body: %d %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response["code"] != "invalid_argument" {
		t.Fatalf("Connect error envelope: %+v %v", response, err)
	}
}
func TestHealthSPARoutesAndRetiredREST(t *testing.T) {
	ctrl := controller.New(driver.NewInProcess())
	handler := New(ctrl, fstest.MapFS{"index.html": {Data: []byte("portal shell")}, "assets/test.js": {Data: []byte("asset")}}, deploymode.ModeBinary)
	// /readyz is 503 here: the controller never ran a reconcile pass.
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 503, "/login": 200, "/assets/test.js": 200, "/assets/missing.js": 404, "/api/listeners": 404, "/api/stream": 404} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != want {
			t.Errorf("%s status %d, want %d", path, recorder.Code, want)
		}
		if path == "/login" {
			body, _ := io.ReadAll(recorder.Result().Body)
			if string(body) != "portal shell" {
				t.Fatalf("login did not render portal shell: %q", body)
			}
		}
	}
}

func TestValidateOnlyChecksWithoutSaving(t *testing.T) {
	client, ctrl, _ := newTestClient()
	ctx := context.Background()
	def := basicDefinition()
	if _, err := client.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, def), ValidateOnly: true})); err != nil {
		t.Fatal(err)
	}
	if len(ctrl.List()) != 0 {
		t.Fatal("validation saved the flow")
	}
	def.Sinks = []spec.SinkCfg{{Type: "http", URL: "https://receiver.example", Encoding: &spec.Encoding{Encoders: []spec.CodecStep{{Type: "bzip2"}}}}}
	if _, err := client.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, def), ValidateOnly: true})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("an invalid flow fails validation: %v", err)
	}
}

func TestInlinePythonIsShownAsCodeNotItsManagedLibrary(t *testing.T) {
	client, ctrl, _ := newTestClient()
	ctx := context.Background()
	def := basicDefinition()
	def.Stages = []spec.StageSpec{{ID: "enrich", Kind: "python", Python: &spec.PythonRef{InlineSource: "def transform(fields, meta):\n    return fields\n"}}}
	if _, err := client.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, def)})); err != nil {
		t.Fatal(err)
	}
	// The controller runs it through its managed library...
	if running, _ := ctrl.Get(def.Name); running.Stages[0].Python.Library == "" {
		t.Fatal("the controller resolves inline stages to their managed library")
	}
	// ...but people only see the code.
	got, err := client.GetFlow(ctx, connect.NewRequest(&gofiv1.GetFlowRequest{Name: def.Name}))
	if err != nil {
		t.Fatal(err)
	}
	python := got.Msg.Flow.Stages[0].Python
	if python.Library != "" || python.Function != "" || python.InlineSource == "" {
		t.Fatalf("inline stage leaked its managed library: %+v", python)
	}
	list, _ := client.ListFlows(ctx, connect.NewRequest(&gofiv1.ListFlowsRequest{}))
	if list.Msg.Flows[0].Stages[0].Python.Library != "" {
		t.Fatal("list leaked the managed library")
	}
}

// TestReadyzFollowsControllerReadiness: /readyz is 503 until the controller
// has completed its first reconcile pass, 200 after it, and 503 again while
// the runtime driver does not answer; /healthz is liveness only and stays 200.
func TestReadyzFollowsControllerReadiness(t *testing.T) {
	d := &probeDriver{Driver: driver.NewInProcess()}
	ctrl := controller.New(d)
	handler := New(ctrl, nil, deploymode.ModeBinary)
	probe := func(path string) (int, string) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		body, _ := io.ReadAll(recorder.Result().Body)
		return recorder.Code, string(body)
	}
	if code, body := probe("/readyz"); code != http.StatusServiceUnavailable || !strings.Contains(body, "first reconcile pass") {
		t.Fatalf("before the first pass: %d %q", code, body)
	}
	if code, _ := probe("/healthz"); code != http.StatusOK {
		t.Fatal("healthz must be liveness only")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ctrl.Run(ctx)
	waitForCode(t, probe, http.StatusOK)

	d.listErr.Store(true)
	ctrl.Enqueue("anything") // single-flow items do not List; force a full pass
	if err := ctrl.Add(basicDefinition()); err != nil {
		t.Fatal(err)
	}
	waitForCode(t, probe, http.StatusServiceUnavailable)
	if _, body := probe("/readyz"); !strings.Contains(body, "runtime driver unreachable") {
		t.Fatalf("reason: %q", body)
	}
	d.listErr.Store(false)
	def := basicDefinition()
	def.Framing.Mode = "raw"
	if err := ctrl.Add(def); err != nil { // another save triggers a full pass
		t.Fatal(err)
	}
	waitForCode(t, probe, http.StatusOK)
	_ = driver.NewInProcess().DrainAll(ctx)
}

func waitForCode(t *testing.T, probe func(string) (int, string), want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body := probe("/readyz")
		if code == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("/readyz = %d %q, want %d", code, body, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// probeDriver is the in-process driver with a switch that makes List fail,
// as a Docker daemon that stopped answering would.
type probeDriver struct {
	driver.Driver
	listErr atomic.Bool
}

func (d *probeDriver) List(ctx context.Context) ([]spec.FlowDefinition, error) {
	if d.listErr.Load() {
		return nil, errors.New("Cannot connect to the Docker daemon")
	}
	return d.Driver.List(ctx)
}
