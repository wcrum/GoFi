package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/spec"
)

type fakeDriver struct {
	definitions      map[string]spec.ListenerDefinition
	creates, deletes int
	deleteErr        error
}

func (d *fakeDriver) Create(_ context.Context, def spec.ListenerDefinition) error {
	d.definitions[def.Name] = def
	d.creates++
	return nil
}
func (d *fakeDriver) Delete(_ context.Context, name string) error {
	d.deletes++
	if d.deleteErr != nil {
		return d.deleteErr
	}
	delete(d.definitions, name)
	return nil
}
func (d *fakeDriver) List(context.Context) ([]spec.ListenerDefinition, error) {
	out := []spec.ListenerDefinition{}
	for _, def := range d.definitions {
		out = append(out, def)
	}
	return out, nil
}
func (d *fakeDriver) Tap(context.Context, string) (driver.TapTarget, bool, error) {
	return driver.TapTarget{}, false, nil
}
func (d *fakeDriver) RuntimeStatus(context.Context, string) (driver.RuntimeInfo, bool, error) {
	return driver.RuntimeInfo{}, false, nil
}
func (d *fakeDriver) Kind() string { return "test" }

func controllerDef() spec.ListenerDefinition {
	return spec.ListenerDefinition{Name: "route", Source: spec.Source{Port: 9000}, Parser: spec.ParserCfg{Format: "none"}}
}

func TestControllerRepairsRuntimeDefinitionDriftAndRetriesDelete(t *testing.T) {
	def := controllerDef()
	drifted := def
	drifted.Source.Port = 1234
	d := &fakeDriver{definitions: map[string]spec.ListenerDefinition{def.Name: drifted}, deleteErr: errors.New("temporarily unavailable")}
	c := New(d)
	c.desired[def.Name] = def
	c.sync(context.Background())
	if d.creates != 0 || d.definitions[def.Name].Source.Port != 1234 {
		t.Fatal("must not create over a failed delete")
	}
	d.deleteErr = nil
	c.sync(context.Background())
	if d.creates != 1 || d.definitions[def.Name].Source.Port != 9000 {
		t.Fatal("drift not repaired after delete recovered")
	}
	c.sync(context.Background())
	if d.creates != 1 {
		t.Fatal("unchanged definition restarted")
	}
}

func TestEquivalentDefinitionsNormalizesEmptyCollections(t *testing.T) {
	a := controllerDef()
	b := a
	b.Stages = []spec.StageSpec{}
	b.Sinks = []spec.SinkCfg{}
	if !equivalentDefinitions(a, b) {
		t.Fatal("container serialization should not cause drift for empty arrays")
	}
}

type memoryStore struct {
	defs             []spec.ListenerDefinition
	loadErr, saveErr error
}

func (s *memoryStore) Load(context.Context) ([]spec.ListenerDefinition, error) {
	return s.defs, s.loadErr
}
func (s *memoryStore) Save(_ context.Context, def spec.ListenerDefinition) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.defs = []spec.ListenerDefinition{def}
	return nil
}
func (s *memoryStore) Delete(context.Context, string) error { return s.saveErr }
func (s *memoryStore) Configuration() desired.Configuration {
	return desired.Configuration{Source: "test"}
}

func TestControllerRetainsLastGoodStateWhenStoreFails(t *testing.T) {
	def := controllerDef()
	d := &fakeDriver{definitions: map[string]spec.ListenerDefinition{}}
	s := &memoryStore{defs: []spec.ListenerDefinition{def}}
	c := New(d)
	if err := c.WithStore(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	c.sync(context.Background())
	s.loadErr = errors.New("configuration unavailable")
	c.sync(context.Background())
	if _, ok := c.Get(def.Name); !ok || len(d.definitions) != 1 {
		t.Fatal("failed load removed last good listener")
	}
	s.loadErr = nil
	s.saveErr = errors.New("read only")
	updated := def
	updated.Source.Port = 9001
	if err := c.Add(updated); err == nil {
		t.Fatal("save failure not reported")
	}
	if got, _ := c.Get(def.Name); got.Source.Port != 9000 {
		t.Fatal("failed persistence mutated desired state")
	}
}

func TestControllerRejectsInvalidSnapshotAtomicallyAndRecoversMissingRuntime(t *testing.T) {
	ctx := context.Background()
	def := controllerDef()
	d := &fakeDriver{definitions: map[string]spec.ListenerDefinition{}}
	s := &memoryStore{defs: []spec.ListenerDefinition{def}}
	c := New(d)
	if err := c.WithStore(ctx, s); err != nil {
		t.Fatal(err)
	}
	c.sync(ctx)
	invalid := def
	invalid.Name = "broken"
	invalid.DeadLetterSink = &spec.SinkCfg{Type: "stdout", When: "123"}
	updated := def
	updated.Source.Port = 9100
	s.defs = []spec.ListenerDefinition{updated, invalid}
	delete(d.definitions, def.Name) // Runtime crashed while config was invalid.
	c.sync(ctx)
	got, ok := c.Get(def.Name)
	if !ok || got.Source.Port != 9000 || len(c.List()) != 1 {
		t.Fatalf("partial invalid snapshot installed: %+v", c.List())
	}
	if d.definitions[def.Name].Source.Port != 9000 {
		t.Fatal("invalid snapshot stopped recovery of last good runtime")
	}
	// Store outage keeps existing runtimes intact, including uncertain changes.
	s.loadErr = errors.New("unavailable")
	d.definitions["unknown"] = spec.ListenerDefinition{Name: "unknown"}
	drifted := def
	drifted.Source.Port = 9200
	d.definitions[def.Name] = drifted
	c.sync(ctx)
	if _, found := d.definitions["unknown"]; !found || d.definitions[def.Name].Source.Port != 9200 {
		t.Fatal("store outage caused destructive changes to uncertain runtime state")
	}
}
func TestControllerAddValidatesBeforeSavingAndOwnsDefinitionCopies(t *testing.T) {
	d := &fakeDriver{definitions: map[string]spec.ListenerDefinition{}}
	store := &memoryStore{}
	c := New(d)
	if err := c.WithStore(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	invalid := controllerDef()
	invalid.Name = ""
	if err := c.Add(invalid); err == nil {
		t.Fatal("invalid definition accepted")
	}
	if len(store.defs) != 0 || len(c.List()) != 0 {
		t.Fatal("invalid definition persisted")
	}
	def := controllerDef()
	def.Sinks = []spec.SinkCfg{{Type: "http", URL: "https://receiver.example", Headers: map[string]string{"X-Test": "original"}}}
	if err := c.Add(def); err != nil {
		t.Fatal(err)
	}
	def.Sinks[0].Headers["X-Test"] = "caller mutation"
	fetched, _ := c.Get(def.Name)
	fetched.Sinks[0].Headers["X-Test"] = "get mutation"
	listed := c.List()
	listed[0].Sinks[0].Headers["X-Test"] = "list mutation"
	got, _ := c.Get(def.Name)
	if got.Sinks[0].Headers["X-Test"] != "original" {
		t.Fatal("external mutation changed controller-owned state")
	}
}
