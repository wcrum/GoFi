package desired

import (
	"context"
	"testing"

	"github.com/wcrum/gofi/spec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

func kubeStore() *Kubernetes {
	client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{FlowGVR: "FlowList"})
	return &Kubernetes{Client: client, Namespace: "gofi"}
}
func TestKubernetesPreservesMetadataAndLoadsGitOpsUpdates(t *testing.T) {
	ctx := context.Background()
	store := kubeStore()
	resource := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": APIVersion, "kind": KindFlow,
		"metadata": map[string]any{"name": "route", "namespace": "gofi", "resourceVersion": "17", "labels": map[string]any{"app.kubernetes.io/managed-by": "gitops"}, "annotations": map[string]any{"owner": "platform"}},
		"spec":     map[string]any{"source": map[string]any{"listen": map[string]any{"port": int64(9000)}}, "parser": map[string]any{"format": "none"}},
	}}
	resources := store.Client.Resource(FlowGVR).Namespace(store.Namespace)
	if _, err := resources.Create(ctx, resource, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defs, err := store.Load(ctx)
	if err != nil || len(defs) != 1 || defs[0].Name != "route" {
		t.Fatalf("load: %+v %v", defs, err)
	}
	defs[0].Source.Listen.Port = 9100
	if err := store.Save(ctx, defs[0]); err != nil {
		t.Fatal(err)
	}
	saved, err := resources.Get(ctx, "route", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if saved.GetResourceVersion() != "17" || saved.GetLabels()["app.kubernetes.io/managed-by"] != "gitops" || saved.GetAnnotations()["owner"] != "platform" {
		t.Fatalf("metadata overwritten: %+v", saved.Object["metadata"])
	}
	if _, exists, _ := unstructured.NestedString(saved.Object, "spec", "name"); exists {
		t.Fatal("redundant spec.name persisted")
	}
	// An external declarative edit must be observed on the next inventory load.
	if err := unstructured.SetNestedField(saved.Object, int64(9200), "spec", "source", "listen", "port"); err != nil {
		t.Fatal(err)
	}
	saved.SetResourceVersion("18")
	if _, err := resources.Update(ctx, saved, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	defs, err = store.Load(ctx)
	if err != nil || defs[0].Source.Listen.Port != 9200 {
		t.Fatalf("GitOps edit not reloaded: %+v %v", defs, err)
	}
	if err := store.Delete(ctx, "route"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "route"); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
	defs, err = store.Load(ctx)
	if err != nil || len(defs) != 0 {
		t.Fatalf("delete: %+v %v", defs, err)
	}
}
func TestKubernetesCreateAndRejectConflictingResourceName(t *testing.T) {
	ctx := context.Background()
	store := kubeStore()
	def := spec.FlowDefinition{Name: "route", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 9000}}, Parser: spec.ParserCfg{Format: "none"}}
	if err := store.Save(ctx, def); err != nil {
		t.Fatal(err)
	}
	resources := store.Client.Resource(FlowGVR).Namespace(store.Namespace)
	item, err := resources.Get(ctx, "route", metav1.GetOptions{})
	if err != nil || item.GetNamespace() != "gofi" || item.GetAPIVersion() != APIVersion {
		t.Fatalf("create: %+v %v", item, err)
	}
	if err := unstructured.SetNestedField(item.Object, "another", "spec", "name"); err != nil {
		t.Fatal(err)
	}
	if _, err := resources.Update(ctx, item, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx); err == nil {
		t.Fatal("conflicting spec.name accepted")
	}
}

// A Flow written before the decode step (spec.parser.decoders) loads as
// spec.decode and is saved back in the current shape.
func TestKubernetesLoadsLegacyParserDecoders(t *testing.T) {
	ctx := context.Background()
	store := kubeStore()
	resource := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": APIVersion, "kind": KindFlow,
		"metadata": map[string]any{"name": "route", "namespace": "gofi"},
		"spec":     map[string]any{"source": map[string]any{"listen": map[string]any{"port": int64(9000)}}, "parser": map[string]any{"format": "json", "decoders": []any{"base64"}, "maxDecodedBytes": int64(512)}},
	}}
	resources := store.Client.Resource(FlowGVR).Namespace(store.Namespace)
	if _, err := resources.Create(ctx, resource, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defs, err := store.Load(ctx)
	if err != nil || len(defs) != 1 || defs[0].Decode == nil || defs[0].Decode.Decoders[0].Type != "base64" || defs[0].Decode.MaxDecodedBytes != 512 || defs[0].Parser.LegacyDecoders != nil {
		t.Fatalf("load: %+v %v", defs, err)
	}
	if err := store.Save(ctx, defs[0]); err != nil {
		t.Fatal(err)
	}
	saved, _ := resources.Get(ctx, "route", metav1.GetOptions{})
	if _, legacy, _ := unstructured.NestedFieldNoCopy(saved.Object, "spec", "parser", "decoders"); legacy {
		t.Fatalf("saved legacy shape: %v", saved.Object["spec"])
	}
	if steps, ok, _ := unstructured.NestedSlice(saved.Object, "spec", "decode", "decoders"); !ok || len(steps) != 1 {
		t.Fatalf("saved spec = %v", saved.Object["spec"])
	}
}
