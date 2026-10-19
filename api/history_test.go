package api

import (
	"context"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/history"
)

func TestFlowHistoryRecordsSavesAndRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ctrl := controller.New(driver.NewInProcess())
	if err := ctrl.WithStore(ctx, &desired.File{Path: filepath.Join(dir, "flows.yaml")}); err != nil {
		t.Fatal(err)
	}
	rec := history.NewRecorder(&history.File{Dir: filepath.Join(dir, "history")}, ctrl, history.Options{})
	handler := NewWithHistory(ctrl, nil, deploymode.ModeBinary, rec)
	flows := gofiv1connect.NewGoFiServiceClient(handlerClient{handler}, "http://gofi.test")
	revisions := gofiv1connect.NewFlowHistoryServiceClient(handlerClient{handler}, "http://gofi.test")

	save := func(port int, message string) {
		t.Helper()
		def := basicDefinition()
		def.Source.Listen.Port = port
		req := connect.NewRequest(&gofiv1.UpsertFlowRequest{Flow: flowMessage(t, def), Message: message})
		req.Header().Set("X-Forwarded-User", "ada")
		if _, err := flows.UpsertFlow(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	save(9000, "first")
	save(9000, "no change") // same content: no revision
	save(9100, "move port")

	list, err := revisions.ListFlowRevisions(ctx, connect.NewRequest(&gofiv1.ListFlowRevisionsRequest{Flow: "route"}))
	if err != nil {
		t.Fatal(err)
	}
	revs := list.Msg.Revisions
	if len(revs) != 2 || revs[0].Revision != 2 || revs[0].Message != "move port" || revs[0].Author != "ada" || revs[0].Source != "portal" || revs[0].Definition != nil || list.Msg.RetentionLimit != history.DefaultLimit {
		t.Fatalf("list: %+v", list.Msg)
	}
	got, err := revisions.GetFlowRevision(ctx, connect.NewRequest(&gofiv1.GetFlowRevisionRequest{Flow: "route", Revision: 1}))
	if err != nil || got.Msg.Revision.Definition.GetSource().GetListen().GetPort() != 9000 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err = revisions.GetFlowRevision(ctx, connect.NewRequest(&gofiv1.GetFlowRevisionRequest{Flow: "route", Revision: 99})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing revision: %v", err)
	}

	rollback, err := revisions.RollbackFlow(ctx, connect.NewRequest(&gofiv1.RollbackFlowRequest{Flow: "route", Revision: 1}))
	if err != nil || !rollback.Msg.Created || rollback.Msg.Revision.Revision != 3 || rollback.Msg.Revision.Source != "rollback" || rollback.Msg.Revision.Author != "unknown" {
		t.Fatalf("rollback: %+v %v", rollback, err)
	}
	if def, _ := ctrl.Get("route"); def.Source.Listen.Port != 9000 {
		t.Fatalf("not restored: %+v", def)
	}

	// Deleting keeps the history and records a tombstone.
	if _, err = flows.DeleteFlow(ctx, connect.NewRequest(&gofiv1.DeleteFlowRequest{Name: "route"})); err != nil {
		t.Fatal(err)
	}
	list, err = revisions.ListFlowRevisions(ctx, connect.NewRequest(&gofiv1.ListFlowRevisionsRequest{Flow: "route", Limit: 1}))
	if err != nil || len(list.Msg.Revisions) != 1 || !list.Msg.Revisions[0].Deleted {
		t.Fatalf("tombstone: %+v %v", list, err)
	}
}
