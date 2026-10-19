package history

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/wcrum/gofi/kubeclient"
)

// GVR is the namespaced FlowRevision resource (charts/gofi/crds/flowrevisions.yaml).
var GVR = schema.GroupVersionResource{Group: "gofi.wcrum.dev", Version: "v1alpha1", Resource: "flowrevisions"}

// Kubernetes keeps each revision as its own FlowRevision object in GoFi's
// namespace. One object per revision keeps every write as small as the Flow
// it snapshots (well under etcd's object limit), makes Append an atomic
// create (AlreadyExists is ErrExists), and lets retention delete single
// objects. Revisions have no owner reference, so they outlive a deleted Flow.
type Kubernetes struct {
	Client    dynamic.Interface
	Namespace string
}

// NewKubernetes connects with the in-cluster service account.
func NewKubernetes(namespace string) (*Kubernetes, error) {
	if namespace == "" {
		return nil, fmt.Errorf("flow history requires a namespace")
	}
	clients, err := kubeclient.Get()
	if err != nil {
		return nil, err
	}
	return &Kubernetes{Client: clients.Dynamic, Namespace: namespace}, nil
}

func (k *Kubernetes) Describe() string { return "kubernetes:" + k.Namespace }

func (k *Kubernetes) resources() dynamic.ResourceInterface {
	return k.Client.Resource(GVR).Namespace(k.Namespace)
}

func (k *Kubernetes) Append(ctx context.Context, rev Revision) error {
	if err := validFlowName(rev.Flow); err != nil {
		return err
	}
	item, err := toResource(rev)
	if err != nil {
		return err
	}
	item.Metadata.Namespace = k.Namespace
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err = json.Unmarshal(data, &obj); err != nil {
		return err
	}
	_, err = k.resources().Create(ctx, &unstructured.Unstructured{Object: obj}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return ErrExists
	}
	return err
}

func decodeObject(obj *unstructured.Unstructured) (Revision, error) {
	data, err := json.Marshal(obj.Object)
	if err != nil {
		return Revision{}, err
	}
	var item resource
	if err = json.Unmarshal(data, &item); err != nil {
		return Revision{}, fmt.Errorf("%s %q: %w", Kind, obj.GetName(), err)
	}
	return fromResource(item)
}

func (k *Kubernetes) List(ctx context.Context, flow string) ([]Revision, error) {
	if err := validFlowName(flow); err != nil {
		return nil, err
	}
	list, err := k.resources().List(ctx, metav1.ListOptions{LabelSelector: FlowLabel + "=" + flowLabel(flow)})
	if err != nil {
		return nil, err
	}
	out := make([]Revision, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].GetDeletionTimestamp() != nil {
			continue
		}
		rev, err := decodeObject(&list.Items[i])
		if err != nil {
			return nil, err
		}
		if rev.Flow == flow { // a digest label could in principle be shared
			out = append(out, rev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

func (k *Kubernetes) Get(ctx context.Context, flow string, number int64) (Revision, error) {
	if err := validFlowName(flow); err != nil {
		return Revision{}, err
	}
	obj, err := k.resources().Get(ctx, objectName(flow, number), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Revision{}, ErrNotFound
	}
	if err != nil {
		return Revision{}, err
	}
	rev, err := decodeObject(obj)
	if err != nil {
		return Revision{}, err
	}
	if rev.Flow != flow || rev.Number != number {
		return Revision{}, ErrNotFound
	}
	return rev, nil
}

func (k *Kubernetes) Delete(ctx context.Context, flow string, numbers ...int64) error {
	if err := validFlowName(flow); err != nil {
		return err
	}
	for _, n := range numbers {
		err := k.resources().Delete(ctx, objectName(flow, n), metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (k *Kubernetes) Flows(ctx context.Context) ([]string, error) {
	list, err := k.resources().List(ctx, metav1.ListOptions{LabelSelector: FlowLabel})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for i := range list.Items {
		flow, _, _ := unstructured.NestedString(list.Items[i].Object, "spec", "flow")
		if flow != "" && !seen[flow] && validFlowName(flow) == nil {
			seen[flow] = true
			out = append(out, flow)
		}
	}
	sort.Strings(out)
	return out, nil
}
