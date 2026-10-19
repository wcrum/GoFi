package desired

import (
	"context"
	"github.com/wcrum/gofi/atomicfile"
	"os"
	"path/filepath"
	"testing"

	"github.com/wcrum/gofi/spec"
)

func TestFileRoundTripAndMissingConfiguration(t *testing.T) {
	f := &File{Path: filepath.Join(t.TempDir(), "listeners.yaml")}
	ctx := context.Background()
	if defs, err := f.Load(ctx); err != nil || len(defs) != 0 {
		t.Fatalf("first missing file should initialize empty: %v", err)
	}
	def := spec.FlowDefinition{Name: "ingest", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000}}, Parser: spec.ParserCfg{Format: "none"}}
	if err := f.Save(ctx, def); err != nil {
		t.Fatal(err)
	}
	defs, err := f.Load(ctx)
	if err != nil || len(defs) != 1 || defs[0].Name != "ingest" {
		t.Fatalf("failed round trip: %+v %v", defs, err)
	}
	// A primary that disappears is served from the backup of the last save;
	// with every copy gone the store refuses rather than reporting "no flows".
	if err := os.Remove(f.Path); err != nil {
		t.Fatal(err)
	}
	if defs, err := f.Load(ctx); err != nil || len(defs) != 1 {
		t.Fatalf("disappearing primary must be served from its backup: %v %v", defs, err)
	}
	for _, suffix := range []string{atomicfile.BackupSuffix, atomicfile.PreviousSuffix} {
		_ = os.Remove(f.Path + suffix)
	}
	if _, err := f.Load(ctx); err == nil {
		t.Fatal("disappearing configuration must not erase live desired state")
	}
}

func TestFileReadsSingleDeclarativeResourceAndRejectsDuplicates(t *testing.T) {
	f := &File{Path: filepath.Join(t.TempDir(), "listeners.yaml")}
	ctx := context.Background()
	raw := `apiVersion: gofi.wcrum.dev/v1alpha1
kind: Flow
metadata:
  name: declarative
spec:
  source:
    listen:
      port: 9000
  framing:
    mode: line
  parser:
    format: json
`
	if err := os.WriteFile(f.Path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	defs, err := f.Load(ctx)
	if err != nil || len(defs) != 1 || defs[0].Name != "declarative" {
		t.Fatalf("single manifest: %+v %v", defs, err)
	}
	if err := f.Save(ctx, defs[0]); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(ctx, "declarative"); err != nil {
		t.Fatal(err)
	}
	defs, err = f.Load(ctx)
	if err != nil || len(defs) != 0 {
		t.Fatalf("empty List must remain readable: %+v %v", defs, err)
	}
}

func TestFilePreservesKubernetesMetadataAndReadsEveryDocument(t *testing.T) {
	f := &File{Path: filepath.Join(t.TempDir(), "listeners.yaml")}
	raw := `apiVersion: gofi.wcrum.dev/v1alpha1
kind: Flow
metadata:
  name: first
  namespace: gofi
  labels:
    app.kubernetes.io/managed-by: gitops
  annotations:
    owner: platform
spec:
  source: {listen: {port: 9000}}
---
apiVersion: gofi.wcrum.dev/v1alpha1
kind: Flow
metadata: {name: second}
spec:
  source: {listen: {port: 9001}}
`
	if err := os.WriteFile(f.Path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	defs, err := f.Load(ctx)
	if err != nil || len(defs) != 2 {
		t.Fatalf("multidocument load: %+v %v", defs, err)
	}
	defs[0].Source.Listen.Port = 9100
	if err := f.Save(ctx, defs[0]); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	items, err := decodeResources(data)
	if err != nil || len(items) != 2 {
		t.Fatalf("saved list: %+v %v", items, err)
	}
	metadata := items[0].Metadata
	if metadata.Namespace != "gofi" || metadata.Labels["app.kubernetes.io/managed-by"] != "gitops" || metadata.Annotations["owner"] != "platform" {
		t.Fatalf("metadata lost: %+v", metadata)
	}
}
func TestFileRejectsInvalidOrDuplicateSnapshots(t *testing.T) {
	for _, raw := range []string{
		`apiVersion: gofi.wcrum.dev/v1alpha1
kind: Flow
metadata: {name: route}
spec: {name: other}
`,
		`apiVersion: v1
kind: List
items:
- {apiVersion: gofi.wcrum.dev/v1alpha1, kind: Flow, metadata: {name: route}, spec: {}}
- {apiVersion: gofi.wcrum.dev/v1alpha1, kind: Flow, metadata: {name: route}, spec: {}}
`,
		`apiVersion: gofi.wcrum.dev/v1alpha1
kind: Listener
metadata: {name: retired}
spec: {source: {port: 9000}}
`,
		`apiVersion: v1
kind: List
items: []
---
apiVersion: gofi.wcrum.dev/v1alpha1
kind: Flow
metadata: {name: broken}
spec: {typo: true}
`,
	} {
		f := &File{Path: filepath.Join(t.TempDir(), "listeners.yaml")}
		if err := os.WriteFile(f.Path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Load(context.Background()); err == nil {
			t.Fatalf("accepted invalid snapshot: %s", raw)
		}
	}
}
