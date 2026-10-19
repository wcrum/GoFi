package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/history"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
	"github.com/wcrum/gofi/tapstream"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Polling bounds. Every browser reads the controller's cache; the cache
// reads each flow runtime at most once per TTL, however many people watch.
const (
	bulletinTTL       = 5 * time.Second
	statusTTL         = 3 * time.Second
	runtimeRPCLimit   = 8 // concurrent runtime reads per refresh
	runtimeRPCTimeout = 2 * time.Second
)

// operationsHandler serves OperationsService. Bulletins are collected from
// the controller's own board (start failures, crashes; and every runtime
// problem for in-process flows) and from each Docker/Kubernetes runtime's
// relay, merged by bulletin ID.
type operationsHandler struct {
	gofiv1connect.UnimplementedOperationsServiceHandler
	ctrl   *controller.Controller
	client *http.Client
	// history is kept for future attributed operations (nil: off). Start and
	// Stop change run state only and record no revision.
	history *history.Recorder

	mu        sync.Mutex
	bulletins map[string]*cacheEntry[[]observability.Bulletin]
	statuses  map[string]*cacheEntry[controller.FlowStatus]
	// acked and cleared remember, per flow and bulletin ID, the bulletin's
	// last-seen time when people acknowledged or cleared it. A bulletin seen
	// again later is open again. The runtime's own clock stamps both sides.
	acked   map[string]map[string]time.Time
	cleared map[string]map[string]time.Time
}

type cacheEntry[T any] struct {
	mu    sync.Mutex // held while refreshing, so concurrent readers share one fetch
	at    time.Time
	value T
}

func newOperationsHandler(ctrl *controller.Controller, rec *history.Recorder) *operationsHandler {
	transport := &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second}
	return &operationsHandler{ctrl: ctrl, history: rec, client: &http.Client{Transport: transport, Timeout: runtimeRPCTimeout},
		bulletins: map[string]*cacheEntry[[]observability.Bulletin]{}, statuses: map[string]*cacheEntry[controller.FlowStatus]{},
		acked: map[string]map[string]time.Time{}, cleared: map[string]map[string]time.Time{}}
}

func operationError(err error) error {
	if errors.Is(err, controller.ErrFlowNotFound) {
		return rpcError(connect.CodeNotFound, err)
	}
	if errors.As(err, new(controller.InvalidInlineError)) {
		return rpcError(connect.CodeInvalidArgument, err)
	}
	return rpcError(connect.CodeFailedPrecondition, err)
}

func (h *operationsHandler) StartFlow(ctx context.Context, req *connect.Request[gofiv1.StartFlowRequest]) (*connect.Response[gofiv1.StartFlowResponse], error) {
	// Run state is operational, not a definition change: no revision.
	if err := h.ctrl.SetState(req.Msg.Name, spec.StateRunning); err != nil {
		return nil, operationError(err)
	}
	return connect.NewResponse(&gofiv1.StartFlowResponse{Status: h.freshStatus(ctx, req.Msg.Name)}), nil
}

func (h *operationsHandler) StopFlow(ctx context.Context, req *connect.Request[gofiv1.StopFlowRequest]) (*connect.Response[gofiv1.StopFlowResponse], error) {
	if err := h.ctrl.SetState(req.Msg.Name, spec.StateStopped); err != nil {
		return nil, operationError(err)
	}
	return connect.NewResponse(&gofiv1.StopFlowResponse{Status: h.freshStatus(ctx, req.Msg.Name)}), nil
}

func (h *operationsHandler) RestartFlow(ctx context.Context, req *connect.Request[gofiv1.RestartFlowRequest]) (*connect.Response[gofiv1.RestartFlowResponse], error) {
	if err := h.ctrl.Restart(ctx, req.Msg.Name); err != nil {
		return nil, operationError(err)
	}
	return connect.NewResponse(&gofiv1.RestartFlowResponse{Status: h.freshStatus(ctx, req.Msg.Name)}), nil
}

func (h *operationsHandler) ListFlowStatus(ctx context.Context, req *connect.Request[gofiv1.ListFlowStatusRequest]) (*connect.Response[gofiv1.ListFlowStatusResponse], error) {
	names := h.flowNames(req.Msg.Name)
	if req.Msg.Name != "" && len(names) == 0 {
		return nil, rpcError(connect.CodeNotFound, controller.ErrFlowNotFound)
	}
	statuses := make([]*gofiv1.FlowStatus, len(names))
	h.each(names, func(i int, name string) {
		status, ok := h.status(ctx, name)
		if !ok {
			return
		}
		item := statusProto(status)
		for _, b := range h.openBulletins(ctx, name, false) {
			switch b.Severity {
			case observability.SeverityError:
				item.Errors++
			case observability.SeverityWarning:
				item.Warnings++
			}
		}
		item.Problems = item.Errors + item.Warnings
		statuses[i] = item
	})
	out := &gofiv1.ListFlowStatusResponse{}
	for _, item := range statuses {
		if item != nil {
			out.Flows = append(out.Flows, item)
			out.Problems += item.Problems
		}
	}
	return connect.NewResponse(out), nil
}

func (h *operationsHandler) ListBulletins(ctx context.Context, req *connect.Request[gofiv1.ListBulletinsRequest]) (*connect.Response[gofiv1.ListBulletinsResponse], error) {
	names := h.flowNames(req.Msg.Flow)
	if req.Msg.Flow != "" && len(names) == 0 {
		return nil, rpcError(connect.CodeNotFound, controller.ErrFlowNotFound)
	}
	perFlow := make([][]*gofiv1.Bulletin, len(names))
	h.each(names, func(i int, name string) {
		perFlow[i] = h.openBulletins(ctx, name, req.Msg.IncludeAcknowledged)
	})
	var all []observability.Bulletin
	acked := map[string]bool{}
	for _, items := range perFlow {
		for _, item := range items {
			all = append(all, tapstream.FromBulletin(item))
			acked[item.Id] = item.Acknowledged
		}
	}
	observability.SortBulletins(all)
	out := &gofiv1.ListBulletinsResponse{}
	for _, b := range all {
		item := tapstream.Bulletin(b)
		item.Acknowledged = acked[b.ID]
		out.Bulletins = append(out.Bulletins, item)
	}
	return connect.NewResponse(out), nil
}

func (h *operationsHandler) AcknowledgeBulletins(ctx context.Context, req *connect.Request[gofiv1.AcknowledgeBulletinsRequest]) (*connect.Response[gofiv1.AcknowledgeBulletinsResponse], error) {
	if err := h.mark(ctx, h.acked, req.Msg.Flow, req.Msg.Ids); err != nil {
		return nil, err
	}
	return connect.NewResponse(&gofiv1.AcknowledgeBulletinsResponse{}), nil
}

func (h *operationsHandler) ClearBulletins(ctx context.Context, req *connect.Request[gofiv1.ClearBulletinsRequest]) (*connect.Response[gofiv1.ClearBulletinsResponse], error) {
	if err := h.mark(ctx, h.cleared, req.Msg.Flow, req.Msg.Ids); err != nil {
		return nil, err
	}
	return connect.NewResponse(&gofiv1.ClearBulletinsResponse{}), nil
}

// mark records the current last-seen time of the flow's bulletins (the given
// IDs, or all of them) in marks.
func (h *operationsHandler) mark(ctx context.Context, marks map[string]map[string]time.Time, flow string, ids []string) error {
	if _, ok := h.ctrl.Get(flow); !ok {
		return rpcError(connect.CodeNotFound, controller.ErrFlowNotFound)
	}
	current := h.flowBulletins(ctx, flow)
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if marks[flow] == nil {
		marks[flow] = map[string]time.Time{}
	}
	for _, b := range current {
		if len(ids) == 0 || wanted[b.ID] {
			marks[flow][b.ID] = b.LastSeen
		}
	}
	return nil
}

// openBulletins is a flow's bulletins without cleared ones, and without
// acknowledged ones unless includeAcknowledged.
func (h *operationsHandler) openBulletins(ctx context.Context, flow string, includeAcknowledged bool) []*gofiv1.Bulletin {
	items := h.flowBulletins(ctx, flow)
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*gofiv1.Bulletin
	for _, b := range items {
		if at, ok := h.cleared[flow][b.ID]; ok && !b.LastSeen.After(at) {
			continue
		}
		at, ok := h.acked[flow][b.ID]
		acknowledged := ok && !b.LastSeen.After(at)
		if acknowledged && !includeAcknowledged {
			continue
		}
		item := tapstream.Bulletin(b)
		item.Acknowledged = acknowledged
		out = append(out, item)
	}
	return out
}

// flowBulletins returns a flow's merged bulletins, from cache when fresh.
func (h *operationsHandler) flowBulletins(ctx context.Context, flow string) []observability.Bulletin {
	h.mu.Lock()
	entry := h.bulletins[flow]
	if entry == nil {
		entry = &cacheEntry[[]observability.Bulletin]{}
		h.bulletins[flow] = entry
	}
	h.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if time.Since(entry.at) < bulletinTTL {
		return entry.value
	}
	entry.value, entry.at = h.collectBulletins(ctx, flow), time.Now()
	return entry.value
}

// collectBulletins merges this process's board with the flow runtime's
// (Docker/Kubernetes). In-process runtimes share this process's board.
func (h *operationsHandler) collectBulletins(ctx context.Context, flow string) []observability.Bulletin {
	byID := map[string]observability.Bulletin{}
	for _, b := range observability.Bulletins(flow) {
		byID[b.ID] = b
	}
	ctx, cancel := context.WithTimeout(ctx, runtimeRPCTimeout)
	defer cancel()
	if target, found, err := h.ctrl.Tap(ctx, flow); err == nil && found && target.Local == nil && target.Addr != "" {
		remote := gofiv1connect.NewOperationsServiceClient(h.client, "http://"+target.Addr)
		resp, err := remote.ListBulletins(ctx, connect.NewRequest(&gofiv1.ListBulletinsRequest{Flow: flow}))
		if err == nil {
			for _, item := range resp.Msg.Bulletins {
				byID[item.Id] = tapstream.FromBulletin(item)
			}
		}
		// An older runtime (Unimplemented) or one that is going away reports
		// nothing; the controller's own board still shows its crashes.
	}
	out := make([]observability.Bulletin, 0, len(byID))
	for _, b := range byID {
		out = append(out, b)
	}
	observability.SortBulletins(out)
	return out
}

func (h *operationsHandler) status(ctx context.Context, name string) (controller.FlowStatus, bool) {
	h.mu.Lock()
	entry := h.statuses[name]
	if entry == nil {
		entry = &cacheEntry[controller.FlowStatus]{}
		h.statuses[name] = entry
	}
	h.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if time.Since(entry.at) < statusTTL {
		return entry.value, entry.value.Name != ""
	}
	ctx, cancel := context.WithTimeout(ctx, runtimeRPCTimeout)
	defer cancel()
	status, ok := h.ctrl.Status(ctx, name)
	if !ok {
		return status, false
	}
	entry.value, entry.at = status, time.Now()
	return status, true
}

// freshStatus reports a flow's status after an operation, bypassing the cache.
func (h *operationsHandler) freshStatus(ctx context.Context, name string) *gofiv1.FlowStatus {
	h.mu.Lock()
	delete(h.statuses, name)
	h.mu.Unlock()
	status, ok := h.status(ctx, name)
	if !ok {
		return nil
	}
	return statusProto(status)
}

func (h *operationsHandler) flowNames(only string) []string {
	var names []string
	for _, def := range h.ctrl.List() {
		if only == "" || def.Name == only {
			names = append(names, def.Name)
		}
	}
	return names
}

// each runs fn for every name with bounded concurrency.
func (h *operationsHandler) each(names []string, fn func(i int, name string)) {
	limit := make(chan struct{}, runtimeRPCLimit)
	var wg sync.WaitGroup
	for i, name := range names {
		limit <- struct{}{}
		wg.Go(func() {
			defer func() { <-limit }()
			fn(i, name)
		})
	}
	wg.Wait()
}

func statusProto(status controller.FlowStatus) *gofiv1.FlowStatus {
	item := &gofiv1.FlowStatus{Name: status.Name, Desired: status.Desired, Phase: status.Phase, Restarts: int32(status.Restarts),
		RuntimeStatus: status.RuntimeStatus, Message: status.Message}
	if !status.LastRestartAt.IsZero() {
		item.LastRestartAt = timestamppb.New(status.LastRestartAt)
	}
	if !status.StartedAt.IsZero() {
		item.StartedAt = timestamppb.New(status.StartedAt)
	}
	return item
}
