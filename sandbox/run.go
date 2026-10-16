package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ErrRunInUse reports a RunSource run_id whose worker is still running.
var ErrRunInUse = errors.New("run_id is already in use by a running worker")

// MaxScratchRuns bounds concurrent unsaved-source runs; each one starts a
// dedicated worker with the same isolation and limits as a saved library.
const MaxScratchRuns = 2

var scratchSlots = make(chan struct{}, MaxScratchRuns)

// RunScratch starts a short-lived worker for unsaved source, calls use with
// its binding, and always removes the worker afterwards. The source is
// loaded inside each invocation process (never at worker startup and never
// in the controller), so load errors are reported to the caller.
func (s *Supervisor) RunScratch(ctx context.Context, d Definition, use func(context.Context, Binding) error) error {
	return s.RunScratchAs(ctx, "", d, use)
}

// ScratchName maps a client run id to its deterministic worker name, so
// WatchWorkerLogs(run_id) can attach. An empty run id yields "".
func ScratchName(runID string) string {
	var name strings.Builder
	for _, r := range strings.ToLower(runID) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			name.WriteRune(r)
		case name.Len() > 0 && !strings.HasSuffix(name.String(), "-"):
			name.WriteByte('-')
		}
	}
	slug := strings.Trim(name.String(), "-")
	if len(slug) > 36 {
		slug = strings.TrimRight(slug[:36], "-")
	}
	if slug == "" {
		return ""
	}
	return ScratchPrefix + slug
}

// RunScratchAs is RunScratch with a worker named from runID (random when
// empty). A run id already in use is refused rather than replacing the
// running worker.
func (s *Supervisor) RunScratchAs(ctx context.Context, runID string, d Definition, use func(context.Context, Binding) error) error {
	if s.backend == nil {
		return fmt.Errorf("Python sandbox backend is not configured")
	}
	name := ScratchName(runID)
	if runID != "" && name == "" {
		return fmt.Errorf("run_id must contain letters or digits")
	}
	if name == "" {
		var identity [8]byte
		if _, err := rand.Read(identity[:]); err != nil {
			return err
		}
		name = ScratchPrefix + hex.EncodeToString(identity[:])
	}
	var credential [32]byte
	if _, err := rand.Read(credential[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(credential[:])
	s.mu.Lock()
	if _, busy := s.scratch[name]; busy {
		s.mu.Unlock()
		return ErrRunInUse
	}
	s.scratch[name] = token
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.scratch, name)
		s.mu.Unlock()
	}()
	select {
	case scratchSlots <- struct{}{}:
		defer func() { <-scratchSlots }()
	case <-ctx.Done():
		return fmt.Errorf("all %d scratch Python workers are busy: %w", MaxScratchRuns, ctx.Err())
	}
	d.Name, d.Scratch, d.ManagedBy, d.Tests = name, true, "", nil
	d, err := Normalize(d)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.backend.Stop(cleanup, d.Name); err != nil {
			slog.Warn("scratch Python worker cleanup failed", "worker", d.Name, "error", err)
		}
	}()
	endpoint, err := s.backend.Start(ctx, d, token)
	if err != nil {
		return fmt.Errorf("scratch Python worker failed to start: %w", err)
	}
	return use(ctx, Binding{Endpoint: endpoint, Token: token, TimeoutSeconds: d.TimeoutSeconds})
}
