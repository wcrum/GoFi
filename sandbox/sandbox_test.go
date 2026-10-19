package sandbox

import (
	"context"
	"github.com/wcrum/gofi/atomicfile"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeBackend struct {
	starts, stops int
	last          Definition
}

func (f *fakeBackend) Start(_ context.Context, d Definition, token string) (string, error) {
	f.starts++
	f.last = d
	return "http://worker:8080", nil
}
func (f *fakeBackend) Stop(context.Context, string) error { f.stops++; return nil }
func TestDurableReconciliationAndDeletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "libraries.yaml")
	b := &fakeBackend{}
	s := New(b, &FileStore{Path: path})
	def := Definition{Name: "library", SourceCode: "import gofi\n@gofi.parser\ndef decode(raw): return {}"}
	if e := s.Apply(ctx, def); e != nil {
		t.Fatal(e)
	}
	bindings, _ := s.Bindings(ctx)
	if len(bindings[def.Name].Token) != 64 || bindings[def.Name].TimeoutSeconds != 5 {
		t.Fatalf("invalid normalized runtime binding: %#v", bindings)
	}
	stat, e := os.Stat(path)
	if e != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("library source persistence must be private: %v %v", stat, e)
	}
	restarted := New(b, &FileStore{Path: path})
	if e = restarted.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	if b.starts != 2 {
		t.Fatal("restart must recreate isolated workers")
	}
	if e = restarted.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	if b.starts != 2 {
		t.Fatal("unchanged desired state must not restart worker")
	}
	if e = restarted.Delete(ctx, def.Name); e != nil {
		t.Fatal(e)
	}
	if len(restarted.List()) != 0 || b.stops != 1 {
		t.Fatal("worker deletion did not clear runtime state")
	}
	defs, e := (&FileStore{Path: path}).Load(ctx)
	if e != nil || len(defs) != 0 {
		t.Fatalf("delete did not persist: %v %v", defs, e)
	}
}
func TestInvalidDefinitionCannotStartWorker(t *testing.T) {
	b := &fakeBackend{}
	s := New(b, nil)
	for _, d := range []Definition{{Name: "../escape", SourceCode: "x"}, {Name: "ok", SourceCode: "x", MemoryMiB: 1}, {Name: "ok", SourceCode: "x", TimeoutSeconds: 31}, {Name: "ok"}} {
		if e := s.Apply(context.Background(), d); e == nil {
			t.Fatalf("invalid definition accepted: %#v", d)
		}
	}
	if b.starts != 0 {
		t.Fatal("invalid configuration reached execution backend")
	}
}
func TestDisappearingDesiredStateDoesNotEraseWorkers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "libraries.yaml")
	b := &fakeBackend{}
	s := New(b, &FileStore{Path: path})
	if e := s.Apply(ctx, Definition{Name: "library", SourceCode: "x"}); e != nil {
		t.Fatal(e)
	}
	// A missing primary is served from the backup of the last save; with
	// every copy gone the store reports an error rather than "no libraries".
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := s.Reconcile(ctx); e != nil {
		t.Fatalf("missing primary must be served from its backup: %v", e)
	}
	for _, suffix := range []string{atomicfile.BackupSuffix, atomicfile.PreviousSuffix} {
		_ = os.Remove(path + suffix)
	}
	if e := s.Reconcile(ctx); e == nil {
		t.Fatal("expected missing desired state error")
	}
	if b.stops != 0 || len(s.List()) != 1 {
		t.Fatal("a transient missing file erased active libraries")
	}
}

func TestWorkerExitedNamesTheException(t *testing.T) {
	err := workerExited(1, "Traceback (most recent call last):\n  File \"<gofi-library>\", line 5\nAttributeError: @gofi.ingress was removed\n\n")
	if err == nil || err.Error() != "worker exited with code 1: AttributeError: @gofi.ingress was removed" {
		t.Fatalf("got %v", err)
	}
	if err := workerExited(1, "\x1b[31mValueError: "+strings.Repeat("x", 600)); !strings.HasPrefix(err.Error(), "worker exited with code 1: [31mValueError: x") || len(err.Error()) > 560 {
		t.Fatalf("control characters or length not bounded: %q", err)
	}
	if err := workerExited(137, ""); err.Error() != "worker exited with code 137" {
		t.Fatalf("got %v", err)
	}
}
