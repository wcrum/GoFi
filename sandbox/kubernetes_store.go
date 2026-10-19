package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"sort"

	"github.com/wcrum/gofi/kubeclient"
)

var SandboxGVR = schema.GroupVersionResource{Group: "gofi.wcrum.dev", Version: "v1alpha1", Resource: "sandboxes"}

type KubernetesStore struct {
	Client    dynamic.Interface
	Namespace string
	// Lister, when set, serves Load from the Sandbox informer cache.
	Lister cache.GenericNamespaceLister
}

// NewKubernetesStore uses the shared in-cluster client (kubeclient).
func NewKubernetesStore(namespace string) (*KubernetesStore, error) {
	clients, e := kubeclient.Get()
	if e != nil {
		return nil, e
	}
	return &KubernetesStore{Client: clients.Dynamic, Namespace: namespace}, nil
}
func (k *KubernetesStore) items(ctx context.Context) ([]unstructured.Unstructured, error) {
	if k.Lister != nil {
		objects, e := k.Lister.List(labels.Everything())
		if e != nil {
			return nil, e
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
	list, e := k.Client.Resource(SandboxGVR).Namespace(k.Namespace).List(ctx, metav1.ListOptions{})
	if e != nil {
		return nil, e
	}
	return list.Items, nil
}
func (k *KubernetesStore) Load(ctx context.Context) ([]Definition, error) {
	items, e := k.items(ctx)
	if e != nil {
		return nil, e
	}
	defs := []Definition{}
	for _, r := range items {
		if r.GetDeletionTimestamp() != nil {
			continue
		}
		obj, ok, e := unstructured.NestedMap(r.Object, "spec")
		if e != nil || !ok {
			return nil, fmt.Errorf("invalid Sandbox spec")
		}
		b, e := json.Marshal(obj)
		if e != nil {
			return nil, e
		}
		var d Definition
		if e = json.Unmarshal(b, &d); e != nil {
			return nil, e
		}
		d.Name = r.GetName()
		defs = append(defs, d)
	}
	return defs, nil
}
func (k *KubernetesStore) Save(ctx context.Context, d Definition) error {
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	var obj map[string]any
	if e = json.Unmarshal(b, &obj); e != nil {
		return e
	}
	delete(obj, "name")
	c := k.Client.Resource(SandboxGVR).Namespace(k.Namespace)
	r, e := c.Get(ctx, d.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(e) {
		_, e = c.Create(ctx, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gofi.wcrum.dev/v1alpha1", "kind": "Sandbox", "metadata": map[string]any{"name": d.Name}, "spec": obj}}, metav1.CreateOptions{})
		return e
	}
	if e != nil {
		return e
	}
	r.Object["spec"] = obj
	_, e = c.Update(ctx, r, metav1.UpdateOptions{})
	return e
}
func (k *KubernetesStore) Delete(ctx context.Context, name string) error {
	e := k.Client.Resource(SandboxGVR).Namespace(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(e) {
		return nil
	}
	return e
}
