package expressions

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newFakeStore() *KubernetesStore {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{GVR: "CelExpressionList"})
	return &KubernetesStore{Client: client, Namespace: "gofi"}
}

func loadOne(t *testing.T, store *KubernetesStore) Expression {
	t.Helper()
	loaded, err := store.Load(context.Background())
	if err != nil || len(loaded) != 1 {
		t.Fatalf("Load = %v, %v", loaded, err)
	}
	return loaded[0]
}

// An external edit (kubectl/GitOps) must not silently change the text that
// listeners pinned to the current generation evaluate.
func TestKubernetesStoreExternalEditBumpsGeneration(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	if err := store.Save(ctx, Expression{Name: "hot", Expression: "fields.t > 30.0", ResultType: "bool", Generation: 2}); err != nil {
		t.Fatal(err)
	}
	if got := loadOne(t, store); got.Generation != 2 || len(got.Revisions) != 0 {
		t.Fatalf("unchanged resource: generation %d revisions %v", got.Generation, got.Revisions)
	}

	resources := store.Client.Resource(GVR).Namespace("gofi")
	r, err := resources.Get(ctx, "hot", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = unstructured.SetNestedField(r.Object, "fields.t > 35.0", "spec", "expression"); err != nil {
		t.Fatal(err)
	}
	if _, err = resources.Update(ctx, r, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	got := loadOne(t, store)
	if got.Generation != 3 || got.Expression != "fields.t > 35.0" {
		t.Fatalf("external edit: generation %d expression %q", got.Generation, got.Expression)
	}
	if len(got.Revisions) != 1 || got.Revisions[0].Generation != 2 || got.Revisions[0].Expression != "fields.t > 30.0" {
		t.Fatalf("previous text not retained: %+v", got.Revisions)
	}
	if again := loadOne(t, store); again.Generation != 3 {
		t.Fatalf("Load is not idempotent: generation %d", again.Generation)
	}

	// Saving through GoFi records the new text, so no further bump occurs.
	if err = store.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	if final := loadOne(t, store); final.Generation != 3 {
		t.Fatalf("after save: generation %d", final.Generation)
	}
}
