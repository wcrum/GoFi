package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/wcrum/gofi/functionrpc"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

// PythonWorker is the worker RPC surface a source.python uses. The flow
// runtime only calls the library's worker; the function itself (and its
// network access, under the library's egress allowlist) runs there.
type PythonWorker interface {
	// Describe returns the function's kind ("fetcher" or "streamer") and a
	// fetcher's decorator interval.
	Describe(ctx context.Context, library, function string) (kind, every string, err error)
	Fetch(ctx context.Context, library string, req *gofiv1.WorkerSourceRequest) (*gofiv1.WorkerFetchResponse, error)
	// Stream calls recv for each message until the stream ends or ctx ends.
	Stream(ctx context.Context, library string, req *gofiv1.WorkerSourceRequest, recv func(*gofiv1.WorkerStreamResponse) error) error
}

// pythonWorker is replaced in tests.
var pythonWorker PythonWorker = rpcWorker{}

// pythonDescribeBackoff bounds retries while the library's worker is not
// ready or does not (yet) register the function.
var pythonDescribeBackoff = struct{ min, max time.Duration }{time.Second, 30 * time.Second}

// MaxPythonStateBytes bounds ctx.state carried between calls.
const MaxPythonStateBytes = 64 << 10

func pythonOrigin(p *spec.PythonSource) string { return "python:" + p.Library + "." + p.Function }

// runPython resolves the function's kind from the library's registry, then
// polls a fetcher (pollLoop: no overlapping polls) or keeps a streamer
// connected (reconnect: capped exponential backoff).
func runPython(ctx context.Context, flow string, p *spec.PythonSource, emit Emit) error {
	kind, every, ok := describePython(ctx, flow, p)
	if !ok {
		return nil
	}
	run := &pythonRun{flow: flow, cfg: p, emit: emit}
	if kind == spec.KindStreamer {
		slog.Info("Python streamer source started", "flow", flow, "function", pythonOrigin(p))
		return reconnect(ctx, flow, "python", run.stream)
	}
	interval := p.PollEvery(every)
	slog.Info("Python fetcher source started", "flow", flow, "function", pythonOrigin(p), "every", interval)
	return pollLoop(ctx, flow, "python", pythonOrigin(p), interval, run.fetch)
}

func describePython(ctx context.Context, flow string, p *spec.PythonSource) (kind, every string, ok bool) {
	delay := pythonDescribeBackoff.min
	for ctx.Err() == nil {
		var err error
		kind, every, err = pythonWorker.Describe(ctx, p.Library, p.Function)
		if err == nil {
			err = spec.CheckSourceKind(p.Library, p.Function, kind)
		}
		if err == nil {
			return kind, every, true
		}
		if ctx.Err() != nil {
			break
		}
		observability.SourceEvent(flow, "python", "error")
		observability.SourceFailure(flow, err)
		slog.Warn("Python source not available; retrying", "flow", flow, "function", pythonOrigin(p), "error", err, "retryIn", delay)
		select {
		case <-ctx.Done():
			return "", "", false
		case <-time.After(delay):
		}
		delay = min(delay*2, pythonDescribeBackoff.max)
	}
	return "", "", false
}

// pythonRun keeps the function's ctx.state between polls and across stream
// reconnects (in memory: a runtime restart starts from an empty state).
type pythonRun struct {
	flow  string
	cfg   *spec.PythonSource
	emit  Emit
	state map[string]any
}

func (r *pythonRun) request() (*gofiv1.WorkerSourceRequest, error) {
	req := &gofiv1.WorkerSourceRequest{Name: r.cfg.Function, Flow: r.flow, InvocationId: functionrpc.NewInvocationID()}
	var err error
	if len(r.cfg.Config) > 0 {
		if req.Config, err = structpb.NewStruct(r.cfg.Config); err != nil {
			return nil, fmt.Errorf("source.python.config: %w", err)
		}
	}
	if len(r.state) > 0 {
		if req.State, err = structpb.NewStruct(r.state); err != nil {
			return nil, fmt.Errorf("python source state: %w", err)
		}
	}
	return req, nil
}

func (r *pythonRun) keepState(state *structpb.Struct) {
	if state == nil {
		return
	}
	if size := len(state.String()); size > MaxPythonStateBytes {
		slog.Warn("Python source state too large; keeping the previous state", "flow", r.flow, "function", pythonOrigin(r.cfg), "bytes", size)
		return
	}
	r.state = state.AsMap()
}

func (r *pythonRun) emitFrames(ctx context.Context, frames []*gofiv1.SourceFrame) error {
	origin := pythonOrigin(r.cfg)
	for _, frame := range frames {
		if len(frame.Data) > MaxMessageBytes {
			return fmt.Errorf("python source frame exceeds %d bytes", MaxMessageBytes)
		}
	}
	for _, frame := range frames {
		r.emit(message.WithSourceMeta(ctx, frame.Meta), frame.Data, origin)
	}
	return nil
}

// fetch is one poll: a user-code error fails the poll (counted, logged,
// retried at the next interval) and emits nothing.
func (r *pythonRun) fetch(ctx context.Context) error {
	req, err := r.request()
	if err != nil {
		return err
	}
	res, err := pythonWorker.Fetch(ctx, r.cfg.Library, req)
	if err != nil {
		return err
	}
	if res.Error != "" {
		location := ""
		if res.ErrorLocation != nil && res.ErrorLocation.Line > 0 {
			location = fmt.Sprintf(" (line %d)", res.ErrorLocation.Line)
		}
		return fmt.Errorf("%s: %s%s", pythonOrigin(r.cfg), res.Error, location)
	}
	r.keepState(res.State)
	return r.emitFrames(ctx, res.Frames)
}

// stream holds one streamer call open; it returns when the streamer ends or
// fails, and reconnect opens it again with the latest state.
func (r *pythonRun) stream(ctx context.Context) error {
	req, err := r.request()
	if err != nil {
		return err
	}
	err = pythonWorker.Stream(ctx, r.cfg.Library, req, func(msg *gofiv1.WorkerStreamResponse) error {
		r.keepState(msg.State)
		if len(msg.Frames) == 0 {
			return nil
		}
		observability.SourceEvent(r.flow, "python", "ok")
		return r.emitFrames(ctx, msg.Frames)
	})
	if err == nil {
		err = errors.New("streamer ended")
	}
	return err
}

// rpcWorker reaches the library's worker through its binding (resolved like
// every other Python step: in-process supervisor, or GOFI_PYTHON_BINDINGS
// in a Docker/Kubernetes flow runtime).
type rpcWorker struct{}

func (rpcWorker) Describe(ctx context.Context, library, function string) (string, string, error) {
	binding, err := functionrpc.LibraryBinding(ctx, library)
	if err != nil {
		return "", "", err
	}
	functions, err := functionrpc.ListFunctions(ctx, binding)
	if err != nil {
		return "", "", err
	}
	for _, f := range functions {
		if f.Name == function && (f.Kind == spec.KindFetcher || f.Kind == spec.KindStreamer) {
			return f.Kind, f.Every, nil
		}
	}
	for _, f := range functions {
		if f.Name == function {
			return f.Kind, "", nil
		}
	}
	return "", "", fmt.Errorf("library %q registers no fetcher or streamer named %q", library, function)
}

func (rpcWorker) Fetch(ctx context.Context, library string, req *gofiv1.WorkerSourceRequest) (*gofiv1.WorkerFetchResponse, error) {
	binding, err := functionrpc.LibraryBinding(ctx, library)
	if err != nil {
		return nil, err
	}
	return functionrpc.Fetch(ctx, binding, req)
}

func (rpcWorker) Stream(ctx context.Context, library string, req *gofiv1.WorkerSourceRequest, recv func(*gofiv1.WorkerStreamResponse) error) error {
	binding, err := functionrpc.LibraryBinding(ctx, library)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // closes the call; the worker then stops the streamer
	stream, err := functionrpc.Stream(ctx, binding, req)
	if err != nil {
		return err
	}
	defer stream.Close()
	for stream.Receive() {
		if err := recv(stream.Msg()); err != nil {
			return err
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
