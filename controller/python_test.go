package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/wcrum/gofi/spec"
)

type bindingDriver struct {
	*fakeDriver
	current bool
	err     error
}

func (d *bindingDriver) PythonBindingsCurrent(context.Context, spec.FlowDefinition) (bool, error) {
	return d.current, d.err
}

func TestPythonBindingRefreshPreservesNewListenersAndRestartsOnlyStaleOnes(t *testing.T) {
	ctx := context.Background()
	def := controllerDef()
	def.Parser = spec.ParserCfg{Format: "python", Python: &spec.PythonRef{Library: "library", Function: "parse"}}
	d := &bindingDriver{fakeDriver: &fakeDriver{definitions: map[string]spec.FlowDefinition{def.Name: def}}, current: true}
	c := New(d)
	c.desired[def.Name] = def
	// The first watcher tick used to delete even a freshly created listener.
	c.NotifyRuntimeChange()
	c.sync(ctx)
	if d.creates != 0 || d.deletes != 0 {
		t.Fatal("current bindings restarted a fresh listener")
	}
	d.err = errors.New("worker starting")
	d.current = false
	c.sync(ctx)
	if d.deletes != 0 {
		t.Fatal("unavailable bindings deleted a working listener")
	}
	d.err = nil
	c.sync(ctx)
	if d.deletes != 1 || d.creates != 1 {
		t.Fatal("rotated bindings did not restart the stale listener")
	}
	d.current = true
	c.sync(ctx)
	if d.deletes != 1 || d.creates != 1 {
		t.Fatal("updated bindings restarted the listener again")
	}
}
