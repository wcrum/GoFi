package desired

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/wcrum/gofi/spec"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/wcrum/gofi/kubeclient"
)

// FlowGVR is the desired-state resource. Pre-rename Listener resources are
// not read (docs/flows.md).
var FlowGVR = schema.GroupVersionResource{Group: "gofi.wcrum.dev", Version: "v1alpha1", Resource: "flows"}

type Kubernetes struct {
	Client    dynamic.Interface
	Namespace string
	// Lister, when set, serves Load from the Flow informer cache instead of
	// listing through the API server on every reconcile.
	Lister cache.GenericNamespaceLister
}

// NewKubernetes uses the shared in-cluster client (kubeclient).
func NewKubernetes(namespace string) (*Kubernetes, error) {
	if namespace == "" {
		return nil, fmt.Errorf("Kubernetes desired state requires a namespace")
	}
	clients, err := kubeclient.Get()
	if err != nil {
		return nil, err
	}
	return &Kubernetes{Client: clients.Dynamic, Namespace: namespace}, nil
}

// items are the Flow resources: from the cache when a Lister is set.
func (k *Kubernetes) items(ctx context.Context) ([]unstructured.Unstructured, error) {
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
	list, err := k.Client.Resource(FlowGVR).Namespace(k.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}
func (k *Kubernetes) Configuration() Configuration {
	return Configuration{Source: "kubernetes", APIVersion: APIVersion, Kind: KindFlow, Namespace: k.Namespace}
}

func (k *Kubernetes) Load(ctx context.Context) ([]spec.FlowDefinition, error) {
	items, err := k.items(ctx)
	if err != nil {
		return nil, err
	}
	defs := make([]spec.FlowDefinition, 0, len(items))
	for _, item := range items {
		if item.GetDeletionTimestamp() != nil {
			continue
		}
		obj, ok, err := unstructured.NestedMap(item.Object, "spec")
		if err != nil || !ok {
			return nil, fmt.Errorf("Flow %q has no spec", item.GetName())
		}
		data, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		var def spec.FlowDefinition
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&def); err != nil {
			return nil, fmt.Errorf("Flow %q: %w", item.GetName(), err)
		}
		if def.Name != "" && def.Name != item.GetName() {
			return nil, fmt.Errorf("Flow %q spec.name must match metadata.name", item.GetName())
		}
		def.Name = item.GetName()
		// Read parser.decoders as the decode step. A conflict is left in
		// place for the controller to report on that flow alone.
		_ = def.Normalize()
		defs = append(defs, def)
	}
	return defs, nil
}
func (k *Kubernetes) Save(ctx context.Context, def spec.FlowDefinition) error {
	data, err := json.Marshal(def)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err = json.Unmarshal(data, &obj); err != nil {
		return err
	}
	delete(obj, "name")
	client := k.Client.Resource(FlowGVR).Namespace(k.Namespace)
	existing, err := client.Get(ctx, def.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, &unstructured.Unstructured{Object: map[string]any{"apiVersion": APIVersion, "kind": KindFlow, "metadata": map[string]any{"name": def.Name, "namespace": k.Namespace}, "spec": obj}}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	existing.Object["spec"] = obj
	_, err = client.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}
func (k *Kubernetes) Delete(ctx context.Context, name string) error {
	err := k.Client.Resource(FlowGVR).Namespace(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
