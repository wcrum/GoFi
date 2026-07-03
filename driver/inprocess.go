package driver

import (
	"context"
	"fmt"
	"sync"

	"github.com/cnak-us/gogo/listener"
	"github.com/cnak-us/gogo/spec"
)

type entry struct {
	def    spec.ListenerDefinition
	ln     *listener.Listener
	cancel context.CancelFunc
}

// InProcess runs each listener as goroutines within the controller process.
type InProcess struct {
	mu      sync.RWMutex
	running map[string]*entry
}

func NewInProcess() *InProcess {
	return &InProcess{running: make(map[string]*entry)}
}

func (d *InProcess) Create(ctx context.Context, def spec.ListenerDefinition) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.running[def.Name]; exists {
		return fmt.Errorf("listener %q already running", def.Name)
	}

	ln, err := listener.New(def)
	if err != nil {
		return err
	}

	lctx, cancel := context.WithCancel(ctx)
	go func() {
		defer ln.Close()
		ln.Run(lctx)
	}()

	d.running[def.Name] = &entry{def: def, ln: ln, cancel: cancel}
	return nil
}

func (d *InProcess) Delete(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	e, ok := d.running[name]
	if !ok {
		return fmt.Errorf("listener %q not found", name)
	}
	e.cancel()
	delete(d.running, name)
	return nil
}

func (d *InProcess) List(_ context.Context) ([]spec.ListenerDefinition, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]spec.ListenerDefinition, 0, len(d.running))
	for _, e := range d.running {
		out = append(out, e.def)
	}
	return out, nil
}

func (d *InProcess) Tap(name string) *listener.Listener {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if e, ok := d.running[name]; ok {
		return e.ln
	}
	return nil
}
