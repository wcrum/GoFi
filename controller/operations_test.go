package controller

import (
	"context"
	"testing"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// exitingDriver reports runtimes that exited on their own (an exited
// container or failed Pod still listed by RuntimeStatus but not List).
type exitingDriver struct {
	*fakeDriver
	exited map[string]bool
}

func (d *exitingDriver) RuntimeStatus(_ context.Context, name string) (driver.RuntimeInfo, bool, error) {
	if d.exited[name] {
		return driver.RuntimeInfo{Kind: "test", Status: "exited"}, true, nil
	}
	if _, ok := d.definitions[name]; ok {
		return driver.RuntimeInfo{Kind: "test", Status: "running"}, true, nil
	}
	return driver.RuntimeInfo{}, false, nil
}
func (d *exitingDriver) Create(ctx context.Context, def spec.FlowDefinition) error {
	delete(d.exited, def.Name)
	return d.fakeDriver.Create(ctx, def)
}

func TestStoppedFlowHasNoRuntimeAndStartsAgain(t *testing.T) {
	d := &exitingDriver{fakeDriver: &fakeDriver{definitions: map[string]spec.FlowDefinition{}}, exited: map[string]bool{}}
	c := New(d)
	store := &mapStore{}
	if err := c.WithStore(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	def := controllerDef()
	if err := c.Add(def); err != nil {
		t.Fatal(err)
	}
	c.sync(context.Background())
	if d.creates != 1 {
		t.Fatalf("creates = %d", d.creates)
	}
	if err := c.SetState(def.Name, spec.StateStopped); err != nil {
		t.Fatal(err)
	}
	c.sync(context.Background())
	if _, running := d.definitions[def.Name]; running {
		t.Fatal("stopped flow still has a runtime")
	}
	if stored := store.defs[def.Name]; stored.State != spec.StateStopped {
		t.Fatalf("stopped state not persisted: %+v", stored)
	}
	c.sync(context.Background())
	if d.creates != 1 {
		t.Fatal("stopped flow was recreated")
	}
	status, _ := c.Status(context.Background(), def.Name)
	if status.Phase != PhaseStopped || status.Desired != spec.StateStopped {
		t.Fatalf("status = %+v", status)
	}
	if err := c.SetState(def.Name, spec.StateRunning); err != nil {
		t.Fatal(err)
	}
	if store.defs[def.Name].State != "" {
		t.Fatal("running is stored as an omitted state")
	}
	c.sync(context.Background())
	if d.creates != 2 {
		t.Fatalf("started flow not created (creates = %d)", d.creates)
	}
	if got := d.definitions[def.Name].State; got != "" {
		t.Fatalf("runtime received declared state %q", got)
	}
	if status, _ := c.Status(context.Background(), def.Name); status.Phase != PhaseRunning {
		t.Fatalf("status = %+v", status)
	}
}

// A runtime that exits on its own is restarted, counted, and posted as a
// bulletin; a deliberate restart is not a crash.
func TestRuntimeExitIsCountedAsRestart(t *testing.T) {
	d := &exitingDriver{fakeDriver: &fakeDriver{definitions: map[string]spec.FlowDefinition{}}, exited: map[string]bool{}}
	c := New(d)
	def := controllerDef()
	observability.ClearBulletins(def.Name)
	c.desired[def.Name] = def
	c.sync(context.Background())
	// The runtime dies: List no longer has it, RuntimeStatus says exited.
	delete(d.definitions, def.Name)
	d.exited[def.Name] = true
	if status, _ := c.Status(context.Background(), def.Name); status.Phase != PhaseCrashed {
		t.Fatalf("status = %+v", status)
	}
	c.sync(context.Background())
	status, _ := c.Status(context.Background(), def.Name)
	if status.Restarts != 1 || status.Phase != PhaseRunning || d.creates != 2 {
		t.Fatalf("status = %+v creates = %d", status, d.creates)
	}
	bulletins := observability.Bulletins(def.Name)
	if len(bulletins) != 1 || bulletins[0].Component != observability.ComponentRuntime {
		t.Fatalf("bulletins = %+v", bulletins)
	}
	if err := c.Restart(context.Background(), def.Name); err != nil {
		t.Fatal(err)
	}
	c.sync(context.Background())
	if status, _ := c.Status(context.Background(), def.Name); status.Restarts != 1 || d.creates != 3 {
		t.Fatalf("deliberate restart: status = %+v creates = %d", status, d.creates)
	}
}

func TestRestartRejectsStoppedFlow(t *testing.T) {
	c := New(&fakeDriver{definitions: map[string]spec.FlowDefinition{}})
	def := controllerDef()
	def.State = spec.StateStopped
	c.desired[def.Name] = def
	if err := c.Restart(context.Background(), def.Name); err == nil {
		t.Fatal("restarted a stopped flow")
	}
	if err := c.Restart(context.Background(), "missing"); err != ErrFlowNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestInvalidStateIsRejected(t *testing.T) {
	def := controllerDef()
	def.State = "paused"
	if err := ValidateDefinition(def); err == nil {
		t.Fatal("accepted state paused")
	}
}

type mapStore struct {
	defs map[string]spec.FlowDefinition
}

func (s *mapStore) Load(context.Context) ([]spec.FlowDefinition, error) {
	out := []spec.FlowDefinition{}
	for _, def := range s.defs {
		out = append(out, cloneDefinition(def))
	}
	return out, nil
}
func (s *mapStore) Save(_ context.Context, def spec.FlowDefinition) error {
	if s.defs == nil {
		s.defs = map[string]spec.FlowDefinition{}
	}
	s.defs[def.Name] = cloneDefinition(def)
	return nil
}
func (s *mapStore) Delete(_ context.Context, name string) error {
	delete(s.defs, name)
	return nil
}
func (s *mapStore) Configuration() desired.Configuration {
	return desired.Configuration{Source: "memory"}
}
