package sandbox

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type toolingTestBackend struct {
	mu            sync.Mutex
	definitions   []Definition
	tokens        []string
	stops         []string
	healthFailure bool
	started       chan struct{}
	block         bool
}

func (b *toolingTestBackend) Start(ctx context.Context, d Definition, token string) (string, error) {
	b.mu.Lock()
	b.definitions = append(b.definitions, d)
	b.tokens = append(b.tokens, token)
	block := b.block
	started := b.started
	b.mu.Unlock()
	if started != nil {
		close(started)
	}
	if block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "http://private-worker:8080", nil
}
func (b *toolingTestBackend) Stop(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stops = append(b.stops, name)
	return nil
}
func (b *toolingTestBackend) Health(context.Context, Binding) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.healthFailure {
		return context.DeadlineExceeded
	}
	return nil
}
func TestToolingWorkerIsPrivateFixedSourceReusedAndCleaned(t *testing.T) {
	backend := &toolingTestBackend{}
	manager := New(backend, nil)
	first, err := manager.ToolingBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.ToolingBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(backend.definitions) != 1 {
		t.Fatal("healthy tooling worker was not reused")
	}
	definition := backend.definitions[0]
	if !strings.HasPrefix(definition.Name, "gofi-tools-") || definition.SourceCode != toolingSource || definition.MemoryMiB != 128 || definition.TimeoutSeconds != 5 {
		t.Fatalf("unexpected tooling definition: %+v", definition)
	}
	if len(manager.List()) != 0 {
		t.Fatal("tooling became a public/durable library")
	}
	bindings, _ := manager.Bindings(context.Background())
	if len(bindings) != 0 {
		t.Fatal("private tooling credentials exposed as application bindings")
	}
	backend.healthFailure = true
	replacement, err := manager.ToolingBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Token == first.Token || len(backend.definitions) != 2 || backend.definitions[1].Name != definition.Name {
		t.Fatal("unhealthy private worker did not restart with new credentials")
	}
	if err := manager.CloseTooling(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(backend.stops) != 1 || backend.stops[0] != definition.Name {
		t.Fatal("private worker cleanup targeted the wrong resource")
	}
	if _, err := manager.ToolingBinding(context.Background()); err == nil {
		t.Fatal("closed supervisor created a new tooling worker")
	}
}
func TestToolingShutdownCancelsStartupBeforeWaitingForLock(t *testing.T) {
	backend := &toolingTestBackend{block: true, started: make(chan struct{})}
	manager := New(backend, nil)
	complete := make(chan error, 1)
	go func() { _, err := manager.ToolingBinding(context.Background()); complete <- err }()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("tooling startup did not begin")
	}
	closed := make(chan error, 1)
	go func() { closed <- manager.CloseTooling(context.Background()) }()
	select {
	case err := <-complete:
		if err == nil {
			t.Fatal("cancelled startup succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for an uncancelled startup")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("private worker cleanup did not finish")
	}
}
