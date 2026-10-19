package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/observability"
)

func TestOperationsStopStartAndBulletins(t *testing.T) {
	client, _, handler := newTestClient()
	ops := gofiv1connect.NewOperationsServiceClient(handlerClient{handler}, "http://gofi.test")
	ctx := context.Background()
	def := basicDefinition()
	def.Name = "ops-flow"
	if _, err := client.UpsertFlow(ctx, connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, def)})); err != nil {
		t.Fatal(err)
	}
	stopped, err := ops.StopFlow(ctx, connect.NewRequest(&gofiv1.StopFlowRequest{Name: def.Name}))
	if err != nil || stopped.Msg.Status.Desired != "stopped" {
		t.Fatalf("stop: %v %v", stopped, err)
	}
	got, err := client.GetFlow(ctx, connect.NewRequest(&gofiv1.GetFlowRequest{Name: def.Name}))
	if err != nil || got.Msg.Flow.State != "stopped" {
		t.Fatalf("stored state: %v %v", got, err)
	}
	if _, err := ops.RestartFlow(ctx, connect.NewRequest(&gofiv1.RestartFlowRequest{Name: def.Name})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("restart of a stopped flow: %v", err)
	}
	started, err := ops.StartFlow(ctx, connect.NewRequest(&gofiv1.StartFlowRequest{Name: def.Name}))
	if err != nil || started.Msg.Status.Desired != "running" {
		t.Fatalf("start: %v %v", started, err)
	}
	if _, err := ops.StopFlow(ctx, connect.NewRequest(&gofiv1.StopFlowRequest{Name: "missing"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing flow: %v", err)
	}

	observability.ClearBulletins(def.Name)
	observability.Report(def.Name, observability.SeverityError, "sink:0", "connection refused")
	observability.Report(def.Name, observability.SeverityWarning, observability.ComponentParser, "bad json")
	list, err := ops.ListBulletins(ctx, connect.NewRequest(&gofiv1.ListBulletinsRequest{Flow: def.Name}))
	if err != nil || len(list.Msg.Bulletins) != 2 {
		t.Fatalf("bulletins: %v %v", list, err)
	}
	status, err := ops.ListFlowStatus(ctx, connect.NewRequest(&gofiv1.ListFlowStatusRequest{}))
	if err != nil || status.Msg.Problems != 2 || len(status.Msg.Flows) != 1 || status.Msg.Flows[0].Errors != 1 {
		t.Fatalf("status: %v %v", status, err)
	}
	sinkID := observability.BulletinID(def.Name, "sink:0", "connection refused")
	if _, err := ops.AcknowledgeBulletins(ctx, connect.NewRequest(&gofiv1.AcknowledgeBulletinsRequest{Flow: def.Name, Ids: []string{sinkID}})); err != nil {
		t.Fatal(err)
	}
	open, _ := ops.ListBulletins(ctx, connect.NewRequest(&gofiv1.ListBulletinsRequest{Flow: def.Name}))
	all, _ := ops.ListBulletins(ctx, connect.NewRequest(&gofiv1.ListBulletinsRequest{Flow: def.Name, IncludeAcknowledged: true}))
	if len(open.Msg.Bulletins) != 1 || len(all.Msg.Bulletins) != 2 {
		t.Fatalf("after acknowledge: open %d, all %d", len(open.Msg.Bulletins), len(all.Msg.Bulletins))
	}
	if _, err := ops.ClearBulletins(ctx, connect.NewRequest(&gofiv1.ClearBulletinsRequest{Flow: def.Name})); err != nil {
		t.Fatal(err)
	}
	all, _ = ops.ListBulletins(ctx, connect.NewRequest(&gofiv1.ListBulletinsRequest{Flow: def.Name, IncludeAcknowledged: true}))
	if len(all.Msg.Bulletins) != 0 {
		t.Fatalf("after clear: %v", all.Msg.Bulletins)
	}
}
