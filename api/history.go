package api

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/history"
	"github.com/wcrum/gofi/spec"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// historyHandler serves FlowHistoryService from the controller's Recorder.
type historyHandler struct {
	gofiv1connect.UnimplementedFlowHistoryServiceHandler
	ctrl *controller.Controller
	rec  *history.Recorder
}

// saveWithHistory runs a controller change inside the recorder (when there
// is one) so the resulting revision is attributed to the request's author.
func saveWithHistory(ctx context.Context, rec *history.Recorder, flow string, header http.Header, message string, change func() error) error {
	if rec == nil {
		return change()
	}
	_, _, err := rec.Apply(ctx, flow, history.Meta{Source: history.SourcePortal, Author: history.AuthorFromHeader(header), Message: message}, change)
	return err
}

func revisionToProto(rev history.Revision) (*gofiv1.FlowRevision, error) {
	out := &gofiv1.FlowRevision{
		Flow: rev.Flow, Revision: rev.Number, CreatedAt: timestamppb.New(rev.CreatedAt), Author: rev.Author,
		Message: rev.Message, Source: string(rev.Source), Hash: rev.Hash, Deleted: rev.Deleted,
	}
	if rev.Definition != nil {
		def := *rev.Definition
		_ = def.Normalize()         // older revisions may carry parser.decoders
		item, err := shownFlow(def) // credentials redacted, as in every read
		if err != nil {
			return nil, err
		}
		out.Definition = item
	}
	return out, nil
}

func historyError(err error) error {
	if errors.Is(err, history.ErrNotFound) {
		return rpcError(connect.CodeNotFound, err)
	}
	return rpcError(connect.CodeInternal, err)
}

func (h *historyHandler) ListFlowRevisions(ctx context.Context, req *connect.Request[gofiv1.ListFlowRevisionsRequest]) (*connect.Response[gofiv1.ListFlowRevisionsResponse], error) {
	if req.Msg.Flow == "" {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("flow is required"))
	}
	revs, err := h.rec.List(ctx, req.Msg.Flow, int(req.Msg.Limit))
	if err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	response := &gofiv1.ListFlowRevisionsResponse{RetentionLimit: int32(h.rec.Limit()), Backend: h.rec.Store().Describe()}
	for _, rev := range revs {
		item, err := revisionToProto(rev)
		if err != nil {
			return nil, rpcError(connect.CodeInternal, err)
		}
		response.Revisions = append(response.Revisions, item)
	}
	return connect.NewResponse(response), nil
}

func (h *historyHandler) GetFlowRevision(ctx context.Context, req *connect.Request[gofiv1.GetFlowRevisionRequest]) (*connect.Response[gofiv1.GetFlowRevisionResponse], error) {
	rev, err := h.rec.Get(ctx, req.Msg.Flow, req.Msg.Revision)
	if err != nil {
		return nil, historyError(err)
	}
	item, err := revisionToProto(rev)
	if err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.GetFlowRevisionResponse{Revision: item}), nil
}

func (h *historyHandler) RollbackFlow(ctx context.Context, req *connect.Request[gofiv1.RollbackFlowRequest]) (*connect.Response[gofiv1.RollbackFlowResponse], error) {
	meta := history.Meta{Author: history.AuthorFromHeader(req.Header()), Message: req.Msg.Message}
	var saveErr error
	rev, created, err := h.rec.Rollback(ctx, req.Msg.Flow, req.Msg.Revision, meta, func(def spec.FlowDefinition) error {
		if err := h.ctrl.Validate(def); err != nil {
			saveErr = rpcError(connect.CodeInvalidArgument, err)
			return saveErr
		}
		if err := h.ctrl.Add(def); err != nil {
			code := connect.CodeInternal
			if errors.As(err, new(controller.InvalidInlineError)) {
				code = connect.CodeInvalidArgument
			}
			saveErr = rpcError(code, err)
			return saveErr
		}
		return nil
	})
	if saveErr != nil {
		return nil, saveErr
	}
	if errors.Is(err, history.ErrNotFound) {
		return nil, historyError(err)
	}
	if err != nil {
		return nil, rpcError(connect.CodeFailedPrecondition, err)
	}
	item, err := revisionToProto(rev)
	if err != nil {
		return nil, rpcError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&gofiv1.RollbackFlowResponse{Revision: item, Created: created}), nil
}
