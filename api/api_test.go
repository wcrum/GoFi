package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

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
func listenerMessage(t *testing.T, def spec.ListenerDefinition) *gofiv1.Listener {
	t.Helper()
	value := &gofiv1.Listener{}
	if err := toProto(def, value); err != nil {
		t.Fatal(err)
	}
	return value
}
func basicDefinition() spec.ListenerDefinition {
	return spec.ListenerDefinition{Name: "route", Source: spec.Source{Port: 9000, Network: "tcp"}, Framing: spec.Framing{Mode: "line"}, Parser: spec.ParserCfg{Format: "json"}}
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
	def.Parser.Decoders = []string{"base64"}
	def.Parser.ErrorPolicy = "drop"
	def.Stages = []spec.StageSpec{{ID: "app", Kind: "filter", Type: "regex", Field: "app", Expression: "^payments$"}, {ID: "status", Kind: "filter", Type: "cel", Expression: `fields["status"] >= 200`}}
	def.Sinks = []spec.SinkCfg{{Type: "http", URL: "https://receiver.example/ingest", Headers: map[string]string{"X-App": "GoFi"}, Auth: &spec.HTTPAuth{Type: "apiKey", HeaderName: "X-Token", APIKey: "example"}, When: `fields["app"] == "payments"`}}
	def.DeadLetterSink = &spec.SinkCfg{Type: "stdout", When: `meta["droppedBy"] != ""`}
	def.TestCases = []spec.TestCase{{ID: "sample", Name: "Sample event", Raw: `{"app":"payments"}`}}
	saved, err := client.UpsertListener(ctx, connect.NewRequest(&gofiv1.UpsertListenerRequest{Listener: listenerMessage(t, def)}))
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip spec.ListenerDefinition
	if err := fromProto(saved.Msg.Listener, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(def, roundTrip) {
		t.Fatalf("typed contract lost fields:\nwant %+v\ngot %+v", def, roundTrip)
	}
	got, err := client.GetListener(ctx, connect.NewRequest(&gofiv1.GetListenerRequest{Name: def.Name}))
	if err != nil || len(got.Msg.Listener.Stages) != 2 || got.Msg.Listener.Stages[1].Expression != def.Stages[1].Expression {
		t.Fatalf("get: %v %v", got, err)
	}
	list, err := client.ListListeners(ctx, connect.NewRequest(&gofiv1.ListListenersRequest{}))
	if err != nil || len(list.Msg.Listeners) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if _, err := client.GetRuntimeStatus(ctx, connect.NewRequest(&gofiv1.GetRuntimeStatusRequest{Name: def.Name})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("runtime not yet reconciled must be NotFound: %v", err)
	}
	if _, err := client.DeleteListener(ctx, connect.NewRequest(&gofiv1.DeleteListenerRequest{Name: def.Name})); err != nil {
		t.Fatal(err)
	}
	if len(ctrl.List()) != 0 {
		t.Fatal("delete retained desired resource")
	}
	if _, err := client.GetListener(ctx, connect.NewRequest(&gofiv1.GetListenerRequest{Name: def.Name})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("deleted resource get: %v", err)
	}
}
func TestConnectRejectsInvalidConfigurationBeforePersistence(t *testing.T) {
	invalid := map[string]func(*spec.ListenerDefinition){
		"empty name":            func(d *spec.ListenerDefinition) { d.Name = "" },
		"invalid resource name": func(d *spec.ListenerDefinition) { d.Name = "has spaces" },
		"missing port":          func(d *spec.ListenerDefinition) { d.Source.Port = 0 },
		"negative port":         func(d *spec.ListenerDefinition) { d.Source.Port = -1 },
		"oversized port":        func(d *spec.ListenerDefinition) { d.Source.Port = 65536 },
		"invalid network":       func(d *spec.ListenerDefinition) { d.Source.Network = "sctp" },
		"binary ClusterIP":      func(d *spec.ListenerDefinition) { d.Source.Service = &spec.ServiceExposure{Type: "ClusterIP"} },
		"unknown parser":        func(d *spec.ListenerDefinition) { d.Parser.Format = "unknown" },
		"invalid decoder":       func(d *spec.ListenerDefinition) { d.Parser.Decoders = []string{"unknown"} },
		"invalid framing":       func(d *spec.ListenerDefinition) { d.Framing.Mode = "unknown" },
		"duplicate stage IDs": func(d *spec.ListenerDefinition) {
			d.Stages = []spec.StageSpec{{ID: "same", Kind: "filter", Type: "cel", Expression: "true"}, {ID: "same", Kind: "filter", Type: "cel", Expression: "false"}}
		},
		"unknown stage": func(d *spec.ListenerDefinition) {
			d.Stages = []spec.StageSpec{{ID: "bad", Kind: "filter", Type: "unknown"}}
		},
		"invalid sink type":    func(d *spec.ListenerDefinition) { d.Sinks = []spec.SinkCfg{{Type: "unknown"}} },
		"invalid sink address": func(d *spec.ListenerDefinition) { d.Sinks = []spec.SinkCfg{{Type: "tcp", URL: "tcp://receiver"}} },
		"invalid HTTP address": func(d *spec.ListenerDefinition) { d.Sinks = []spec.SinkCfg{{Type: "http", URL: "relative"}} },
		"invalid HTTP method": func(d *spec.ListenerDefinition) {
			d.Sinks = []spec.SinkCfg{{Type: "http", URL: "https://receiver.example", Method: "INVALID"}}
		},
		"invalid sink condition": func(d *spec.ListenerDefinition) { d.Sinks = []spec.SinkCfg{{Type: "stdout", When: "invalid syntax !"}} },
		"invalid dead letter": func(d *spec.ListenerDefinition) {
			d.DeadLetterSink = &spec.SinkCfg{Type: "stdout", When: "invalid syntax !"}
		},
		"invalid python sink": func(d *spec.ListenerDefinition) {
			d.Sinks = []spec.SinkCfg{{Type: "python", Python: &spec.PythonRef{Library: "library"}}}
		},
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			client, ctrl, _ := newTestClient()
			def := basicDefinition()
			mutate(&def)
			_, err := client.UpsertListener(context.Background(), connect.NewRequest(&gofiv1.UpsertListenerRequest{Listener: listenerMessage(t, def)}))
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
	if _, err := client.UpsertListener(context.Background(), connect.NewRequest(&gofiv1.UpsertListenerRequest{Listener: listenerMessage(t, def)})); err != nil {
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
			response, err := client.Simulate(context.Background(), connect.NewRequest(&gofiv1.SimulateRequest{Listener: listenerMessage(t, def), Raw: raw}))
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
	if _, err := client.UpsertListener(context.Background(), connect.NewRequest(&gofiv1.UpsertListenerRequest{})); connect.CodeOf(err) != connect.CodeInvalidArgument {
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
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 200, "/login": 200, "/assets/test.js": 200, "/assets/missing.js": 404, "/api/listeners": 404, "/api/stream": 404} {
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
