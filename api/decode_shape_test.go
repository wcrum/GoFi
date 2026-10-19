package api

import (
	"context"
	"reflect"
	"testing"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/spec"
)

// Older clients still send parser.decoders (deprecated proto fields); the
// flow is stored and returned with the decode step instead. Clients that set
// both locations with different values are rejected.
func TestUpsertFlowAcceptsLegacyParserDecoders(t *testing.T) {
	client, ctrl, _ := newTestClient()
	ctx := context.Background()
	flow := flowMessage(t, basicDefinition())
	flow.Parser.Decoders = []*gofiv1.CodecStep{{Type: "base64"}} //nolint:staticcheck // the deprecated field is the point
	flow.Parser.MaxDecodedBytes = 2048                           //nolint:staticcheck
	if _, err := client.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flow})); err != nil {
		t.Fatal(err)
	}
	if def, _ := ctrl.Get("route"); def.Decode == nil || def.Decode.Decoders[0].Type != "base64" || def.Decode.MaxDecodedBytes != 2048 || def.Parser.LegacyDecoders != nil {
		t.Fatalf("stored = %+v", def)
	}
	got, err := client.GetFlow(ctx, connect.NewRequest(&gofiv1.GetFlowRequest{Name: "route"}))
	if err != nil {
		t.Fatal(err)
	}
	if f := got.Msg.Flow; len(f.Parser.Decoders) != 0 || f.Parser.MaxDecodedBytes != 0 || f.Decode == nil || f.Decode.Decoders[0].Type != "base64" || f.Decode.MaxDecodedBytes != 2048 { //nolint:staticcheck
		t.Fatalf("returned = %+v", f)
	}
	flow.Decode = &gofiv1.DecodeConfig{Decoders: []*gofiv1.CodecStep{{Type: "hex"}}}
	if _, err := client.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flow})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("conflicting decode accepted: %v", err)
	}
	// Simulation reads the legacy shape too.
	flow.Decode = nil
	sim, err := client.Simulate(ctx, connect.NewRequest(&gofiv1.SimulateRequest{Flow: flow, Raw: "eyJhIjoiYiJ9"}))
	if err != nil || sim.Msg.Result.ParsedFields.AsMap()["a"] != "b" {
		t.Fatalf("simulate: %+v %v", sim, err)
	}
}

// The new fields survive the protobuf bridge unchanged.
func TestDecodePythonAndParseStageRoundTrip(t *testing.T) {
	client, _, _ := newTestClient()
	ctx := context.Background()
	def := basicDefinition()
	def.Decode = &spec.DecodeCfg{Decoders: []spec.CodecStep{{Type: "base64"}}, MaxDecodedBytes: 4096}
	def.Python = &spec.FlowPython{Library: "shared"}
	def.Stages = []spec.StageSpec{{ID: "inner", Kind: "parse", Parse: &spec.ParseStage{From: `fields["blob"]`, Decoders: []spec.CodecStep{{Type: "base64"}, {Type: "gzip"}}, Format: "json", Into: "payload", Mode: "merge", OnError: "drop"}}}
	saved, err := client.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, def)}))
	if err != nil {
		t.Fatal(err)
	}
	var back spec.FlowDefinition
	if err := fromProto(saved.Msg.Flow, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(def, back) {
		t.Fatalf("round trip:\nwant %+v\ngot  %+v", def, back)
	}
}
