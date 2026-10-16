package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

const toolingSource = "import gofi\n@gofi.parser(\"noop\")\ndef noop(raw):\n    return {}\n"

// ToolingBinding starts a private fixed-source worker lazily, before any user
// library is saved. Editor source is supplied only to its static tooling RPCs,
// never used as a worker startup definition or imported for formatting/linting.
func (s *Supervisor) ToolingBinding(ctx context.Context) (Binding, error) {
	s.toolingMu.Lock()
	defer s.toolingMu.Unlock()
	if err := ctx.Err(); err != nil {
		return Binding{}, err
	}
	if s.backend == nil || s.toolingClosed {
		return Binding{}, fmt.Errorf("Python tooling backend is unavailable")
	}
	if s.toolingContext != nil {
		callCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(s.toolingContext, cancel)
		defer stop()
		defer cancel()
		ctx = callCtx
	}
	if s.tooling.Endpoint != "" {
		if health, ok := s.backend.(HealthBackend); !ok || health.Health(ctx, s.tooling) == nil {
			return s.tooling, nil
		}
	}
	if s.toolingName == "" {
		var identity [8]byte
		if _, err := rand.Read(identity[:]); err != nil {
			return Binding{}, err
		}
		s.toolingName = "gofi-tools-" + hex.EncodeToString(identity[:])
	}
	var credential [32]byte
	if _, err := rand.Read(credential[:]); err != nil {
		return Binding{}, err
	}
	token := hex.EncodeToString(credential[:])
	definition := Definition{Name: s.toolingName, SourceCode: toolingSource, TimeoutSeconds: 5, MemoryMiB: 128}
	endpoint, err := s.backend.Start(ctx, definition, token)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.backend.Stop(cleanup, s.toolingName)
		s.tooling = Binding{}
		return Binding{}, err
	}
	s.tooling = Binding{Endpoint: endpoint, Token: token, TimeoutSeconds: 5}
	return s.tooling, nil
}

// CloseTooling removes only this supervisor's private tooling worker. Public
// durable libraries retain their own normal runtime lifecycle.
func (s *Supervisor) CloseTooling(ctx context.Context) error {
	if s.toolingCancel != nil {
		s.toolingCancel()
	}
	s.toolingMu.Lock()
	defer s.toolingMu.Unlock()
	s.toolingClosed = true
	s.tooling = Binding{}
	if s.backend == nil || s.toolingName == "" {
		return nil
	}
	if err := s.backend.Stop(ctx, s.toolingName); err != nil {
		return err
	}
	s.toolingName = ""
	return nil
}
