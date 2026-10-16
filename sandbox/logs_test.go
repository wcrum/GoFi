package sandbox

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseLogLine(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	worker := ParseLogLine(RawLine{Time: now, Stream: "stdout", Text: `{"ts":"2026-10-16T12:00:01.5+00:00","stream":"stderr","line":"careful","invocationId":"i1","function":"f","kind":"parser"}`})
	if worker.Stream != "stderr" || worker.Line != "careful" || worker.InvocationID != "i1" || worker.Function != "f" || worker.Kind != "parser" || worker.Level != "warn" || worker.Time.Second() != 1 {
		t.Fatalf("worker record: %+v", worker)
	}
	system := ParseLogLine(RawLine{Time: now, Stream: "stdout", Text: `{"ts":"x","stream":"system","line":"invocation failed","level":"error"}`})
	if system.Stream != "system" || system.Level != "error" || !system.Time.Equal(now) {
		t.Fatalf("system record: %+v", system)
	}
	operational := ParseLogLine(RawLine{Time: now, Stream: "stdout", Text: `{"time":"2026-10-16T12:00:02+00:00","level":"WARNING","msg":"Started server","logger":"uvicorn"}`})
	if operational.Stream != "system" || operational.Line != "Started server" || operational.Level != "warn" {
		t.Fatalf("operational record: %+v", operational)
	}
	for _, raw := range []RawLine{{Stream: "stdout", Text: "plain text"}, {Stream: "stderr", Text: "{not json"}, {Stream: "stdout", Text: `{"other":1}`}} {
		got := ParseLogLine(raw)
		if got.Stream != raw.Stream || got.Line != raw.Text || got.InvocationID != "" {
			t.Fatalf("verbatim line %q: %+v", raw.Text, got)
		}
	}
	if ParseLogLine(RawLine{Stream: "stderr", Text: "oops"}).Level != "warn" {
		t.Fatal("plain stderr lines are warnings")
	}
}

func TestScratchName(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"!!!":                   "",
		"Run 42":                "run-run-42",
		"abc_DEF..9":            "run-abc-def-9",
		strings.Repeat("a", 80): "run-" + strings.Repeat("a", 36),
		"ui-" + strings.Repeat("x", 34) + "-tail": "run-ui-" + strings.Repeat("x", 33),
	}
	for input, want := range cases {
		got := ScratchName(input)
		if got != want {
			t.Fatalf("ScratchName(%q) = %q, want %q", input, got, want)
		}
		if got != "" && !namePattern.MatchString(got) {
			t.Fatalf("ScratchName(%q) = %q violates the name rule", input, got)
		}
	}
}

// scriptedSource replays canned lines per worker name; missing names report
// ErrNoWorker until `appear` adds them.
type scriptedSource struct {
	mu      sync.Mutex
	workers map[string][]string
	follows map[string]int
	block   chan struct{}
}

func (s *scriptedSource) Start(context.Context, Definition, string) (string, error) {
	return "http://worker", nil
}
func (s *scriptedSource) Stop(context.Context, string) error { return nil }
func (s *scriptedSource) appear(name string, lines ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[name] = lines
}
func (s *scriptedSource) Follow(ctx context.Context, worker string, _ int, emit func(RawLine) error) error {
	s.mu.Lock()
	lines, ok := s.workers[worker]
	block := s.block
	s.follows[worker]++
	s.mu.Unlock()
	if !ok {
		return ErrNoWorker
	}
	for _, line := range lines {
		if err := emit(RawLine{Time: time.Now(), Stream: "stdout", Text: line}); err != nil {
			return err
		}
	}
	if block != nil {
		select {
		case <-ctx.Done():
		case <-block:
		}
	}
	return nil
}

func newScripted() *scriptedSource {
	return &scriptedSource{workers: map[string][]string{}, follows: map[string]int{}}
}

func TestWatchLogsRunWaitsForTheWorkerAndRedactsTokens(t *testing.T) {
	source := newScripted()
	s := New(source, nil)
	s.scratch["run-r1"] = "secret-token"
	go func() {
		time.Sleep(300 * time.Millisecond)
		source.appear("gofi-python-run-r1", `{"ts":"2026-10-16T12:00:00+00:00","stream":"stdout","line":"hello","invocationId":"r1"}`, "token is secret-token")
	}()
	var got []LogLine
	err := s.WatchLogs(context.Background(), "", "r1", 0, func(line LogLine) error { got = append(got, line); return nil })
	if err != nil || len(got) != 2 {
		t.Fatalf("WatchLogs = %v, %+v", err, got)
	}
	if got[0].Line != "hello" || got[0].InvocationID != "r1" || got[1].Line != "token is [redacted]" {
		t.Fatalf("lines: %+v", got)
	}
}

func TestWatchLogsRunWithoutWorkerEnds(t *testing.T) {
	s := New(newScripted(), nil)
	start := time.Now()
	var got []LogLine
	if err := s.WatchLogs(context.Background(), "", "missing", 0, func(line LogLine) error { got = append(got, line); return nil }); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < runWait || elapsed > runWait+2*time.Second || len(got) != 1 || got[0].Stream != "system" {
		t.Fatalf("a run that never starts ends after the wait with a notice: %v %+v", elapsed, got)
	}
}

func TestWatchLogsRunEndsWhenASeenRunFinishes(t *testing.T) {
	s := New(newScripted(), nil)
	s.scratch["run-r2"] = "token"
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.mu.Lock()
		delete(s.scratch, "run-r2")
		s.mu.Unlock()
	}()
	start := time.Now()
	var got []LogLine
	if err := s.WatchLogs(context.Background(), "", "r2", 0, func(line LogLine) error { got = append(got, line); return nil }); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second || len(got) != 1 || got[0].Line != "the run finished" {
		t.Fatalf("a finished run ends promptly: %v %+v", time.Since(start), got)
	}
}

func TestWatchLogsLibrary(t *testing.T) {
	source := newScripted()
	s := New(source, nil)
	if err := s.WatchLogs(context.Background(), "nope", "", 0, func(LogLine) error { return nil }); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("unknown library: %v", err)
	}
	s.defs["lib"] = Definition{Name: "lib"}
	s.statuses["lib"] = Status{Name: "lib", Phase: "Ready"}
	source.appear("gofi-python-lib", "first")
	block := make(chan struct{})
	source.mu.Lock()
	source.block = block
	source.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	lines := make(chan LogLine, 10)
	done := make(chan error, 1)
	go func() { done <- s.WatchLogs(ctx, "lib", "", 0, func(line LogLine) error { lines <- line; return nil }) }()
	if line := <-lines; line.Line != "first" {
		t.Fatalf("first line: %+v", line)
	}
	// The worker is replaced: the follow ends and reattaches to the new one.
	source.appear("gofi-python-lib", "second")
	source.mu.Lock()
	source.block = nil
	source.mu.Unlock()
	close(block)
	seen := map[string]bool{}
	for !seen["second"] {
		seen[(<-lines).Line] = true
	}
	if !seen["worker stopped; reattaching when it is running again"] {
		t.Fatalf("restart notice missing: %v", seen)
	}
	// Deleting the library ends the stream.
	s.mu.Lock()
	delete(s.defs, "lib")
	s.mu.Unlock()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWatchLogsStopsWhenTheClientGoesAway(t *testing.T) {
	source := newScripted()
	source.appear("gofi-python-lib", "a", "b", "c")
	s := New(source, nil)
	s.defs["lib"] = Definition{Name: "lib"}
	gone := errors.New("client gone")
	calls := 0
	err := s.WatchLogs(context.Background(), "lib", "", 0, func(LogLine) error { calls++; return gone })
	if !errors.Is(err, gone) || calls != 1 {
		t.Fatalf("emit failure must end the follow: %v after %d", err, calls)
	}
}

func TestRunScratchRefusesARunIDInUse(t *testing.T) {
	s := New(&fakeBackend{}, nil)
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_ = s.RunScratchAs(context.Background(), "same", Definition{SourceCode: "x = 1"}, func(context.Context, Binding) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	err := s.RunScratchAs(context.Background(), "SAME", Definition{SourceCode: "x = 1"}, func(context.Context, Binding) error { return nil })
	close(release)
	if !errors.Is(err, ErrRunInUse) {
		t.Fatalf("duplicate run id: %v", err)
	}
}
