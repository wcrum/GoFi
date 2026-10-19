package invariants

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/history"
	"github.com/wcrum/gofi/spec"
)

// KubeObserver sees a namespace the way the Kubernetes driver, the sandbox
// manager, and the history recorder leave it.
type KubeObserver struct {
	Clientset kubernetes.Interface
	Dynamic   dynamic.Interface
	Namespace string
	// PackageName is GoFi's UDS Package (empty: skip).
	PackageName string
}

// The Sandbox resource shares the Flow CRD's group and version, so a group
// rename (gofi.live) is followed automatically.
var (
	sandboxGVR  = schema.GroupVersionResource{Group: desired.FlowGVR.Group, Version: desired.FlowGVR.Version, Resource: "sandboxes"}
	packagesGVR = schema.GroupVersionResource{Group: "uds.dev", Version: "v1alpha1", Resource: "packages"}
)

// Observe reads Flow and Sandbox resources (desired), Pods and Services with
// GoFi's ownership labels (runtimes, flow Services, worker Pods), worker
// Secrets and NetworkPolicies, FlowRevisions, and the UDS Package.
func (o *KubeObserver) Observe(ctx context.Context) (World, error) {
	w := World{Observed: "kube:" + o.Namespace, Bindings: map[string]map[string]string{}, History: map[string][]Revision{}}
	store := &desired.Kubernetes{Client: o.Dynamic, Namespace: o.Namespace}
	defs, err := store.Load(ctx)
	if err != nil {
		return w, fmt.Errorf("flows: %w", err)
	}
	w.Desired = defs
	want := map[string]bool{}
	for _, def := range defs {
		want[def.Name] = true
	}
	libs := map[string]bool{}
	if list, err := o.Dynamic.Resource(sandboxGVR).Namespace(o.Namespace).List(ctx, metav1.ListOptions{}); err == nil {
		for _, item := range list.Items {
			libs[item.GetName()] = true
			w.Libraries = append(w.Libraries, item.GetName())
		}
	} else if !apierrors.IsNotFound(err) && !isNoMatch(err) {
		return w, fmt.Errorf("sandboxes: %w", err)
	}

	pods, err := o.Clientset.CoreV1().Pods(o.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return w, fmt.Errorf("pods: %w", err)
	}
	for _, pod := range pods.Items {
		labels := pod.Labels
		switch {
		case labels["gofi.io/managed-by"] == "true" && labels["gofi.io/listener"] != "":
			flow := labels["gofi.io/listener"]
			rt := Runtime{Flow: flow, ID: pod.Name, Status: string(pod.Status.Phase), Node: pod.Spec.NodeName}
			if pod.DeletionTimestamp != nil {
				rt.Status = "terminating"
			}
			if len(pod.Spec.Containers) > 0 {
				for _, env := range pod.Spec.Containers[0].Env {
					switch env.Name {
					case spec.FlowSpecEnv:
						var def spec.FlowDefinition
						if json.Unmarshal([]byte(env.Value), &def) == nil {
							rt.Def = &def
						}
					case "GOFI_PYTHON_BINDINGS":
						w.Bindings[flow] = parseBindings(env.Value)
					}
				}
			}
			w.Runtimes = append(w.Runtimes, rt)
		case labels["gofi.managed-by"] == "gofi" && labels["gofi.sandbox"] != "":
			if len(libs) > 0 && !libs[labels["gofi.sandbox"]] {
				w.Orphans = append(w.Orphans, "pod/"+pod.Name)
			}
		}
	}

	services, err := o.Clientset.CoreV1().Services(o.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return w, fmt.Errorf("services: %w", err)
	}
	for _, svc := range services.Items {
		labels := svc.Labels
		switch {
		case labels["gofi.io/managed-by"] == "true" && labels["gofi.io/listener"] != "":
			if labels["gofi.io/target"] != "" || svc.Spec.ClusterIP == corev1.ClusterIPNone && svc.Spec.Selector == nil {
				continue // destination fan-out Services are owned by the flow's sinks
			}
			s := Service{Name: svc.Name, Flow: labels["gofi.io/listener"], Type: string(svc.Spec.Type), Selector: svc.Spec.Selector}
			for _, p := range svc.Spec.Ports {
				s.Ports = append(s.Ports, ServicePort{Port: p.Port, TargetPort: int32(p.TargetPort.IntValue()), NodePort: p.NodePort, Protocol: string(p.Protocol)})
			}
			w.Services = append(w.Services, s)
		case labels["gofi.managed-by"] == "gofi" && labels["gofi.sandbox"] != "":
			if len(libs) > 0 && !libs[labels["gofi.sandbox"]] {
				w.Orphans = append(w.Orphans, "service/"+svc.Name)
			}
		}
	}

	secrets, err := o.Clientset.CoreV1().Secrets(o.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "gofi.managed-by=gofi"})
	if err != nil {
		return w, fmt.Errorf("secrets: %w", err)
	}
	for _, secret := range secrets.Items {
		lib := secret.Labels["gofi.sandbox"]
		if lib == "" {
			continue
		}
		token := string(secret.Data["token"])
		if token == "" {
			token = secret.StringData["token"]
		}
		w.Secrets = append(w.Secrets, Secret{Name: secret.Name, Library: lib, Token: token})
	}
	policies, err := o.Clientset.NetworkingV1().NetworkPolicies(o.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "gofi.managed-by=gofi"})
	if err != nil {
		return w, fmt.Errorf("networkpolicies: %w", err)
	}
	for _, policy := range policies.Items {
		if lib := policy.Labels["gofi.sandbox"]; lib != "" && len(libs) > 0 && !libs[lib] {
			w.Orphans = append(w.Orphans, "networkpolicy/"+policy.Name)
		}
	}

	hist := &history.Kubernetes{Client: o.Dynamic, Namespace: o.Namespace}
	if flows, err := hist.Flows(ctx); err == nil {
		for _, flow := range flows {
			revs, err := hist.List(ctx, flow)
			if err != nil {
				return w, fmt.Errorf("flowrevisions %s: %w", flow, err)
			}
			w.History[flow] = convertRevisions(revs)
		}
	} else if !isNoMatch(err) {
		return w, fmt.Errorf("flowrevisions: %w", err)
	}

	if o.PackageName != "" {
		pkg, err := o.Dynamic.Resource(packagesGVR).Namespace(o.Namespace).Get(ctx, o.PackageName, metav1.GetOptions{})
		if err == nil {
			w.Allows = packageAllows(pkg)
		} else if !apierrors.IsNotFound(err) && !isNoMatch(err) {
			return w, fmt.Errorf("package: %w", err)
		}
	}
	return w, nil
}

func packageAllows(pkg *unstructured.Unstructured) []any {
	allows, _, _ := unstructured.NestedSlice(pkg.Object, "spec", "network", "allow")
	if allows == nil {
		return []any{}
	}
	return allows
}

func isNoMatch(err error) bool {
	return err != nil && (apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err))
}
