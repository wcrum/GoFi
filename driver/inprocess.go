package driver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wcrum/gofi/listener"
	"github.com/wcrum/gofi/spec"
)

type entry struct {
	def       spec.FlowDefinition
	ln        *listener.Listener
	cancel    context.CancelFunc
	startedAt time.Time
}

// InProcess runs each listener as goroutines within the controller process.
type InProcess struct {
	mu      sync.RWMutex
	running map[string]*entry
}

func NewInProcess() *InProcess {
	return &InProcess{running: make(map[string]*entry)}
}

func (d *InProcess) Create(ctx context.Context, def spec.FlowDefinition) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.running[def.Name]; exists {
		return fmt.Errorf("flow %q already running", def.Name)
	}

	ln, err := listener.New(def)
	if err != nil {
		return err
	}

	// The runtime outlives the reconcile that created it: Delete or DrainAll
	// stop it (gracefully), not the end of the controller's context.
	lctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	go func() {
		defer ln.Close()
		ln.Run(lctx)
	}()

	d.running[def.Name] = &entry{def: def, ln: ln, cancel: cancel, startedAt: time.Now()}
	return nil
}

// Delete drains the listener: intake stops at once (the port is released),
// frames already received finish, and sinks flush, within DrainTimeout.
func (d *InProcess) Delete(_ context.Context, name string) error {
	d.mu.Lock()
	e, ok := d.running[name]
	if !ok {
		d.mu.Unlock()
		return fmt.Errorf("flow %q not found", name)
	}
	delete(d.running, name)
	d.mu.Unlock()
	d.drain(e)
	return nil
}

func (d *InProcess) drain(e *entry) {
	ctx, cancel := context.WithTimeout(context.Background(), DrainTimeout())
	defer cancel()
	_ = e.ln.Shutdown(ctx)
	e.cancel()
}

// DrainAll drains every listener concurrently (controller shutdown).
func (d *InProcess) DrainAll(ctx context.Context) error {
	d.mu.Lock()
	entries := make([]*entry, 0, len(d.running))
	for name, e := range d.running {
		entries = append(entries, e)
		delete(d.running, name)
	}
	d.mu.Unlock()
	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Go(func() {
			_ = e.ln.Shutdown(ctx)
			e.cancel()
		})
	}
	wg.Wait()
	return ctx.Err()
}

func (d *InProcess) List(_ context.Context) ([]spec.FlowDefinition, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]spec.FlowDefinition, 0, len(d.running))
	for _, e := range d.running {
		out = append(out, e.def)
	}
	return out, nil
}

func (d *InProcess) Tap(_ context.Context, name string) (TapTarget, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if e, ok := d.running[name]; ok {
		return TapTarget{Local: e.ln.Tap}, true, nil
	}
	return TapTarget{}, false, nil
}

// RuntimeStatus reports "running" for any listener present in the map — if
// it's tracked here at all, its goroutine is running (no separate concept of
// container/pod IDs, health, or node exists for this driver).
func (d *InProcess) RuntimeStatus(_ context.Context, name string) (RuntimeInfo, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.running[name]
	if !ok {
		return RuntimeInfo{}, false, nil
	}
	return RuntimeInfo{Kind: "inprocess", Status: "running", StartedAt: e.startedAt}, true, nil
}

// Kind identifies this driver implementation.
func (d *InProcess) Kind() string { return "inprocess" }
