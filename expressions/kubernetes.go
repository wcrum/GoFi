package expressions

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/wcrum/gofi/kubeclient"
)

var GVR = schema.GroupVersionResource{Group: "gofi.wcrum.dev", Version: "v1alpha1", Resource: "celexpressions"}

// writtenAnnotation records the expression text GoFi last saved. A resource
// whose spec text differs was edited outside GoFi (kubectl, GitOps), so Load
// treats it as a new generation and keeps the previous text as a revision:
// listeners pinned to the old generation keep their old behavior.
const writtenAnnotation = "gofi.wcrum.dev/written-expression"

// KubernetesStore keeps entries as namespaced CelExpression resources so the
// library can be managed with GitOps alongside Listener resources.
type KubernetesStore struct {
	Client    dynamic.Interface
	Namespace string
	// Lister, when set, serves Load from the CelExpression informer cache.
	Lister cache.GenericNamespaceLister
}

// NewKubernetesStore uses the shared in-cluster client (kubeclient).
func NewKubernetesStore(namespace string) (*KubernetesStore, error) {
	clients, err := kubeclient.Get()
	if err != nil {
		return nil, err
	}
	return &KubernetesStore{Client: clients.Dynamic, Namespace: namespace}, nil
}

func (k *KubernetesStore) items(ctx context.Context) ([]unstructured.Unstructured, error) {
	if k.Lister != nil {
		objects, err := k.Lister.List(labels.Everything())
		if err != nil {
			return nil, err
		}
		out := make([]unstructured.Unstructured, 0, len(objects))
		for _, obj := range objects {
			if item, ok := obj.(*unstructured.Unstructured); ok {
				out = append(out, *item)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].GetName() < out[j].GetName() })
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, kubeclient.CallTimeout)
	defer cancel()
	list, err := k.Client.Resource(GVR).Namespace(k.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (k *KubernetesStore) Load(ctx context.Context) ([]Expression, error) {
	items, err := k.items(ctx)
	if err != nil {
		return nil, err
	}
	out := []Expression{}
	for _, r := range items {
		if r.GetDeletionTimestamp() != nil {
			continue
		}
		obj, ok, err := unstructured.NestedMap(r.Object, "spec")
		if err != nil || !ok {
			return nil, fmt.Errorf("invalid CelExpression %s spec", r.GetName())
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		var e Expression
		if err = json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		e.Name = r.GetName()
		if e, err = Normalize(e); err != nil {
			return nil, fmt.Errorf("CelExpression %s: %w", r.GetName(), err)
		}
		if e.Generation == 0 {
			e.Generation = 1
		}
		if written, ok := r.GetAnnotations()[writtenAnnotation]; ok && written != e.Expression {
			e.Revisions = append(e.Revisions, Revision{Generation: e.Generation, Expression: written})
			e.Generation++
		}
		out = append(out, e)
	}
	return out, nil
}

func (k *KubernetesStore) Save(ctx context.Context, e Expression) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err = json.Unmarshal(b, &obj); err != nil {
		return err
	}
	delete(obj, "name")
	c := k.Client.Resource(GVR).Namespace(k.Namespace)
	r, err := c.Get(ctx, e.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		r = &unstructured.Unstructured{Object: map[string]any{"apiVersion": APIVersion, "kind": Kind, "metadata": map[string]any{"name": e.Name}, "spec": obj}}
		r.SetAnnotations(map[string]string{writtenAnnotation: e.Expression})
		_, err = c.Create(ctx, r, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	r.Object["spec"] = obj
	annotations := r.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[writtenAnnotation] = e.Expression
	r.SetAnnotations(annotations)
	_, err = c.Update(ctx, r, metav1.UpdateOptions{})
	return err
}

func (k *KubernetesStore) Delete(ctx context.Context, name string) error {
	err := k.Client.Resource(GVR).Namespace(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
