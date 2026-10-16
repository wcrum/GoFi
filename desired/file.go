package desired

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/wcrum/gofi/spec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// File stores Listener resources as YAML. Replacing the file declaratively is
// supported; a failed read never erases the last known desired state.
type File struct {
	Path           string
	mu             sync.Mutex
	loadedExisting bool
	metadata       map[string]metav1.ObjectMeta
}

type resource struct {
	APIVersion string                  `json:"apiVersion"`
	Kind       string                  `json:"kind"`
	Metadata   metav1.ObjectMeta       `json:"metadata"`
	Spec       spec.ListenerDefinition `json:"spec"`
}
type resourceList struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Items      []resource      `json:"items"`
	Metadata   metav1.ListMeta `json:"metadata,omitempty"`
}

func (f *File) Configuration() Configuration {
	return Configuration{Source: "file", APIVersion: APIVersion, Kind: "Listener", Path: f.Path}
}
func (f *File) Load(_ context.Context) ([]spec.ListenerDefinition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load()
}
func (f *File) load() ([]spec.ListenerDefinition, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		if f.loadedExisting {
			return nil, fmt.Errorf("desired configuration disappeared: %w", err)
		}
		return []spec.ListenerDefinition{}, nil
	}
	if err != nil {
		return nil, err
	}
	items, err := decodeResources(data)
	if err != nil {
		return nil, err
	}
	defs := make([]spec.ListenerDefinition, 0, len(items))
	seen := map[string]bool{}
	metadata := map[string]metav1.ObjectMeta{}
	for _, item := range items {
		if item.APIVersion != APIVersion || item.Kind != "Listener" || item.Metadata.Name == "" {
			return nil, fmt.Errorf("invalid Listener resource")
		}
		if seen[item.Metadata.Name] {
			return nil, fmt.Errorf("duplicate Listener %q", item.Metadata.Name)
		}
		seen[item.Metadata.Name] = true
		if item.Spec.Name != "" && item.Spec.Name != item.Metadata.Name {
			return nil, fmt.Errorf("Listener %q spec.name must match metadata.name", item.Metadata.Name)
		}
		metadata[item.Metadata.Name] = item.Metadata
		item.Spec.Name = item.Metadata.Name
		defs = append(defs, item.Spec)
	}
	f.loadedExisting = true
	f.metadata = metadata
	return defs, nil
}

// decodeResources accepts Kubernetes single-resource, List, and multi-document
// YAML exports. Strict decoding prevents a typo from silently dropping config.
func decodeResources(data []byte) ([]resource, error) {
	reader := k8syaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	items := []resource{}
	found := false
	for {
		doc, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read desired configuration: %w", err)
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		var header struct {
			Kind string `json:"kind"`
		}
		if err := yaml.Unmarshal(doc, &header); err != nil {
			return nil, fmt.Errorf("read desired configuration: %w", err)
		}
		if header.Kind == "" {
			return nil, fmt.Errorf("configuration requires a Listener, List, or ListenerList")
		}
		found = true
		switch header.Kind {
		case "Listener":
			var item resource
			if err := yaml.UnmarshalStrict(doc, &item); err != nil {
				return nil, fmt.Errorf("read desired configuration: %w", err)
			}
			items = append(items, item)
		case "List", "ListenerList":
			var list resourceList
			if err := yaml.UnmarshalStrict(doc, &list); err != nil {
				return nil, fmt.Errorf("read desired configuration: %w", err)
			}
			if (list.Kind == "List" && list.APIVersion != "v1") || (list.Kind == "ListenerList" && list.APIVersion != APIVersion) {
				return nil, fmt.Errorf("unsupported List apiVersion")
			}
			if list.Items == nil {
				return nil, fmt.Errorf("configuration list requires items (use [] for an empty configuration)")
			}
			items = append(items, list.Items...)
		default:
			return nil, fmt.Errorf("configuration requires a Listener, List, or ListenerList")
		}
	}
	if !found {
		return nil, fmt.Errorf("configuration is empty; use a List with items: []")
	}
	return items, nil
}

func (f *File) Save(_ context.Context, def spec.ListenerDefinition) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	defs, err := f.load()
	if err != nil {
		return err
	}
	found := false
	for i := range defs {
		if defs[i].Name == def.Name {
			defs[i] = def
			found = true
			break
		}
	}
	if !found {
		defs = append(defs, def)
	}
	return f.write(defs)
}
func (f *File) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	defs, err := f.load()
	if err != nil {
		return err
	}
	out := defs[:0]
	for _, def := range defs {
		if def.Name != name {
			out = append(out, def)
		}
	}
	return f.write(out)
}
func (f *File) write(defs []spec.ListenerDefinition) error {
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	list := resourceList{APIVersion: "v1", Kind: "List", Items: make([]resource, 0, len(defs))}
	for _, def := range defs {
		item := resource{APIVersion: APIVersion, Kind: "Listener", Spec: def}
		item.Metadata = f.metadata[def.Name]
		item.Metadata.Name = def.Name
		item.Spec.Name = ""
		list.Items = append(list.Items, item)
	}
	// Drop the redundant spec.name key; the Kubernetes resource name owns it.
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err = json.Unmarshal(data, &obj); err != nil {
		return err
	}
	for _, v := range obj["items"].([]any) {
		delete(v.(map[string]any)["spec"].(map[string]any), "name")
	}
	data, err = yaml.Marshal(obj)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(f.Path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), ".gofi-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), f.Path); err != nil {
		return err
	}
	f.loadedExisting = true
	return nil
}
