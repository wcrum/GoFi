package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/wcrum/gofi/atomicfile"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

type FileStore struct {
	Path   string
	mu     sync.Mutex
	loaded bool
}
type libraryResource struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   metav1.ObjectMeta `json:"metadata"`
	Spec       Definition        `json:"spec"`
}
type libraryList struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Items      []libraryResource `json:"items"`
}

// load reads the file, or the backup of the last save when the primary is
// missing or unreadable (atomicfile); the next write heals the primary.
func (f *FileStore) load() ([]Definition, error) {
	out, recovered, err := atomicfile.Decode(f.Path, decodeLibraries)
	if os.IsNotExist(err) && !f.loaded {
		return []Definition{}, nil
	}
	if err != nil {
		return nil, err
	}
	if recovered {
		slog.Warn("sandbox libraries unreadable; recovered from backup", "path", f.Path, "backup", f.Path+atomicfile.BackupSuffix)
	}
	f.loaded = true
	return out, nil
}

func decodeLibraries(b []byte) ([]Definition, error) {
	var err error
	var list libraryList
	if err = yaml.UnmarshalStrict(b, &list); err != nil {
		var item libraryResource
		if e := yaml.UnmarshalStrict(b, &item); e != nil {
			return nil, err
		}
		list = libraryList{Kind: "List", Items: []libraryResource{item}}
	}
	if list.Kind != "List" && list.Kind != "SandboxList" {
		return nil, fmt.Errorf("expected Sandbox or List resources")
	}
	if list.Items == nil {
		return nil, fmt.Errorf("Sandbox list requires items (use [] for an empty list)")
	}
	out := []Definition{}
	seen := map[string]bool{}
	for _, r := range list.Items {
		if r.APIVersion != "gofi.wcrum.dev/v1alpha1" || r.Kind != "Sandbox" || r.Metadata.Name == "" {
			return nil, fmt.Errorf("invalid Sandbox resource")
		}
		if r.Spec.Name != "" && r.Spec.Name != r.Metadata.Name {
			return nil, fmt.Errorf("Sandbox spec.name must match metadata.name")
		}
		r.Spec.Name = r.Metadata.Name
		if seen[r.Spec.Name] {
			return nil, fmt.Errorf("duplicate Sandbox %s", r.Spec.Name)
		}
		seen[r.Spec.Name] = true
		d, e := Normalize(r.Spec)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}
func (f *FileStore) Load(context.Context) ([]Definition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load()
}
func (f *FileStore) write(defs []Definition) error {
	list := libraryList{APIVersion: "v1", Kind: "List", Items: []libraryResource{}}
	for _, d := range defs {
		r := libraryResource{APIVersion: "gofi.wcrum.dev/v1alpha1", Kind: "Sandbox", Spec: d}
		r.Metadata.Name = d.Name
		r.Spec.Name = ""
		list.Items = append(list.Items, r)
	}
	b, e := json.Marshal(list)
	if e != nil {
		return e
	}
	b, e = yaml.JSONToYAML(b)
	if e != nil {
		return e
	}
	// Atomic + fsync, keeps .bak, skips unchanged content (see atomicfile).
	if e = atomicfile.WriteFile(f.Path, b, 0o600); e != nil {
		return e
	}
	f.loaded = true
	return nil
}
func (f *FileStore) Save(_ context.Context, d Definition) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	defs, e := f.load()
	if e != nil {
		return e
	}
	found := false
	for i := range defs {
		if defs[i].Name == d.Name {
			defs[i] = d
			found = true
		}
	}
	if !found {
		defs = append(defs, d)
	}
	return f.write(defs)
}
func (f *FileStore) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	defs, e := f.load()
	if e != nil {
		return e
	}
	out := []Definition{}
	for _, d := range defs {
		if d.Name != name {
			out = append(out, d)
		}
	}
	return f.write(out)
}
