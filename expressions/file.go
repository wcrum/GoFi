package expressions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const (
	APIVersion = "gofi.wcrum.dev/v1alpha1"
	Kind       = "CelExpression"
)

// FileStore keeps CelExpression resources in one YAML List file, the same
// shape `kubectl get celexpressions -o yaml` produces.
type FileStore struct {
	Path   string
	mu     sync.Mutex
	loaded bool
}

type resource struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   metav1.ObjectMeta `json:"metadata"`
	Spec       Expression        `json:"spec"`
}
type resourceList struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Items      []resource `json:"items"`
}

func (f *FileStore) load() ([]Expression, error) {
	b, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) && !f.loaded {
		return []Expression{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list resourceList
	if err = yaml.UnmarshalStrict(b, &list); err != nil {
		var item resource
		if e := yaml.UnmarshalStrict(b, &item); e != nil {
			return nil, err
		}
		list = resourceList{Kind: "List", Items: []resource{item}}
	}
	if list.Kind != "List" && list.Kind != "CelExpressionList" {
		return nil, fmt.Errorf("expected CelExpression or List resources")
	}
	if list.Items == nil {
		return nil, fmt.Errorf("CelExpression list requires items (use [] for an empty list)")
	}
	out := []Expression{}
	seen := map[string]bool{}
	for _, r := range list.Items {
		if r.APIVersion != APIVersion || r.Kind != Kind || r.Metadata.Name == "" {
			return nil, fmt.Errorf("invalid CelExpression resource")
		}
		if r.Spec.Name != "" && r.Spec.Name != r.Metadata.Name {
			return nil, fmt.Errorf("CelExpression spec.name must match metadata.name")
		}
		r.Spec.Name = r.Metadata.Name
		if seen[r.Spec.Name] {
			return nil, fmt.Errorf("duplicate CelExpression %s", r.Spec.Name)
		}
		seen[r.Spec.Name] = true
		e, err := Normalize(r.Spec)
		if err != nil {
			return nil, err
		}
		if e.Generation == 0 {
			e.Generation = 1
		}
		out = append(out, e)
	}
	f.loaded = true
	return out, nil
}

func (f *FileStore) Load(context.Context) ([]Expression, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load()
}

func (f *FileStore) write(items []Expression) error {
	list := resourceList{APIVersion: "v1", Kind: "List", Items: []resource{}}
	for _, e := range items {
		r := resource{APIVersion: APIVersion, Kind: Kind, Spec: e}
		r.Metadata.Name = e.Name
		r.Spec.Name = ""
		list.Items = append(list.Items, r)
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	if b, err = yaml.JSONToYAML(b); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(f.Path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), ".gofi-cel-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), f.Path); err != nil {
		return err
	}
	f.loaded = true
	return nil
}

func (f *FileStore) Save(_ context.Context, e Expression) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	items, err := f.load()
	if err != nil {
		return err
	}
	found := false
	for i := range items {
		if items[i].Name == e.Name {
			items[i] = e
			found = true
		}
	}
	if !found {
		items = append(items, e)
	}
	return f.write(items)
}

func (f *FileStore) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	items, err := f.load()
	if err != nil {
		return err
	}
	out := []Expression{}
	for _, e := range items {
		if e.Name != name {
			out = append(out, e)
		}
	}
	return f.write(out)
}
