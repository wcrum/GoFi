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
	definitions      map[string]spec.FlowDefinition
	creates, deletes int
	deleteErr        error
	createErr        error
}

func (d *fakeDriver) Create(_ context.Context, def spec.FlowDefinition) error {
	if d.createErr != nil {
		return d.createErr
	}
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
func (d *fakeDriver) List(context.Context) ([]spec.FlowDefinition, error) {
	out := []spec.FlowDefinition{}
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

func controllerDef() spec.FlowDefinition {
	return spec.FlowDefinition{Name: "route", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000}}, Parser: spec.ParserCfg{Format: "none"}}
}

func TestControllerRepairsRuntimeDefinitionDriftAndRetriesDelete(t *testing.T) {
	def := controllerDef()
	drifted := cloneDefinition(def) // deep copy: Source.Listen is a pointer
	drifted.Source.Listen.Port = 1234
	d := &fakeDriver{definitions: map[string]spec.FlowDefinition{def.Name: drifted}, deleteErr: errors.New("temporarily unavailable")}
	c := New(d)
	c.desired[def.Name] = def
	c.sync(context.Background())
	if d.creates != 0 || d.definitions[def.Name].Source.Listen.Port != 1234 {
		t.Fatal("must not create over a failed delete")
	}
	d.deleteErr = nil
	c.sync(context.Background())
	if d.creates != 1 || d.definitions[def.Name].Source.Listen.Port != 9000 {
		t.Fatal("drift not repaired after delete recovered")
	}
	c.sync(context.Background())
	if d.creates != 1 {
		t.Fatal("unchanged definition restarted")
	}
}

func TestControllerReportsRuntimeStartFailureUntilItStarts(t *testing.T) {
	def := controllerDef()
	d := &fakeDriver{definitions: map[string]spec.FlowDefinition{}, createErr: errors.New("Service is invalid")}
	c := New(d)
	c.desired[def.Name] = def
	c.sync(context.Background())
	info, found, err := c.RuntimeStatus(context.Background(), def.Name)
	if err != nil || !found || info.Status != "failed" || info.Error != "Service is invalid" {
		t.Fatalf("start failure not reported: %+v %v %v", info, found, err)
	}
	d.createErr = nil
	c.sync(context.Background())
	if info, _, _ := c.RuntimeStatus(context.Background(), def.Name); info.Error != "" {
		t.Fatalf("error kept after a successful start: %+v", info)
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
	defs             []spec.FlowDefinition
	loadErr, saveErr error
}

func (s *memoryStore) Load(context.Context) ([]spec.FlowDefinition, error) {
	return s.defs, s.loadErr
}
func (s *memoryStore) Save(_ context.Context, def spec.FlowDefinition) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.defs = []spec.FlowDefinition{def}
	return nil
}
func (s *memoryStore) Delete(context.Context, string) error { return s.saveErr }
func (s *memoryStore) Configuration() desired.Configuration {
	return desired.Configuration{Source: "test"}
}

func TestControllerRetainsLastGoodStateWhenStoreFails(t *testing.T) {
	def := controllerDef()
	d := &fakeDriver{definitions: map[string]spec.FlowDefinition{}}
	s := &memoryStore{defs: []spec.FlowDefinition{def}}
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
	updated := cloneDefinition(def)
	updated.Source.Listen.Port = 9001
	if err := c.Add(updated); err == nil {
		t.Fatal("save failure not reported")
	}
	if got, _ := c.Get(def.Name); got.Source.Listen.Port != 9000 {
		t.Fatal("failed persistence mutated desired state")
	}
}

func TestControllerSkipsInvalidFlowsAndKeepsTheLastGoodVersion(t *testing.T) {
	ctx := context.Background()
	def := controllerDef()
	broken := cloneDefinition(def)
	broken.Name = "broken"
	broken.DeadLetterSink = &spec.SinkCfg{Type: "stdout", When: "123"}
	d := &fakeDriver{definitions: map[string]spec.FlowDefinition{}}
	s := &memoryStore{defs: []spec.FlowDefinition{def, broken}}
	c := New(d)
	// An invalid stored flow must not stop the controller from starting.
	if err := c.WithStore(ctx, s); err != nil {
		t.Fatal(err)
	}
	c.sync(ctx)
	if _, running := d.definitions["broken"]; running || d.definitions[def.Name].Name == "" {
		t.Fatalf("expected only the valid flow to run: %+v", d.definitions)
	}
	if got, ok := c.Get("broken"); !ok || got.Name != "broken" || len(c.List()) != 2 {
		t.Fatal("invalid flow must stay listed so it can be fixed or deleted")
	}
	if info, found, _ := c.RuntimeStatus(ctx, "broken"); !found || info.Status != "invalid" || info.Error == "" {
		t.Fatalf("invalid flow not reported: %+v", info)
	}
	// An invalid edit of a running flow keeps its last valid version running.
	invalid := cloneDefinition(def)
	invalid.Source.Listen.Port = 9100
	invalid.DeadLetterSink = broken.DeadLetterSink
	s.defs = []spec.FlowDefinition{invalid}
	delete(d.definitions, def.Name) // Runtime crashed while config was invalid.
	c.sync(ctx)
	if d.definitions[def.Name].Source.Listen.Port != 9000 {
		t.Fatal("invalid edit stopped recovery of the last good runtime")
	}
	if info, _, _ := c.RuntimeStatus(ctx, def.Name); info.Error == "" {
		t.Fatal("invalid edit of a running flow not reported")
	}
	// Store outage keeps existing runtimes intact, including uncertain changes.
	s.loadErr = errors.New("unavailable")
	d.definitions["unknown"] = spec.FlowDefinition{Name: "unknown"}
	drifted := def
	drifted.Source.Listen.Port = 9200
	d.definitions[def.Name] = drifted
	c.sync(ctx)
	if _, found := d.definitions["unknown"]; !found || d.definitions[def.Name].Source.Listen.Port != 9200 {
		t.Fatal("store outage caused destructive changes to uncertain runtime state")
	}
}
func TestControllerAddValidatesBeforeSavingAndOwnsDefinitionCopies(t *testing.T) {
	d := &fakeDriver{definitions: map[string]spec.FlowDefinition{}}
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
