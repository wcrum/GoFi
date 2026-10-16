package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	// DefaultLogTail and MaxLogTail bound the lines replayed before following.
	DefaultLogTail = 100
	MaxLogTail     = 1000
	// runWait is how long WatchLogs waits for a RunSource worker to appear.
	runWait = 10 * time.Second
	// restartWait is how long a library follow waits for a replaced worker.
	restartWait = 30 * time.Second
	maxLogLine  = 64 << 10
)

// ErrNoWorker reports that no worker container/Pod exists (yet) for a name.
var ErrNoWorker = errors.New("worker not found")

// LogLine is one worker output line, decoded from the worker's JSON log
// format (gofi/logs.py) or wrapped verbatim when it is not JSON.
type LogLine struct {
	Time         time.Time
	Stream       string // "stdout" | "stderr" | "system"
	Line         string
	InvocationID string
	Function     string
	Kind         string
	Level        string
}

// RawLine is one line as read from the container runtime.
type RawLine struct {
	Time   time.Time
	Stream string // runtime stream: "stdout" | "stderr"
	Text   string
}

// LogSource is implemented by backends that can follow a worker's output.
// Follow streams lines until the worker exits or ctx ends; it returns
// ErrNoWorker when the worker does not exist.
type LogSource interface {
	Follow(ctx context.Context, worker string, tail int, emit func(RawLine) error) error
}

type workerRecord struct {
	TS           string `json:"ts"`
	Stream       string `json:"stream"`
	Line         string `json:"line"`
	InvocationID string `json:"invocationId"`
	Function     string `json:"function"`
	Kind         string `json:"kind"`
	Level        string `json:"level"`
	// Operational logs (serve.py JSONFormatter): {"time","level","msg"}.
	Time string  `json:"time"`
	Msg  *string `json:"msg"`
}

func level(value string) string {
	switch strings.ToLower(value) {
	case "warning", "warn":
		return "warn"
	case "error", "critical", "fatal":
		return "error"
	case "debug":
		return "debug"
	default:
		return "info"
	}
}

// ParseLogLine decodes a worker output line.
func ParseLogLine(raw RawLine) LogLine {
	out := LogLine{Time: raw.Time, Stream: raw.Stream, Line: raw.Text, Level: "info"}
	if raw.Stream == "stderr" {
		out.Level = "warn"
	}
	text := strings.TrimSpace(raw.Text)
	if !strings.HasPrefix(text, "{") {
		return out
	}
	var record workerRecord
	if json.Unmarshal([]byte(text), &record) != nil {
		return out
	}
	stamp := record.TS
	switch {
	case record.Stream != "":
		out.Stream, out.Line = record.Stream, record.Line
		out.InvocationID, out.Function, out.Kind = record.InvocationID, record.Function, record.Kind
		out.Level = "info"
		if record.Level != "" {
			out.Level = level(record.Level)
		} else if record.Stream == "stderr" {
			out.Level = "warn"
		}
	case record.Msg != nil:
		out.Stream, out.Line, out.Level, stamp = "system", *record.Msg, level(record.Level), record.Time
	default:
		return out
	}
	if parsed, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
		out.Time = parsed
	}
	return out
}

// scanLines emits each line of r, truncating very long lines.
func scanLines(r io.Reader, stream string, timestamps bool, emit func(RawLine) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		stamp := time.Now().UTC()
		if timestamps {
			if before, after, ok := strings.Cut(line, " "); ok {
				if parsed, err := time.Parse(time.RFC3339Nano, before); err == nil {
					stamp, line = parsed, after
				}
			}
		}
		if len(line) > maxLogLine {
			line = line[:maxLogLine]
		}
		if err := emit(RawLine{Time: stamp, Stream: stream, Text: line}); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	return nil
}

func workerName(name string) string { return "gofi-python-" + name }

func redact(line LogLine, tokens []string) LogLine {
	for _, token := range tokens {
		if token != "" && strings.Contains(line.Line, token) {
			line.Line = strings.ReplaceAll(line.Line, token, "[redacted]")
		}
	}
	return line
}

// WatchLogs follows a saved library's worker (library) or a RunSource
// worker (runID). A library follow reattaches when the worker is replaced
// (re-apply, restart) and ends when the library is deleted; a run follow
// waits briefly for the worker to start and ends when it exits.
func (s *Supervisor) WatchLogs(ctx context.Context, library, runID string, tail int, emit func(LogLine) error) error {
	source, ok := s.backend.(LogSource)
	if !ok || s.backend == nil {
		return fmt.Errorf("this sandbox backend cannot stream worker logs")
	}
	if tail <= 0 {
		tail = DefaultLogTail
	}
	tail = min(tail, MaxLogTail)
	if runID != "" {
		name := ScratchName(runID)
		if name == "" {
			return fmt.Errorf("run_id must contain letters or digits")
		}
		return s.followRun(ctx, source, name, tail, emit)
	}
	if _, found := s.Get(library); !found {
		return ErrLibraryNotFound
	}
	return s.followLibrary(ctx, source, library, tail, emit)
}

// ErrLibraryNotFound reports an unknown library name.
var ErrLibraryNotFound = errors.New("Python library not found")

func (s *Supervisor) tokens(name string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []string{s.scratch[name]}
	if binding, ok := s.bindings[name]; ok {
		out = append(out, binding.Token)
	}
	return out
}

func (s *Supervisor) relay(name string, emit func(LogLine) error) func(RawLine) error {
	return func(raw RawLine) error { return emit(redact(ParseLogLine(raw), s.tokens(name))) }
}

func system(line, lvl string) LogLine {
	return LogLine{Time: time.Now().UTC(), Stream: "system", Line: line, Level: lvl}
}

func (s *Supervisor) runActive(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.scratch[name]
	return ok
}

func (s *Supervisor) followRun(ctx context.Context, source LogSource, name string, tail int, emit func(LogLine) error) error {
	started, seen := time.Now(), false
	for {
		err := source.Follow(ctx, workerName(name), tail, s.relay(name, emit))
		if !errors.Is(err, ErrNoWorker) || ctx.Err() != nil {
			return err
		}
		// Not created yet (RunSource validates statically before starting
		// its worker), or already removed. Wait up to runWait for the run;
		// once it was seen and has finished, end.
		active := s.runActive(name)
		seen = seen || active
		switch {
		case seen && !active:
			return emit(system("the run finished", "info"))
		case time.Since(started) > runWait:
			return emit(system("no worker started for this run_id within 10s", "warn"))
		}
		if sleep(ctx, 200*time.Millisecond) != nil {
			return nil
		}
	}
}

func (s *Supervisor) followLibrary(ctx context.Context, source LogSource, library string, tail int, emit func(LogLine) error) error {
	misses := 0
	for {
		err := source.Follow(ctx, workerName(library), tail, s.relay(library, emit))
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !errors.Is(err, ErrNoWorker) {
			return err
		}
		if _, found := s.Get(library); !found {
			return emit(system("library deleted; log stream ended", "info"))
		}
		message := "worker stopped; reattaching when it is running again"
		if err != nil {
			misses++
			if status, _ := s.Status(library); misses > 2 && status.Phase == "Ready" {
				return fmt.Errorf("library %s is ready but its worker cannot be followed", library)
			}
			message = "worker is not running; waiting for it to start"
		} else {
			misses = 0
		}
		if err := emit(system(message, "info")); err != nil {
			return err
		}
		if err := s.waitWorker(ctx, library); err != nil {
			return err
		}
		tail = MaxLogTail // replay the replacement worker's startup lines
	}
}

// waitWorker polls until the library's worker can be followed again.
func (s *Supervisor) waitWorker(ctx context.Context, library string) error {
	deadline := time.Now().Add(restartWait)
	for time.Now().Before(deadline) {
		if err := sleep(ctx, 500*time.Millisecond); err != nil {
			return nil
		}
		if _, found := s.Get(library); !found {
			return nil
		}
		if status, _ := s.Status(library); status.Phase == "Ready" {
			return nil
		}
	}
	return fmt.Errorf("library %s worker did not become ready within %s", library, restartWait)
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
