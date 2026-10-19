package desired

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wcrum/gofi/atomicfile"
	"github.com/wcrum/gofi/spec"
)

func flow(name string, port int) spec.FlowDefinition {
	return spec.FlowDefinition{Name: name, Source: spec.FlowSource{Listen: &spec.ListenSource{Network: "tcp", Port: port}}}
}

// TestFileSurvivesCorruptionAndRestart: a torn/garbled listeners.yaml (power
// loss on flash) falls back to the .bak of the last acknowledged save, a
// restart sees every saved flow, and the next save heals the primary.
func TestFileSurvivesCorruptionAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gofi", "listeners.yaml")
	store := &File{Path: path}
	if err := store.Save(ctx, flow("a", 7001)); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, flow("b", 7002)); err != nil {
		t.Fatal(err)
	}
	primary, _ := os.ReadFile(path)
	if bak, err := os.ReadFile(path + atomicfile.BackupSuffix); err != nil || string(bak) != string(primary) {
		t.Fatalf("backup must equal the last acknowledged save: %v", err)
	}

	// Garble the primary mid-file.
	data, _ := os.ReadFile(path)
	torn := append([]byte{}, data[:len(data)/2]...)
	torn = append(torn, []byte("\x00\x00garbage: [")...)
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}

	// Fresh process: load recovers from the backup (both flows: the last
	// acknowledged save) instead of failing or erasing everything.
	restarted := &File{Path: path}
	defs, err := restarted.Load(ctx)
	if err != nil {
		t.Fatalf("load after corruption: %v", err)
	}
	if len(defs) != 2 || defs[0].Name != "a" || defs[1].Name != "b" {
		t.Fatalf("recovered %v, want flows a and b", defs)
	}

	// Saving heals the primary; a plain read of the file now parses.
	if err := restarted.Save(ctx, flow("c", 7003)); err != nil {
		t.Fatal(err)
	}
	healed, _ := os.ReadFile(path)
	if _, err := decodeResources(healed); err != nil {
		t.Fatalf("primary not healed: %v", err)
	}
	if defs, err = (&File{Path: path}).Load(ctx); err != nil || len(defs) != 3 {
		t.Fatalf("after heal: %v %v", defs, err)
	}
}

func TestFileUnchangedSaveDoesNotRewrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "listeners.yaml")
	store := &File{Path: path}
	if err := store.Save(ctx, flow("a", 7001)); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if err := store.Save(ctx, flow("a", 7001)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("identical save rewrote the file (flash wear)")
	}
	if _, err := os.Stat(path + atomicfile.PreviousSuffix); err == nil {
		t.Fatal("identical save must not rotate the backups")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestFileBothCopiesCorruptFailsLoudly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "listeners.yaml")
	store := &File{Path: path}
	_ = store.Save(ctx, flow("a", 7001))
	_ = store.Save(ctx, flow("b", 7002))
	_ = os.WriteFile(path, []byte("kind: Nope\n"), 0o600)
	_ = os.WriteFile(path+atomicfile.BackupSuffix, []byte("{{{"), 0o600)
	_ = os.WriteFile(path+atomicfile.PreviousSuffix, []byte("{{{"), 0o600)
	if _, err := (&File{Path: path}).Load(ctx); err == nil || !strings.Contains(err.Error(), "backup also invalid") {
		t.Fatalf("expected a loud failure naming both copies, got %v", err)
	}
}
