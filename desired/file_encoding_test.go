package desired

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/wcrum/gofi/spec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// referenceEncode is the whole-list encoder the store used before per-flow
// fragments: marshal the List, drop spec.name, marshal as YAML.
func referenceEncode(defs []spec.FlowDefinition, metadata map[string]metav1.ObjectMeta) []byte {
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	list := resourceList{APIVersion: "v1", Kind: "List", Items: make([]resource, 0, len(defs))}
	for _, def := range defs {
		item := resource{APIVersion: APIVersion, Kind: KindFlow, Spec: def}
		item.Metadata = metadata[def.Name]
		item.Metadata.Name = def.Name
		item.Spec.Name = ""
		list.Items = append(list.Items, item)
	}
	data, _ := json.Marshal(list)
	var obj map[string]any
	_ = json.Unmarshal(data, &obj)
	for _, v := range obj["items"].([]any) {
		delete(v.(map[string]any)["spec"].(map[string]any), "name")
	}
	data, _ = yaml.Marshal(obj)
	return data
}

// awkwardFlows are definitions whose YAML needs quoting, literal blocks,
// leading spaces, and nested maps: everything the fragment indentation must
// keep valid.
func awkwardFlows() []spec.FlowDefinition {
	return []spec.FlowDefinition{
		{
			Name:    "z-last",
			Source:  spec.FlowSource{Listen: &spec.ListenSource{Port: 7001, Network: "tcp"}},
			Framing: spec.Framing{Mode: "line"},
			Parser:  spec.ParserCfg{Format: "python", Python: &spec.PythonRef{Function: "parse", InlineSource: "def parse(raw, meta=None):\n    # leading spaces and a colon: here\n    return {\"a\": 1}\n\n\n", Config: map[string]any{"depth": 2, "names": []any{"a", "b"}, "nested": map[string]any{"k": "v: w"}}}},
			Stages: []spec.StageSpec{
				{ID: "f", Kind: "filter", Type: "cel", Expression: `fields["x"] == "a: b" && fields["y"] != '- c'`},
				{ID: "t", Kind: "transform", TransformOp: "set", Field: "note", ValueExpr: "\"  starts with spaces\\nand a newline\""},
			},
			Sinks: []spec.SinkCfg{{Type: "http", URL: "https://receiver.example/ingest?x=1#frag", Headers: map[string]string{"X-Multi": "line one\nline two", "X-Colon": "a: b", "": "empty key"}}},
		},
		{
			Name:      "a-first",
			Source:    spec.FlowSource{Listen: &spec.ListenSource{Port: 7002, Network: "udp"}},
			Parser:    spec.ParserCfg{Format: "none"},
			Sinks:     []spec.SinkCfg{{Type: "stdout", When: "true"}},
			TestCases: []spec.TestCase{{ID: "1", Name: "yes", Raw: "yes"}, {ID: "2", Name: "tabs", Raw: "\t{\"a\":\t1}\r\n"}, {ID: "3", Name: "yaml-ish", Raw: "- item\n- item2\nkey: value"}},
			State:     spec.StateStopped,
		},
		{
			Name:   "m-middle",
			Source: spec.FlowSource{Fetch: &spec.FetchSource{HTTP: &spec.HTTPFetch{URL: "http://api.example", Body: "  indented body\n    deeper\nback"}}},
			Parser: spec.ParserCfg{Format: "json"},
		},
	}
}

// TestFragmentEncodingMatchesWholeListEncoding pins the on-disk shape: a
// file assembled from per-flow fragments is byte-identical to the one the
// whole-list encoder produced, so upgrading rewrites nothing.
func TestFragmentEncodingMatchesWholeListEncoding(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "listeners.yaml")
	store := &File{Path: path}
	defs := awkwardFlows()
	metadata := map[string]metav1.ObjectMeta{"z-last": {Labels: map[string]string{"team": "a"}, Annotations: map[string]string{"note": "multi\nline"}}}
	for _, def := range defs {
		if err := store.Save(ctx, def); err != nil {
			t.Fatal(err)
		}
	}
	// Give one flow metadata through the file, as kubectl apply would, then
	// save again so the store carries it.
	withMeta := referenceEncode(defs, metadata)
	if err := os.WriteFile(path, withMeta, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, defs[1]); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(withMeta) {
		t.Fatalf("fragment encoding differs from the whole-list encoding:\n--- got\n%s\n--- want\n%s", got, withMeta)
	}
	// Empty list.
	for _, def := range defs {
		if err := store.Delete(ctx, def.Name); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = os.ReadFile(path)
	if want := referenceEncode(nil, nil); string(got) != string(want) {
		t.Fatalf("empty list:\n%s\nwant\n%s", got, want)
	}
}

// TestFileRoundTripsAwkwardContent decodes what the fragment encoder wrote
// and compares it with the saved definitions.
func TestFileRoundTripsAwkwardContent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "listeners.yaml")
	store := &File{Path: path}
	defs := awkwardFlows()
	for _, def := range defs {
		if err := store.Save(ctx, def); err != nil {
			t.Fatal(err)
		}
	}
	for _, reader := range []*File{store, {Path: path}} {
		got, err := reader.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
		if len(got) != len(defs) {
			t.Fatalf("got %d flows, want %d", len(got), len(defs))
		}
		for i := range defs {
			want, _ := json.Marshal(defs[i])
			have, _ := json.Marshal(got[i])
			if string(want) != string(have) {
				t.Fatalf("flow %s changed in the round trip:\n%s\n%s", defs[i].Name, have, want)
			}
		}
	}
}

// TestFileCacheFollowsExternalEdits: a save after an outside edit of the
// file (hand edit, kubectl-style apply) keeps that edit, and Load never
// returns the cache's own slice.
func TestFileCacheFollowsExternalEdits(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "listeners.yaml")
	store := &File{Path: path}
	if err := store.Save(ctx, flow("a", 7001)); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, flow("b", 7002)); err != nil {
		t.Fatal(err)
	}
	// Outside edit: b moves to another port and c appears.
	external := referenceEncode([]spec.FlowDefinition{flow("a", 7001), flow("b", 7999), flow("c", 7003)}, nil)
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, flow("d", 7004)); err != nil {
		t.Fatal(err)
	}
	got, err := (&File{Path: path}).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ports := map[string]int{}
	for _, def := range got {
		ports[def.Name] = def.Source.Listen.Port
	}
	if !reflect.DeepEqual(ports, map[string]int{"a": 7001, "b": 7999, "c": 7003, "d": 7004}) {
		t.Fatalf("external edit lost: %v", ports)
	}
	// Mutating a Load result must not change what the store saves next.
	loaded, _ := store.Load(ctx)
	loaded[0].Source.Listen.Port = 1
	if err := store.Save(ctx, flow("e", 7005)); err != nil {
		t.Fatal(err)
	}
	again, _ := (&File{Path: path}).Load(ctx)
	for _, def := range again {
		if def.Source.Listen.Port == 1 {
			t.Fatal("a caller's mutation of a Load result reached the file")
		}
	}
}
