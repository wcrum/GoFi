package desired

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/wcrum/gofi/spec"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var ListenerGVR = schema.GroupVersionResource{Group: "gofi.wcrum.dev", Version: "v1alpha1", Resource: "listeners"}

type Kubernetes struct {
	Client    dynamic.Interface
	Namespace string
}

func NewKubernetes(namespace string) (*Kubernetes, error) {
	if namespace == "" {
		return nil, fmt.Errorf("Kubernetes desired state requires a namespace")
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Kubernetes{Client: client, Namespace: namespace}, nil
}
func (k *Kubernetes) Configuration() Configuration {
	return Configuration{Source: "kubernetes", APIVersion: APIVersion, Kind: "Listener", Namespace: k.Namespace}
}
func (k *Kubernetes) Load(ctx context.Context) ([]spec.ListenerDefinition, error) {
	list, err := k.Client.Resource(ListenerGVR).Namespace(k.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	defs := make([]spec.ListenerDefinition, 0, len(list.Items))
	for _, item := range list.Items {
		if item.GetDeletionTimestamp() != nil {
			continue
		}
		obj, ok, err := unstructured.NestedMap(item.Object, "spec")
		if err != nil || !ok {
			return nil, fmt.Errorf("Listener %q has no spec", item.GetName())
		}
		data, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		var def spec.ListenerDefinition
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&def); err != nil {
			return nil, err
		}
		if def.Name != "" && def.Name != item.GetName() {
			return nil, fmt.Errorf("Listener %q spec.name must match metadata.name", item.GetName())
		}
		def.Name = item.GetName()
		defs = append(defs, def)
	}
	return defs, nil
}
func (k *Kubernetes) Save(ctx context.Context, def spec.ListenerDefinition) error {
	data, err := json.Marshal(def)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err = json.Unmarshal(data, &obj); err != nil {
		return err
	}
	delete(obj, "name")
	client := k.Client.Resource(ListenerGVR).Namespace(k.Namespace)
	existing, err := client.Get(ctx, def.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, &unstructured.Unstructured{Object: map[string]any{"apiVersion": APIVersion, "kind": "Listener", "metadata": map[string]any{"name": def.Name, "namespace": k.Namespace}, "spec": obj}}, metav1.CreateOptions{})
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
	err := k.Client.Resource(ListenerGVR).Namespace(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
