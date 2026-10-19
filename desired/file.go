package desired

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/wcrum/gofi/atomicfile"
	"github.com/wcrum/gofi/spec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// File stores Flow resources as YAML. Replacing the file declaratively is
// supported; a failed read never erases the last known desired state.
//
// Every Save and Delete is durable when it returns (atomicfile: temp, fsync,
// rename, fsync dir, backup refreshed). To keep that cheap for large flow
// sets the store caches the snapshot it last read or wrote: a save re-reads
// the file (cheap) and, when the bytes are the ones it last saw, skips
// parsing them; and each flow's encoded YAML item is cached by name, so a
// save re-encodes only the flow that changed and concatenates the rest.
type File struct {
	Path           string
	mu             sync.Mutex
	loadedExisting bool
	// cache is the snapshot behind cacheData, the file content it was
	// decoded from or encoded to. Entries of cache.defs are never mutated in
	// place: Save and Delete build a new slice.
	cacheData []byte
	cache     snapshot
	// items caches each flow's encoded List item (fragment) by name; it is
	// valid for the definitions in cache.defs and reset when the file is
	// re-decoded from disk.
	items map[string]string
}

// snapshot is the decoded flow file: definitions plus the Kubernetes
// metadata each one carried, preserved across saves.
type snapshot struct {
	defs     []spec.FlowDefinition
	metadata map[string]metav1.ObjectMeta
}

type resource struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Metadata   metav1.ObjectMeta   `json:"metadata"`
	Spec       spec.FlowDefinition `json:"spec"`
}
type resourceList struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Items      []resource      `json:"items"`
	Metadata   metav1.ListMeta `json:"metadata,omitempty"`
}

func (f *File) Configuration() Configuration {
	return Configuration{Source: "file", APIVersion: APIVersion, Kind: KindFlow, Path: f.Path}
}

// Load returns every stored flow. The result is the caller's own copy.
func (f *File) Load(_ context.Context) ([]spec.FlowDefinition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap, err := f.load()
	if err != nil {
		return nil, err
	}
	return cloneDefinitions(snap.defs), nil
}

// load reads the file (or its backup) and returns the decoded snapshot,
// which may be the cached one: callers must not mutate it.
func (f *File) load() (snapshot, error) {
	// A corrupted file (torn write, flash error) falls back to the last
	// good save kept by atomicfile; the next Save heals the primary.
	type decoded struct {
		snap snapshot
		data []byte
	}
	result, recovered, err := atomicfile.Decode(f.Path, func(b []byte) (decoded, error) {
		if f.cacheData != nil && bytes.Equal(b, f.cacheData) {
			return decoded{snap: f.cache, data: f.cacheData}, nil
		}
		defs, metadata, err := decodeFlows(b)
		if err != nil {
			return decoded{}, err
		}
		return decoded{snap: snapshot{defs: defs, metadata: metadata}, data: b}, nil
	})
	if os.IsNotExist(err) {
		if f.loadedExisting {
			return snapshot{}, fmt.Errorf("desired configuration disappeared: %w", err)
		}
		return snapshot{defs: []spec.FlowDefinition{}, metadata: map[string]metav1.ObjectMeta{}}, nil
	}
	if err != nil {
		return snapshot{}, err
	}
	if recovered {
		slog.Warn("desired configuration unreadable; recovered from backup", "path", f.Path, "backup", f.Path+atomicfile.BackupSuffix)
	}
	f.loadedExisting = true
	f.remember(result.data, result.snap, false)
	return result.snap, nil
}

// remember installs the snapshot behind data as the cache. fromWrite says
// the fragments in f.items encoded it; a fresh decode invalidates them.
func (f *File) remember(data []byte, snap snapshot, fromWrite bool) {
	if bytes.Equal(data, f.cacheData) && f.cacheData != nil {
		return
	}
	f.cacheData, f.cache = data, snap
	if !fromWrite {
		f.items = nil
	}
}

func cloneDefinitions(defs []spec.FlowDefinition) []spec.FlowDefinition {
	data, err := json.Marshal(defs)
	if err != nil {
		return append([]spec.FlowDefinition(nil), defs...)
	}
	out := make([]spec.FlowDefinition, 0, len(defs))
	if err := json.Unmarshal(data, &out); err != nil {
		return append([]spec.FlowDefinition(nil), defs...)
	}
	return out
}

// DecodeFlows reads Flow definitions from YAML exactly as the file store
// does: a single Flow, a v1 List or FlowList, or multi-document YAML, decoded
// strictly, with metadata.name owning each flow's name. Clients that apply
// files to a controller use it so a file accepted here is accepted there.
func DecodeFlows(data []byte) ([]spec.FlowDefinition, error) {
	defs, _, err := decodeFlows(data)
	return defs, err
}

func decodeFlows(data []byte) ([]spec.FlowDefinition, map[string]metav1.ObjectMeta, error) {
	items, err := decodeResources(data)
	if err != nil {
		return nil, nil, err
	}
	defs := make([]spec.FlowDefinition, 0, len(items))
	seen := map[string]bool{}
	metadata := map[string]metav1.ObjectMeta{}
	for _, item := range items {
		if item.Kind == "Listener" {
			return nil, nil, fmt.Errorf("%q: %s", item.Metadata.Name, errListenerKind)
		}
		if item.APIVersion != APIVersion || item.Kind != KindFlow || item.Metadata.Name == "" {
			return nil, nil, fmt.Errorf("invalid Flow resource")
		}
		if seen[item.Metadata.Name] {
			return nil, nil, fmt.Errorf("duplicate flow %q", item.Metadata.Name)
		}
		seen[item.Metadata.Name] = true
		if item.Spec.Name != "" && item.Spec.Name != item.Metadata.Name {
			return nil, nil, fmt.Errorf("flow %q spec.name must match metadata.name", item.Metadata.Name)
		}
		metadata[item.Metadata.Name] = item.Metadata
		item.Spec.Name = item.Metadata.Name
		// Read parser.decoders as the decode step. A conflict is left in
		// place for the controller to report on that flow alone.
		_ = item.Spec.Normalize()
		defs = append(defs, item.Spec)
	}
	return defs, metadata, nil
}

// EncodeFlow renders one definition as a Flow resource (apiVersion, kind,
// metadata.name, spec) in the YAML the file store writes, without the
// redundant spec.name key.
func EncodeFlow(def spec.FlowDefinition) ([]byte, error) {
	item := resource{APIVersion: APIVersion, Kind: KindFlow, Spec: def}
	item.Metadata.Name = def.Name
	item.Spec.Name = ""
	obj, err := resourceObject(item)
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(obj)
}

// resourceObject converts a resource to a generic object with spec.name dropped.
func resourceObject(item resource) (map[string]any, error) {
	data, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err = json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	if specObj, ok := obj["spec"].(map[string]any); ok {
		delete(specObj, "name")
	}
	return obj, nil
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
			return nil, fmt.Errorf("configuration requires a Flow, List, or FlowList")
		}
		found = true
		switch header.Kind {
		case "Listener", "ListenerList":
			return nil, fmt.Errorf("read desired configuration: %s", errListenerKind)
		case KindFlow:
			var item resource
			if err := yaml.UnmarshalStrict(doc, &item); err != nil {
				return nil, fmt.Errorf("read desired configuration: %w", err)
			}
			items = append(items, item)
		case "List", "FlowList":
			var list resourceList
			if err := yaml.UnmarshalStrict(doc, &list); err != nil {
				return nil, fmt.Errorf("read desired configuration: %w", err)
			}
			if (list.Kind == "List" && list.APIVersion != "v1") || (list.Kind != "List" && list.APIVersion != APIVersion) {
				return nil, fmt.Errorf("unsupported List apiVersion")
			}
			if list.Items == nil {
				return nil, fmt.Errorf("configuration list requires items (use [] for an empty configuration)")
			}
			items = append(items, list.Items...)
		default:
			return nil, fmt.Errorf("configuration requires a Flow, List, or FlowList")
		}
	}
	if !found {
		return nil, fmt.Errorf("configuration is empty; use a List with items: []")
	}
	return items, nil
}

// Save stores def (replacing a flow of the same name) and returns once the
// file is durable on disk.
func (f *File) Save(_ context.Context, def spec.FlowDefinition) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap, err := f.load()
	if err != nil {
		return err
	}
	def = cloneDefinitions([]spec.FlowDefinition{def})[0] // the cache owns its copy
	next := make([]spec.FlowDefinition, 0, len(snap.defs)+1)
	found := false
	for _, existing := range snap.defs {
		if existing.Name == def.Name {
			next = append(next, def)
			found = true
			continue
		}
		next = append(next, existing)
	}
	if !found {
		next = append(next, def)
	}
	return f.write(snapshot{defs: next, metadata: snap.metadata}, def.Name)
}

// Delete removes the named flow (a no-op write when it is absent) and
// returns once the file is durable on disk.
func (f *File) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap, err := f.load()
	if err != nil {
		return err
	}
	next := make([]spec.FlowDefinition, 0, len(snap.defs))
	for _, def := range snap.defs {
		if def.Name != name {
			next = append(next, def)
		}
	}
	return f.write(snapshot{defs: next, metadata: snap.metadata}, "")
}

// write encodes the snapshot as a v1 List and replaces the file atomically.
// changed names the one flow whose definition differs from the cached
// snapshot (every other flow's cached fragment is reused).
func (f *File) write(snap snapshot, changed string) error {
	defs := snap.defs
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	if f.items == nil {
		f.items = make(map[string]string, len(defs))
	}
	delete(f.items, changed)
	var buf bytes.Buffer
	buf.WriteString("apiVersion: v1\n")
	if len(defs) == 0 {
		buf.WriteString("items: []\n")
	} else {
		buf.WriteString("items:\n")
	}
	for _, def := range defs {
		fragment, ok := f.items[def.Name]
		if !ok {
			var err error
			if fragment, err = encodeItem(def, snap.metadata[def.Name]); err != nil {
				return err
			}
			f.items[def.Name] = fragment
		}
		buf.WriteString(fragment)
	}
	buf.WriteString("kind: List\nmetadata: {}\n")
	data := buf.Bytes()
	// Atomic and durable (temp, fsync, rename, fsync dir), keeps the last
	// save as .bak, and skips the write when nothing changed so flash is
	// not rewritten by no-op saves.
	if err := atomicfile.WriteFile(f.Path, data, 0o600); err != nil {
		return err
	}
	f.loadedExisting = true
	for name := range f.items {
		if _, keep := indexOf(defs, name); !keep {
			delete(f.items, name)
		}
	}
	f.remember(data, snap, true)
	return nil
}

func indexOf(defs []spec.FlowDefinition, name string) (int, bool) {
	for i := range defs {
		if defs[i].Name == name {
			return i, true
		}
	}
	return 0, false
}

// encodeItem renders one flow as an item of a YAML List: the Flow resource
// (apiVersion, kind, metadata, spec without the redundant spec.name key)
// marshalled as a block mapping, then indented under "- ". YAML indentation
// is relative, so shifting every line of a block node keeps it valid,
// multi-line scalars included.
func encodeItem(def spec.FlowDefinition, metadata metav1.ObjectMeta) (string, error) {
	item := resource{APIVersion: APIVersion, Kind: KindFlow, Spec: def}
	item.Metadata = metadata
	item.Metadata.Name = def.Name
	item.Spec.Name = ""
	obj, err := resourceObject(item)
	if err != nil {
		return "", err
	}
	data, err := yaml.Marshal(obj)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(len(data) + 2*bytes.Count(data, []byte{'\n'}))
	for i, line := range bytes.SplitAfter(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		switch {
		case i == 0:
			b.WriteString("- ")
		case len(line) == 1: // an empty line inside a literal block stays empty
		default:
			b.WriteString("  ")
		}
		b.Write(line)
	}
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return b.String(), nil
}
