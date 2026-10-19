package source

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeWorker stands in for a library worker.
type fakeWorker struct {
	kind, every string
	describeErr atomic.Int32 // fail this many Describe calls first

	mu       sync.Mutex
	requests []*gofiv1.WorkerSourceRequest
	fetch    func(n int, req *gofiv1.WorkerSourceRequest) (*gofiv1.WorkerFetchResponse, error)
	stream   func(ctx context.Context, n int, req *gofiv1.WorkerSourceRequest, recv func(*gofiv1.WorkerStreamResponse) error) error
	inFlight atomic.Int32
	overlap  atomic.Bool
}

func (f *fakeWorker) Describe(context.Context, string, string) (string, string, error) {
	if f.describeErr.Add(-1) >= 0 {
		return "", "", errors.New(`Python library "radar" is not ready`)
	}
	return f.kind, f.every, nil
}

func (f *fakeWorker) record(req *gofiv1.WorkerSourceRequest) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	return len(f.requests)
}

func (f *fakeWorker) Fetch(_ context.Context, _ string, req *gofiv1.WorkerSourceRequest) (*gofiv1.WorkerFetchResponse, error) {
	if f.inFlight.Add(1) > 1 {
		f.overlap.Store(true)
	}
	defer f.inFlight.Add(-1)
	return f.fetch(f.record(req), req)
}

func (f *fakeWorker) Stream(ctx context.Context, _ string, req *gofiv1.WorkerSourceRequest, recv func(*gofiv1.WorkerStreamResponse) error) error {
	return f.stream(ctx, f.record(req), req, recv)
}

func useWorker(t *testing.T, w PythonWorker) {
	t.Helper()
	previous, previousBackoff, previousDescribe := pythonWorker, backoff, pythonDescribeBackoff
	pythonWorker = w
	backoff.min, backoff.max = 10*time.Millisecond, 50*time.Millisecond
	pythonDescribeBackoff.min, pythonDescribeBackoff.max = 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { pythonWorker, backoff, pythonDescribeBackoff = previous, previousBackoff, previousDescribe })
}

type pythonFrame struct {
	data, remote string
	meta         map[string]string
}

type pythonCollector struct {
	mu     sync.Mutex
	frames []pythonFrame
	got    chan struct{}
}

func (c *pythonCollector) emit(ctx context.Context, data []byte, remote string) {
	c.mu.Lock()
	c.frames = append(c.frames, pythonFrame{string(data), remote, message.SourceMeta(ctx)})
	c.mu.Unlock()
	c.got <- struct{}{}
}

func (c *pythonCollector) wait(t *testing.T, n int) []pythonFrame {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-c.got:
		case <-time.After(5 * time.Second):
			t.Fatalf("got %d of %d frames: %+v", i, n, c.frames)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pythonFrame(nil), c.frames...)
}

func newPythonCollector() *pythonCollector { return &pythonCollector{got: make(chan struct{}, 100)} }

func mustStruct(t *testing.T, v map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPythonFetcherPollsWithStateAndRetries(t *testing.T) {
	w := &fakeWorker{kind: spec.KindFetcher, every: "1s"}
	w.describeErr.Store(2) // worker not ready at first
	w.fetch = func(n int, req *gofiv1.WorkerSourceRequest) (*gofiv1.WorkerFetchResponse, error) {
		time.Sleep(20 * time.Millisecond)
		switch n {
		case 1:
			return &gofiv1.WorkerFetchResponse{Frames: []*gofiv1.SourceFrame{{Data: []byte(`{"n":1}`), Meta: map[string]string{"feed": "a"}}}, State: mustStruct(t, map[string]any{"next": 2})}, nil
		case 2:
			return &gofiv1.WorkerFetchResponse{Error: "NameError: boom", ErrorLocation: &gofiv1.SourceDiagnostic{Line: 7}}, nil
		}
		return &gofiv1.WorkerFetchResponse{Frames: []*gofiv1.SourceFrame{{Data: []byte("a")}, {Data: []byte("b")}}}, nil
	}
	useWorker(t, w)
	c := newPythonCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := spec.FlowSource{Python: &spec.PythonSource{Library: "radar", Function: "poll", Every: "1s", Config: map[string]any{"limit": 3}}}
	go Run(ctx, "flow-a", src, c.emit)
	frames := c.wait(t, 3)
	if frames[0].data != `{"n":1}` || frames[0].remote != "python:radar.poll" || frames[0].meta["feed"] != "a" {
		t.Fatalf("first frame = %+v", frames[0])
	}
	if frames[1].data != "a" || frames[2].data != "b" {
		t.Fatalf("frames = %+v", frames)
	}
	cancel()
	w.mu.Lock()
	defer w.mu.Unlock()
	first, second, third := w.requests[0], w.requests[1], w.requests[2]
	if first.Name != "poll" || first.Flow != "flow-a" || first.Config.AsMap()["limit"] != float64(3) || first.State != nil {
		t.Fatalf("first request = %v", first)
	}
	// The state a call returned is sent with the next call, and kept when a
	// call fails.
	if second.State.AsMap()["next"] != float64(2) || third.State.AsMap()["next"] != float64(2) {
		t.Fatalf("state not carried: %v / %v", second.State, third.State)
	}
	if w.overlap.Load() {
		t.Fatal("polls overlapped")
	}
	if Describe(src) != "python radar.poll" {
		t.Fatalf("describe = %q", Describe(src))
	}
}

func TestPythonStreamerReconnectsWithStateAndStopsOnCancel(t *testing.T) {
	w := &fakeWorker{kind: spec.KindStreamer}
	stopped := make(chan struct{})
	w.stream = func(ctx context.Context, n int, req *gofiv1.WorkerSourceRequest, recv func(*gofiv1.WorkerStreamResponse) error) error {
		if err := recv(&gofiv1.WorkerStreamResponse{Started: true}); err != nil {
			return err
		}
		if n == 1 {
			_ = recv(&gofiv1.WorkerStreamResponse{Frames: []*gofiv1.SourceFrame{{Data: []byte("one")}}, State: mustStruct(t, map[string]any{"cursor": "c1"})})
			return errors.New("streamer radar.feed: RuntimeError: feed went away")
		}
		if req.State.AsMap()["cursor"] != "c1" {
			t.Errorf("reconnect state = %v", req.State)
		}
		_ = recv(&gofiv1.WorkerStreamResponse{Frames: []*gofiv1.SourceFrame{{Data: []byte("two"), Meta: map[string]string{"k": "v"}}, {Data: []byte("three")}}})
		<-ctx.Done() // held open until the flow stops
		close(stopped)
		return ctx.Err()
	}
	useWorker(t, w)
	c := newPythonCollector()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, "flow-b", spec.FlowSource{Python: &spec.PythonSource{Library: "radar", Function: "feed"}}, c.emit)
	}()
	frames := c.wait(t, 3)
	if frames[0].data != "one" || frames[1].data != "two" || frames[1].meta["k"] != "v" || frames[2].data != "three" || frames[2].remote != "python:radar.feed" {
		t.Fatalf("frames = %+v", frames)
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stream not cancelled when the flow stopped")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPythonSourceRejectsWrongKind(t *testing.T) {
	w := &fakeWorker{kind: "transformer"}
	useWorker(t, w)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// Keeps retrying (the library may be fixed) without ever calling it.
	if err := Run(ctx, "flow-c", spec.FlowSource{Python: &spec.PythonSource{Library: "radar", Function: "normalize"}}, func(context.Context, []byte, string) {}); err != nil {
		t.Fatal(err)
	}
	if len(w.requests) != 0 {
		t.Fatal("a transformer was called as a source")
	}
}

func TestPythonFetchOversizedFrameFailsPoll(t *testing.T) {
	w := &fakeWorker{kind: spec.KindFetcher}
	w.fetch = func(int, *gofiv1.WorkerSourceRequest) (*gofiv1.WorkerFetchResponse, error) {
		return &gofiv1.WorkerFetchResponse{Frames: []*gofiv1.SourceFrame{{Data: []byte("ok")}, {Data: make([]byte, MaxMessageBytes+1)}}}, nil
	}
	useWorker(t, w)
	run := &pythonRun{flow: "f", cfg: &spec.PythonSource{Library: "radar", Function: "poll"}, emit: func(context.Context, []byte, string) { t.Fatal("emitted part of a failed poll") }}
	if err := run.fetch(context.Background()); err == nil {
		t.Fatal("oversized frame accepted")
	}
}
