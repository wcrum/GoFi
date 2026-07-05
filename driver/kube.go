package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wcrum/gofi/spec"
)

// Kube creates one bare Pod per ListenerDefinition using client-go — no
// Deployment, no ConfigMap, no per-Pod Service.
//
// Deliberately bare Pods, not Deployment+Service: a Deployment's own
// ReplicaSet reconciler would fight GoFi's own controller.sync reconciler
// over the same object (both would "want" to keep exactly one replica
// alive, double-managing recreation on crash). A Service adds no value here
// either — Pod IPs are already flat-routable cluster-internally, and the
// controller already does a Get per tap request (see Tap below), so there's
// no benefit to the extra indirection/DNS a Service would add.
//
// The spec is injected directly as a Pod env var (LISTENER_SPEC), not via a
// ConfigMap — a ConfigMap per listener would be one more object for the
// controller to garbage-collect in lockstep with the Pod, for no benefit
// over an inline env var.
type Kube struct {
	cs        kubernetes.Interface
	namespace string
	image     string
}

// Labels applied to every Pod the Kubernetes driver creates. Namespaced
// keys per Kubernetes convention (unlike the Docker driver's bare
// "gofi.listener"/"gofi.managed-by" labels, since Docker has no
// registration/namespacing convention for labels).
const (
	kubeLabelListener = "gofi.io/listener"
	kubeLabelManaged  = "gofi.io/managed-by"

	// kubeTapRelayPort must match TAP_RELAY_ADDR's default in main.go.
	kubeTapRelayPort = 9091

	// kubeDeleteTimeout bounds how long Delete waits for a Pod to actually
	// disappear. Kubernetes Pod deletion is not synchronous (unlike Docker's
	// ContainerRemove), so Delete must poll.
	kubeDeleteTimeout = 20 * time.Second
	kubeDeletePoll    = 500 * time.Millisecond
)

// NewKube builds a Kubernetes driver using in-cluster config (this is meant
// to run as the controller Pod itself, inside the cluster it manages).
func NewKube(namespace, image string) (*Kube, error) {
	if image == "" {
		return nil, fmt.Errorf("kube driver: image is required (GOFI_IMAGE)")
	}
	if namespace == "" {
		return nil, fmt.Errorf("kube driver: namespace could not be determined (GOFI_K8S_NAMESPACE or in-cluster serviceaccount namespace file)")
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("kube driver: in-cluster config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kube driver: clientset: %w", err)
	}
	return &Kube{cs: cs, namespace: namespace, image: image}, nil
}

// podName derives a stable, valid Pod name for a listener.
func podName(name string) string {
	return "gofi-listener-" + sanitizeName(name)
}

// Create starts a new bare Pod running the given listener definition.
func (k *Kube) Create(ctx context.Context, def spec.ListenerDefinition) error {
	raw, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("kube driver: marshal spec for %q: %w", def.Name, err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName(def.Name),
			Namespace: k.namespace,
			Labels: map[string]string{
				kubeLabelListener: def.Name,
				kubeLabelManaged:  "true",
			},
		},
		Spec: corev1.PodSpec{
			// GoFi's own periodic resync (controller.Controller.Run), not
			// Kubernetes, is the recreate path on crash — this keeps
			// crashes visible instead of silently hidden by kubelet restarts.
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "gofi-listener",
					Image: k.image,
					Env: []corev1.EnvVar{
						{Name: "LISTENER_SPEC", Value: string(raw)},
					},
				},
			},
		},
	}

	if _, err := k.cs.CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("kube driver: create pod for %q: %w", def.Name, err)
	}
	return nil
}

// Delete deletes the listener's Pod and blocks (polling) until it's
// actually gone. Kubernetes Pod deletion is asynchronous — a Delete call
// only marks the Pod for termination — so unlike Docker's ContainerRemove,
// we can't treat a successful Delete API call as "done"; the controller's
// update path (delete-then-recreate) needs the Pod to truly be gone before
// racing a new Create against it.
func (k *Kube) Delete(ctx context.Context, name string) error {
	pn := podName(name)

	if err := k.cs.CoreV1().Pods(k.namespace).Delete(ctx, pn, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("kube driver: delete pod %q: %w", name, err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, kubeDeleteTimeout)
	defer cancel()

	err := wait.PollUntilContextTimeout(waitCtx, kubeDeletePoll, kubeDeleteTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := k.cs.CoreV1().Pods(k.namespace).Get(ctx, pn, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("kube driver: waiting for pod %q to be removed: %w", name, err)
	}
	return nil
}

// List returns the definitions of all gofi-managed Pods that are Pending or
// Running, decoded back from each Pod's LISTENER_SPEC env var.
func (k *Kube) List(ctx context.Context) ([]spec.ListenerDefinition, error) {
	list, err := k.cs.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: kubeLabelManaged + "=true",
	})
	if err != nil {
		return nil, fmt.Errorf("kube driver: list pods: %w", err)
	}

	out := make([]spec.ListenerDefinition, 0, len(list.Items))
	for _, pod := range list.Items {
		if pod.Status.Phase != corev1.PodPending && pod.Status.Phase != corev1.PodRunning {
			continue
		}
		def, ok := decodePodListenerSpec(&pod)
		if !ok {
			slog.Warn("kube driver: managed pod missing/invalid LISTENER_SPEC, skipping", "pod", pod.Name)
			continue
		}
		out = append(out, def)
	}
	return out, nil
}

// decodePodListenerSpec extracts and decodes the LISTENER_SPEC env var from
// a Pod's first container.
func decodePodListenerSpec(pod *corev1.Pod) (spec.ListenerDefinition, bool) {
	if len(pod.Spec.Containers) == 0 {
		return spec.ListenerDefinition{}, false
	}
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Name != "LISTENER_SPEC" {
			continue
		}
		var def spec.ListenerDefinition
		if err := json.Unmarshal([]byte(e.Value), &def); err != nil {
			return spec.ListenerDefinition{}, false
		}
		return def, true
	}
	return spec.ListenerDefinition{}, false
}

// Tap returns the listener Pod's IP as the tap relay address, if the Pod is
// Running and has an assigned IP. This costs one Get call per tap request.
func (k *Kube) Tap(ctx context.Context, name string) (TapTarget, bool, error) {
	pod, err := k.cs.CoreV1().Pods(k.namespace).Get(ctx, podName(name), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return TapTarget{}, false, nil
		}
		return TapTarget{}, false, fmt.Errorf("kube driver: get pod %q: %w", name, err)
	}
	if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
		return TapTarget{}, false, nil
	}
	return TapTarget{Addr: fmt.Sprintf("%s:%d", pod.Status.PodIP, kubeTapRelayPort)}, true, nil
}

// RuntimeStatus gets the listener's Pod and reports its phase, name, node,
// and IP. ok is false if the Pod doesn't exist.
func (k *Kube) RuntimeStatus(ctx context.Context, name string) (RuntimeInfo, bool, error) {
	pod, err := k.cs.CoreV1().Pods(k.namespace).Get(ctx, podName(name), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return RuntimeInfo{}, false, nil
		}
		return RuntimeInfo{}, false, fmt.Errorf("kube driver: get pod %q: %w", name, err)
	}

	out := RuntimeInfo{
		Kind:   "kubernetes",
		ID:     pod.Name,
		Status: string(pod.Status.Phase),
		Node:   pod.Spec.NodeName,
		IP:     pod.Status.PodIP,
	}
	if pod.Status.StartTime != nil {
		out.StartedAt = pod.Status.StartTime.Time
	}
	return out, true, nil
}

// Kind identifies this driver implementation.
func (k *Kube) Kind() string { return "kubernetes" }
