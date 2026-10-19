package history

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/spec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

func flowDef(name string, port int) spec.FlowDefinition {
	return spec.FlowDefinition{Name: name, Source: spec.FlowSource{Listen: &spec.ListenSource{Port: port}}, Parser: spec.ParserCfg{Format: "none"}}
}

func kubeStore() *Kubernetes {
	client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{GVR: "FlowRevisionList"})
	return &Kubernetes{Client: client, Namespace: "gofi"}
}

func stores(t *testing.T) map[string]Store {
	return map[string]Store{"file": &File{Dir: filepath.Join(t.TempDir(), "history")}, "kubernetes": kubeStore()}
}

// memoryFlows stands in for the controller's stored flows.
type memoryFlows struct {
	mu   sync.Mutex
	defs map[string]spec.FlowDefinition
}

func (m *memoryFlows) Get(name string) (spec.FlowDefinition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	def, ok := m.defs[name]
	return def, ok
}
func (m *memoryFlows) List() []spec.FlowDefinition {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []spec.FlowDefinition{}
	for _, def := range m.defs {
		out = append(out, def)
	}
	return out
}
func (m *memoryFlows) set(def spec.FlowDefinition) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defs[def.Name] = def
}
func (m *memoryFlows) remove(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.defs, name)
}

func TestStoresAppendListGetDelete(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			def := flowDef("route.v1", 9000)
			def.Sinks = []spec.SinkCfg{{Type: "http", URL: "https://receiver.example", Headers: map[string]string{"X-Test": "1"}}}
			created := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
			for n := int64(1); n <= 3; n++ {
				rev := Revision{Flow: "route.v1", Number: n, CreatedAt: created, Author: "ada", Message: "change", Source: SourcePortal, Hash: Hash(def), Definition: &def}
				if err := store.Append(ctx, rev); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Append(ctx, Revision{Flow: "route.v1", Number: 2, CreatedAt: created, Source: SourcePortal}); !errors.Is(err, ErrExists) {
				t.Fatalf("overwrite allowed: %v", err)
			}
			if err := store.Append(ctx, Revision{Flow: "other", Number: 1, CreatedAt: created, Source: SourceExternal, Deleted: true}); err != nil {
				t.Fatal(err)
			}
			revs, err := store.List(ctx, "route.v1")
			if err != nil || len(revs) != 3 || revs[0].Number != 1 || revs[2].Number != 3 {
				t.Fatalf("list: %+v %v", revs, err)
			}
			got, err := store.Get(ctx, "route.v1", 2)
			if err != nil || got.Definition == nil || got.Definition.Name != "route.v1" || got.Definition.Sinks[0].Headers["X-Test"] != "1" || !got.CreatedAt.Equal(created) || got.Author != "ada" {
				t.Fatalf("get: %+v %v", got, err)
			}
			if Hash(*got.Definition) != got.Hash {
				t.Fatal("stored definition does not hash to its recorded hash")
			}
			if _, err = store.Get(ctx, "route.v1", 9); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing revision: %v", err)
			}
			flows, err := store.Flows(ctx)
			if err != nil || len(flows) != 2 || flows[0] != "other" || flows[1] != "route.v1" {
				t.Fatalf("flows: %v %v", flows, err)
			}
			if err = store.Delete(ctx, "route.v1", 1, 2, 42); err != nil {
				t.Fatal(err)
			}
			if revs, _ = store.List(ctx, "route.v1"); len(revs) != 1 || revs[0].Number != 3 {
				t.Fatalf("after delete: %+v", revs)
			}
			if err = store.Append(ctx, Revision{Flow: "../escape", Number: 1, CreatedAt: created, Source: SourcePortal}); err == nil {
				t.Fatal("invalid flow name accepted")
			}
		})
	}
}

func TestFileStoreLayout(t *testing.T) {
	dir := t.TempDir()
	store := &File{Dir: dir}
	def := flowDef("route", 9000)
	if err := store.Append(context.Background(), Revision{Flow: "route", Number: 7, CreatedAt: time.Now(), Source: SourcePortal, Definition: &def}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "route", "7.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind: FlowRevision", "apiVersion: gofi.wcrum.dev/v1alpha1", "name: route.r7", "port: 9000"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("revision file lacks %q:\n%s", want, data)
		}
	}
}

func TestKubernetesStoreObjects(t *testing.T) {
	store := kubeStore()
	ctx := context.Background()
	def := flowDef("route", 9000)
	if err := store.Append(ctx, Revision{Flow: "route", Number: 3, CreatedAt: time.Now(), Source: SourcePortal, Definition: &def}); err != nil {
		t.Fatal(err)
	}
	obj, err := store.Client.Resource(GVR).Namespace("gofi").Get(ctx, "route.r3", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if obj.GetLabels()[FlowLabel] != "route" || obj.GetLabels()[RevisionLabel] != "3" || len(obj.GetOwnerReferences()) != 0 {
		t.Fatalf("labels/owners: %v %v", obj.GetLabels(), obj.GetOwnerReferences())
	}
	long := ""
	for len(long) < 70 {
		long += "abcdefghij"
	}
	if flowLabel(long) == long || len(flowLabel(long)) > 63 {
		t.Fatal("long flow names need a digest label")
	}
}

func TestHashIgnoresEmptyCollectionsAndDerivedPython(t *testing.T) {
	a := flowDef("route", 9000)
	b := flowDef("route", 9000)
	b.Stages = []spec.StageSpec{}
	b.Sinks = []spec.SinkCfg{}
	if Hash(a) != Hash(b) {
		t.Fatal("empty collections changed the hash")
	}
	c := flowDef("route", 9001)
	if Hash(a) == Hash(c) {
		t.Fatal("a real change kept the hash")
	}
}

func TestRecorderDedupsAndAttributes(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			flows := &memoryFlows{defs: map[string]spec.FlowDefinition{"route": flowDef("route", 9000)}}
			r := NewRecorder(store, flows, Options{})
			if err := r.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			revs, _ := r.List(ctx, "route", 0)
			if len(revs) != 1 || revs[0].Source != SourceImport || revs[0].Number != 1 || revs[0].Author != UnknownAuthor || revs[0].Definition != nil {
				t.Fatalf("import: %+v", revs)
			}
			// A portal save with an unchanged spec records nothing.
			rev, created, err := r.Apply(ctx, "route", Meta{Source: SourcePortal, Author: "ada"}, func() error { return nil })
			if err != nil || created || rev.Number != 1 {
				t.Fatalf("dedup: %+v %v %v", rev, created, err)
			}
			rev, created, err = r.Apply(ctx, "route", Meta{Source: SourcePortal, Author: "ada", Message: "move port"}, func() error { flows.set(flowDef("route", 9100)); return nil })
			if err != nil || !created || rev.Number != 2 || rev.Source != SourcePortal || rev.Author != "ada" || rev.Message != "move port" {
				t.Fatalf("portal: %+v %v %v", rev, created, err)
			}
			// A failed save records nothing and returns the error.
			boom := errors.New("invalid")
			if _, _, err = r.Apply(ctx, "route", Meta{Source: SourcePortal}, func() error { return boom }); !errors.Is(err, boom) {
				t.Fatal(err)
			}
			// Sync without a change records nothing; an outside edit is external.
			if err = r.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			flows.set(flowDef("route", 9200))
			if err = r.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			revs, _ = r.List(ctx, "route", 0)
			if len(revs) != 3 || revs[0].Source != SourceExternal || revs[0].Number != 3 {
				t.Fatalf("external: %+v", revs)
			}
			if got, _ := r.Get(ctx, "route", 2); got.Definition == nil || got.Definition.Source.Listen.Port != 9100 {
				t.Fatalf("snapshot: %+v", got)
			}
			// A restarted recorder (fresh cache) continues the numbering.
			r2 := NewRecorder(store, flows, Options{})
			flows.set(flowDef("route", 9300))
			if err = r2.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			if revs, _ = r2.List(ctx, "route", 1); len(revs) != 1 || revs[0].Number != 4 || revs[0].Source != SourceExternal {
				t.Fatalf("restart: %+v", revs)
			}
		})
	}
}

func TestRecorderRetention(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			flows := &memoryFlows{defs: map[string]spec.FlowDefinition{}}
			r := NewRecorder(store, flows, Options{Limit: 3})
			for port := 9000; port < 9006; port++ {
				if _, _, err := r.Apply(ctx, "route", Meta{Source: SourcePortal}, func() error { flows.set(flowDef("route", port)); return nil }); err != nil {
					t.Fatal(err)
				}
			}
			revs, err := r.List(ctx, "route", 0)
			if err != nil || len(revs) != 3 || revs[0].Number != 6 || revs[2].Number != 4 {
				t.Fatalf("retention: %+v %v", revs, err)
			}
		})
	}
}

func TestRecorderTombstoneRestoreAndPurge(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			flows := &memoryFlows{defs: map[string]spec.FlowDefinition{"route": flowDef("route", 9000), "keep": flowDef("keep", 9001)}}
			r := NewRecorder(store, flows, Options{DeletedRetention: 48 * time.Hour, Now: func() time.Time { return now }})
			if err := r.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			// Deleted through the portal: a tombstone attributed to the deleter.
			rev, created, err := r.Apply(ctx, "route", Meta{Source: SourcePortal, Author: "ada"}, func() error { flows.remove("route"); return nil })
			if err != nil || !created || !rev.Deleted || rev.Number != 2 || rev.Author != "ada" {
				t.Fatalf("tombstone: %+v %v %v", rev, created, err)
			}
			// Deleted outside GoFi: an external tombstone.
			flows.remove("keep")
			if err = r.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			if revs, _ := r.List(ctx, "keep", 1); len(revs) != 1 || !revs[0].Deleted || revs[0].Source != SourceExternal {
				t.Fatalf("external delete: %+v", revs)
			}
			// Restoring the deleted flow from revision 1.
			if _, _, err = r.Rollback(ctx, "route", 2, Meta{}, func(spec.FlowDefinition) error { return nil }); err == nil {
				t.Fatal("restoring a tombstone accepted")
			}
			rev, created, err = r.Rollback(ctx, "route", 1, Meta{Author: "bob"}, func(def spec.FlowDefinition) error { flows.set(def); return nil })
			if err != nil || !created || rev.Number != 3 || rev.Source != SourceRollback || rev.Message != "Restore revision 1" || rev.Author != "bob" {
				t.Fatalf("restore: %+v %v %v", rev, created, err)
			}
			// After the retention window only the deleted flow's history goes.
			now = now.Add(72 * time.Hour)
			if err = r.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			if revs, _ := r.List(ctx, "keep", 0); len(revs) != 0 {
				t.Fatalf("not purged: %+v", revs)
			}
			if revs, _ := r.List(ctx, "route", 0); len(revs) != 3 {
				t.Fatalf("live flow purged: %+v", revs)
			}
		})
	}
}

type fakeDriver struct {
	mu   sync.Mutex
	defs map[string]spec.FlowDefinition
}

func (d *fakeDriver) Create(_ context.Context, def spec.FlowDefinition) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.defs[def.Name] = def
	return nil
}
func (d *fakeDriver) Delete(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.defs, name)
	return nil
}
func (d *fakeDriver) List(context.Context) ([]spec.FlowDefinition, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []spec.FlowDefinition{}
	for _, def := range d.defs {
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

func TestRollbackThroughController(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ctrl := controller.New(&fakeDriver{defs: map[string]spec.FlowDefinition{}})
	if err := ctrl.WithStore(ctx, &desired.File{Path: filepath.Join(dir, "flows.yaml")}); err != nil {
		t.Fatal(err)
	}
	r := NewRecorder(&File{Dir: filepath.Join(dir, "history")}, ctrl, Options{})
	save := func(def spec.FlowDefinition) error { return ctrl.Add(def) }
	for _, port := range []int{9000, 9100} {
		if _, created, err := r.Apply(ctx, "route", Meta{Source: SourcePortal, Author: "ada"}, func() error { return save(flowDef("route", port)) }); err != nil || !created {
			t.Fatalf("save %d: %v %v", port, created, err)
		}
	}
	// The controller's validation applies to a rollback like any save.
	bad := flowDef("route", 9000)
	bad.Parser.Format = "no-such-format"
	if err := (&File{Dir: filepath.Join(dir, "history")}).Append(ctx, Revision{Flow: "route", Number: 3, CreatedAt: time.Now(), Source: SourceExternal, Hash: Hash(bad), Definition: &bad}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Rollback(ctx, "route", 3, Meta{}, save); err == nil {
		t.Fatal("invalid revision restored")
	}
	rev, created, err := r.Rollback(ctx, "route", 1, Meta{Author: "bob", Message: "back to 9000"}, save)
	if err != nil || !created || rev.Number != 4 || rev.Source != SourceRollback {
		t.Fatalf("rollback: %+v %v %v", rev, created, err)
	}
	if def, _ := ctrl.Get("route"); def.Source.Listen.Port != 9000 {
		t.Fatalf("controller not rolled back: %+v", def)
	}
	// The controller's own store round trip is not an external change.
	if err = r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if revs, _ := r.List(ctx, "route", 0); len(revs) != 4 {
		t.Fatalf("spurious revisions: %+v", revs)
	}
}

func TestAuthorFromHeader(t *testing.T) {
	jwt := func(claims string) string {
		return "Bearer x." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".sig"
	}
	cases := []struct {
		header http.Header
		want   string
	}{
		{http.Header{}, UnknownAuthor},
		{http.Header{"X-Forwarded-User": {"ada"}}, "ada"},
		{http.Header{"Authorization": {jwt(`{"preferred_username":"doug","email":"doug@example.com"}`)}}, "doug"},
		{http.Header{"Authorization": {jwt(`{"email":"eve@example.com"}`)}}, "eve@example.com"},
		{http.Header{"Authorization": {"Bearer not-a-jwt"}}, UnknownAuthor},
		{http.Header{"X-Forwarded-Email": {"a\x00b@example.com"}}, "ab@example.com"},
	}
	for _, c := range cases {
		if got := AuthorFromHeader(c.header); got != c.want {
			t.Errorf("%v: got %q want %q", c.header, got, c.want)
		}
	}
}
