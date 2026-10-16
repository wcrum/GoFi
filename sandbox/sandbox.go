// Package sandbox manages Python libraries in isolated containers or Pods.
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"sync"
	"time"
)

const DefaultImage = "ghcr.io/wcrum/gofi-python:0.2.0"

type Definition struct {
	Name           string `json:"name,omitempty"`
	SourceCode     string `json:"sourceCode"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	MemoryMiB      int    `json:"memoryMib,omitempty"`
}
type Binding struct {
	Endpoint       string `json:"endpoint"`
	Token          string `json:"token"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}
type Status struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
	Error string `json:"error,omitempty"`
}
type Store interface {
	Load(context.Context) ([]Definition, error)
	Save(context.Context, Definition) error
	Delete(context.Context, string) error
}
type HealthBackend interface {
	Health(context.Context, Binding) error
}

type Backend interface {
	Start(context.Context, Definition, string) (string, error)
	Stop(context.Context, string) error
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

func Normalize(d Definition) (Definition, error) {
	if !namePattern.MatchString(d.Name) {
		return d, fmt.Errorf("library name must be 1-40 lowercase letters, numbers, or hyphens, starting with a letter")
	}
	if len(d.SourceCode) == 0 || len(d.SourceCode) > 65536 {
		return d, fmt.Errorf("sourceCode must contain 1-65536 bytes")
	}
	if d.TimeoutSeconds == 0 {
		d.TimeoutSeconds = 5
	}
	if d.TimeoutSeconds < 1 || d.TimeoutSeconds > 30 {
		return d, fmt.Errorf("timeoutSeconds must be between 1 and 30")
	}
	if d.MemoryMiB == 0 {
		d.MemoryMiB = 128
	}
	if d.MemoryMiB < 64 || d.MemoryMiB > 1024 {
		return d, fmt.Errorf("memoryMib must be between 64 and 1024")
	}
	return d, nil
}

type Supervisor struct {
	mu             sync.RWMutex
	operations     sync.Mutex
	backend        Backend
	store          Store
	defs           map[string]Definition
	bindings       map[string]Binding
	statuses       map[string]Status
	toolingMu      sync.Mutex
	toolingName    string
	tooling        Binding
	toolingClosed  bool
	toolingContext context.Context
	toolingCancel  context.CancelFunc
}

func New(b Backend, s Store) *Supervisor {
	toolingContext, toolingCancel := context.WithCancel(context.Background())
	return &Supervisor{backend: b, store: s, defs: map[string]Definition{}, bindings: map[string]Binding{}, statuses: map[string]Status{}, toolingContext: toolingContext, toolingCancel: toolingCancel}
}
func (s *Supervisor) Apply(ctx context.Context, d Definition) error {
	d, err := Normalize(d)
	if err != nil {
		return err
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	if s.store != nil {
		if err = s.store.Save(ctx, d); err != nil {
			return err
		}
	}
	return s.start(ctx, d)
}
func (s *Supervisor) start(ctx context.Context, d Definition) error {
	if s.backend == nil {
		return fmt.Errorf("Python sandbox backend is not configured")
	}
	s.mu.Lock()
	s.defs[d.Name] = d
	s.statuses[d.Name] = Status{Name: d.Name, Phase: "Starting"}
	delete(s.bindings, d.Name)
	s.mu.Unlock()
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	value := hex.EncodeToString(token)
	endpoint, err := s.backend.Start(ctx, d, value)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.statuses[d.Name] = Status{Name: d.Name, Phase: "Failed", Error: err.Error()}
		return err
	}
	s.bindings[d.Name] = Binding{Endpoint: endpoint, Token: value, TimeoutSeconds: d.TimeoutSeconds}
	s.statuses[d.Name] = Status{Name: d.Name, Phase: "Ready"}
	return nil
}
func (s *Supervisor) Delete(ctx context.Context, name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid library name")
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	if s.store != nil {
		if err := s.store.Delete(ctx, name); err != nil {
			return err
		}
	}
	if s.backend != nil {
		if err := s.backend.Stop(ctx, name); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.defs, name)
	delete(s.bindings, name)
	delete(s.statuses, name)
	return nil
}
func (s *Supervisor) List() []Definition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Definition, 0, len(s.defs))
	for _, d := range s.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (s *Supervisor) Get(name string) (Definition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.defs[name]
	return v, ok
}
func (s *Supervisor) Status(name string) (Status, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.statuses[name]
	return v, ok
}
func (s *Supervisor) Bindings(context.Context) (map[string]Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]Binding{}
	for n, b := range s.bindings {
		out[n] = b
	}
	return out, nil
}
func (s *Supervisor) Reconcile(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	defs, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, d := range defs {
		d, err = Normalize(d)
		if err != nil {
			return err
		}
		seen[d.Name] = true
		old, ok := s.Get(d.Name)
		st, _ := s.Status(d.Name)
		healthy := true
		if hb, supports := s.backend.(HealthBackend); supports && ok && st.Phase == "Ready" {
			s.mu.RLock()
			binding := s.bindings[d.Name]
			s.mu.RUnlock()
			healthy = hb.Health(ctx, binding) == nil
		}
		if !ok || old != d || st.Phase != "Ready" || !healthy {
			if err = s.start(ctx, d); err != nil {
				slog.Error("Python library start failed", "library", d.Name, "error", err)
			}
		}
	}
	for _, d := range s.List() {
		if !seen[d.Name] {
			if s.backend != nil {
				if err = s.backend.Stop(ctx, d.Name); err != nil {
					return err
				}
			}
			s.mu.Lock()
			delete(s.defs, d.Name)
			delete(s.bindings, d.Name)
			delete(s.statuses, d.Name)
			s.mu.Unlock()
		}
	}
	return nil
}
func (s *Supervisor) Run(ctx context.Context) error {
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.CloseTooling(cleanup); err != nil {
			slog.Warn("Python tooling cleanup failed", "error", err)
		}
	}()
	if err := s.Reconcile(ctx); err != nil {
		return err
	}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := s.Reconcile(ctx); err != nil {
				slog.Error("Python library reconciliation failed", "error", err)
			}
		}
	}
}
