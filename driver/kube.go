package driver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wcrum/gofi/clusterinfo"
	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/spec"
)

// Kube creates one bare Pod per FlowDefinition, plus an optional
// ClusterIP or NodePort Service when source.service is configured.
//
// Deliberately bare Pods, not Deployment+Service: a Deployment's own
// ReplicaSet reconciler would fight GoFi's own controller.sync reconciler
// over the same object (both would "want" to keep exactly one replica
// alive, double-managing recreation on crash). Services provide stable DNS
// and optional NodePort ingress; tap traffic still uses the private Pod IP.
//
// The spec is injected directly as a Pod env var (FLOW_SPEC), not via a
// ConfigMap — a ConfigMap per listener would be one more object for the
// controller to garbage-collect in lockstep with the Pod, for no benefit
// over an inline env var.
type Kube struct {
	cs          kubernetes.Interface
	namespace   string
	image       string
	podTemplate ListenerPodTemplate
	// dyn reads and writes CRD-backed objects (Gateway API routes and
	// listeners, UDS Packages); cluster describes cluster networking.
	dyn     dynamic.Interface
	cluster *clusterinfo.Client

	errMu            sync.Mutex
	networkingErrors map[string]string
}

// Cluster returns the read-only cluster networking client.
func (k *Kube) Cluster() *clusterinfo.Client { return k.cluster }

// ListenerPodTemplate adds operator-supplied protocol sidecars and their
// volumes to each listener Pod. Use per-Pod ephemeral volumes for state;
// a shared ReadWriteOnce claim cannot safely back independent listeners.
type ListenerPodTemplate struct {
	Containers   []corev1.Container   `json:"containers,omitempty"`
	Volumes      []corev1.Volume      `json:"volumes,omitempty"`
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"` // mounted into the main GoFi listener
	Env          []corev1.EnvVar      `json:"env,omitempty"`          // added to the main listener, never overrides FLOW_SPEC
}

func parseListenerPodTemplate(raw string) (ListenerPodTemplate, error) {
	var template ListenerPodTemplate
	if raw == "" {
		return template, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&template); err != nil {
		return template, fmt.Errorf("GOFI_LISTENER_POD_TEMPLATE: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return template, fmt.Errorf("GOFI_LISTENER_POD_TEMPLATE: expected a single JSON object")
	}
	names := map[string]bool{"gofi-listener": true}
	for _, c := range template.Containers {
		if c.Name == "" || c.Image == "" || names[c.Name] {
			return template, fmt.Errorf("GOFI_LISTENER_POD_TEMPLATE: sidecars require unique names and an explicit image")
		}
		names[c.Name] = true
	}
	names = map[string]bool{}
	for _, v := range template.Volumes {
		if v.Name == "" || names[v.Name] {
			return template, fmt.Errorf("GOFI_LISTENER_POD_TEMPLATE: volume names must be unique and nonempty")
		}
		names[v.Name] = true
	}
	for _, e := range template.Env {
		if e.Name == spec.FlowSpecEnv || e.Name == "GOFI_K8S_NAMESPACE" || e.Name == "GOFI_PYTHON_BINDINGS" {
			return template, fmt.Errorf("GOFI_LISTENER_POD_TEMPLATE: %s is reserved", e.Name)
		}
	}
	return template, nil
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
	template, err := parseListenerPodTemplate(os.Getenv("GOFI_LISTENER_POD_TEMPLATE"))
	if err != nil {
		return nil, err
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("kube driver: in-cluster config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kube driver: clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kube driver: dynamic client: %w", err)
	}
	cluster := clusterinfo.New(cs, dyn, clusterinfo.Options{Namespace: namespace, NodePortMin: spec.MinNodePort, NodePortMax: spec.MaxNodePort,
		ClusterWide:            envBool("GOFI_CLUSTER_DISCOVERY", true),
		ManageGatewayListeners: envBool("GOFI_MANAGE_GATEWAY_LISTENERS", false),
		ManageUDSAllows:        envBool("GOFI_MANAGE_UDS_ALLOWS", false)})
	return &Kube{cs: cs, namespace: namespace, image: image, podTemplate: template, dyn: dyn, cluster: cluster}, nil
}

// podName derives a stable, valid Pod name for a listener.
func podName(name string) string {
	base := "gofi-listener-" + sanitizeName(name)
	if len(base) > 63 {
		sum := sha256.Sum256([]byte(name))
		base = strings.TrimRight(base[:54], "-") + fmt.Sprintf("-%x", sum[:4])
	}
	return base
}

// Create starts a new bare Pod running the given listener definition.
func (k *Kube) Create(ctx context.Context, def spec.FlowDefinition) error {
	if err := def.Source.Validate(); err != nil {
		return err
	}
	sinks := append([]spec.SinkCfg(nil), def.Sinks...)
	if def.DeadLetterSink != nil {
		sinks = append(sinks, *def.DeadLetterSink)
	}
	for _, sink := range sinks {
		if sink.Kubernetes != nil {
			if err := k.validateTarget(sink.Kubernetes, sink.Type); err != nil {
				return err
			}
		}
	}
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
			AutomountServiceAccountToken: boolPtr(false),
			SecurityContext:              &corev1.PodSecurityContext{RunAsNonRoot: boolPtr(true), RunAsUser: int64Ptr(65532), RunAsGroup: int64Ptr(65532), FSGroup: int64Ptr(65532), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			// GoFi's own periodic resync (controller.Controller.Run), not
			// Kubernetes, is the recreate path on crash — this keeps
			// crashes visible instead of silently hidden by kubelet restarts.
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "gofi-listener",
					Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 9091, Protocol: corev1.ProtocolTCP}},
					Image: k.image,
					// Empty keeps the Kubernetes default; dev installs reusing one tag set Always.
					ImagePullPolicy: corev1.PullPolicy(os.Getenv("GOFI_IMAGE_PULL_POLICY")),
					SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: boolPtr(false), ReadOnlyRootFilesystem: boolPtr(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
					Resources:       corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")}, Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")}},
					Env: []corev1.EnvVar{
						{Name: spec.FlowSpecEnv, Value: string(raw)},
						{Name: "GOFI_K8S_NAMESPACE", Value: k.namespace},
					},
				},
			},
		},
	}
	if def.UsesPython() {
		bindings, err := functionrpc.Environment(ctx, def)
		if err != nil {
			return err
		}
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{Name: "GOFI_PYTHON_BINDINGS", Value: bindings})
	}
	for _, sidecar := range k.podTemplate.Containers {
		pod.Spec.Containers = append(pod.Spec.Containers, *sidecar.DeepCopy())
	}
	for _, env := range k.podTemplate.Env {
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, *env.DeepCopy())
	}
	for _, mount := range k.podTemplate.VolumeMounts {
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, *mount.DeepCopy())
	}
	for _, volume := range k.podTemplate.Volumes {
		pod.Spec.Volumes = append(pod.Spec.Volumes, *volume.DeepCopy())
	}

	created, err := k.cs.CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		old, getErr := k.cs.CoreV1().Pods(k.namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if getErr == nil && old.Labels[kubeLabelManaged] == "true" && (old.Status.Phase == corev1.PodFailed || old.Status.Phase == corev1.PodSucceeded) {
			if err := k.Delete(ctx, def.Name); err != nil {
				return err
			}
			created, err = k.cs.CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{})
		}
	}
	if err != nil {
		return fmt.Errorf("kube driver: create pod for %q: %w", def.Name, err)
	}
	err = k.reconcileService(ctx, def, created)
	if err == nil {
		err = k.reconcileTargetServices(ctx, def, created)
	}
	if err != nil {
		// A partial create must be retryable by the controller.
		if cleanupErr := k.cs.CoreV1().Pods(k.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); cleanupErr != nil && !apierrors.IsNotFound(cleanupErr) {
			return fmt.Errorf("%w; pod rollback: %v", err, cleanupErr)
		}
		return err
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
	if err := k.deleteTargetServices(ctx, name); err != nil {
		return err
	}
	if err := k.deleteGatewayRoute(ctx, name); err != nil {
		return err
	}
	if err := k.deleteService(ctx, name); err != nil {
		return err
	}

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
// Running, decoded back from each Pod's FLOW_SPEC env var.
func (k *Kube) List(ctx context.Context) ([]spec.FlowDefinition, error) {
	list, err := k.cs.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: kubeLabelManaged + "=true",
	})
	if err != nil {
		return nil, fmt.Errorf("kube driver: list pods: %w", err)
	}

	out := make([]spec.FlowDefinition, 0, len(list.Items))
	for _, pod := range list.Items {
		if pod.Status.Phase != corev1.PodPending && pod.Status.Phase != corev1.PodRunning {
			continue
		}
		def, ok := decodePodListenerSpec(&pod)
		if !ok {
			slog.Warn("kube driver: managed pod missing/invalid flow spec, skipping", "pod", pod.Name)
			continue
		}
		// A Service, route, or destination problem belongs to this flow: it is
		// reported on the flow, and the rest keep reconciling.
		err := k.reconcileService(ctx, def, &pod)
		if err == nil {
			err = k.reconcileTargetServices(ctx, def, &pod)
		}
		k.recordNetworking(def.Name, err)
		out = append(out, def)
	}
	return out, nil
}

// decodePodListenerSpec extracts and decodes the flow spec env var from
// a Pod's first container.
func decodePodListenerSpec(pod *corev1.Pod) (spec.FlowDefinition, bool) {
	if len(pod.Spec.Containers) == 0 {
		return spec.FlowDefinition{}, false
	}
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Name != spec.FlowSpecEnv {
			continue
		}
		var def spec.FlowDefinition
		if err := json.Unmarshal([]byte(e.Value), &def); err != nil {
			return spec.FlowDefinition{}, false
		}
		return def, true
	}
	return spec.FlowDefinition{}, false
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
	out.Error = k.networkingError(name)
	return out, true, nil
}

// recordNetworking keeps the last exposure/destination error of a flow.
func (k *Kube) recordNetworking(name string, err error) {
	k.errMu.Lock()
	defer k.errMu.Unlock()
	if err == nil {
		delete(k.networkingErrors, name)
		return
	}
	if k.networkingErrors == nil {
		k.networkingErrors = map[string]string{}
	}
	if k.networkingErrors[name] != err.Error() {
		slog.Warn("kube driver: flow networking failed", "name", name, "error", err)
	}
	k.networkingErrors[name] = err.Error()
}

func (k *Kube) networkingError(name string) string {
	k.errMu.Lock()
	defer k.errMu.Unlock()
	if message := k.networkingErrors[name]; message != "" {
		return "Networking: " + message
	}
	return ""
}

// Kind identifies this driver implementation.
func (k *Kube) Kind() string { return "kubernetes" }

func boolPtr(value bool) *bool    { return &value }
func int64Ptr(value int64) *int64 { return &value }

func (k *Kube) deleteService(ctx context.Context, name string) error {
	svc, err := k.cs.CoreV1().Services(k.namespace).Get(ctx, podName(name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if svc.Labels[kubeLabelManaged] != "true" || svc.Labels[kubeLabelListener] != name {
		// Unrelated Services may happen to share the generated name when
		// exposure is disabled. They are outside this driver's ownership.
		return nil
	}
	err = k.cs.CoreV1().Services(k.namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *Kube) reconcileService(ctx context.Context, def spec.FlowDefinition, pod *corev1.Pod) error {
	if def.Source.Listening().Service == nil {
		if err := k.deleteGatewayRoute(ctx, def.Name); err != nil {
			return err
		}
		return k.deleteService(ctx, def.Name)
	}
	cfg := def.Source.Listening().Service
	port := cfg.Port
	if port == 0 {
		port = def.Source.Listening().Port
	}
	protocol := corev1.ProtocolTCP
	if def.Source.Listening().Network == "udp" {
		protocol = corev1.ProtocolUDP
	}
	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: k.namespace,
			Labels:          map[string]string{kubeLabelManaged: "true", kubeLabelListener: def.Name},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: pod.Name, UID: pod.UID}}},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceType(cfg.Type),
			Selector: map[string]string{kubeLabelManaged: "true", kubeLabelListener: def.Name},
			Ports:    []corev1.ServicePort{{Name: "data", Protocol: protocol, Port: int32(port), TargetPort: intstr.FromInt(def.Source.Listening().Port), NodePort: int32(cfg.NodePort)}}},
	}
	applyExposure(desired, cfg)
	services := k.cs.CoreV1().Services(k.namespace)
	current, err := services.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err = services.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return err
		}
		return k.reconcileGatewayRoute(ctx, def, pod)
	}
	if err != nil {
		return err
	}
	if current.Labels[kubeLabelManaged] != "true" || current.Labels[kubeLabelListener] != def.Name {
		return fmt.Errorf("kube driver: service %q already exists and is unmanaged", desired.Name)
	}
	if cfg.Type != "ClusterIP" && cfg.NodePort == 0 && len(current.Spec.Ports) == 1 {
		desired.Spec.Ports[0].NodePort = current.Spec.Ports[0].NodePort
	}
	// Preserve allocated IPs and admission/defaulted fields while restoring
	// the declared routing fields on every periodic reconciliation.
	current.Spec.Type = desired.Spec.Type
	current.Spec.Selector = desired.Spec.Selector
	current.Spec.Ports = desired.Spec.Ports
	current.OwnerReferences = desired.OwnerReferences
	updateExposure(current, desired)
	if _, err = services.Update(ctx, current, metav1.UpdateOptions{}); err != nil {
		return err
	}
	return k.reconcileGatewayRoute(ctx, def, pod)
}

func (k *Kube) Endpoints(ctx context.Context) ([]Endpoint, error) {
	services, err := k.cs.CoreV1().Services(k.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kube driver: discover services: %w", err)
	}
	out := make([]Endpoint, 0, len(services.Items))
	for _, svc := range services.Items {
		host := svc.Name + "." + svc.Namespace + ".svc.cluster.local"
		if svc.Spec.Type == corev1.ServiceTypeExternalName {
			host = svc.Spec.ExternalName
		}
		e := Endpoint{Name: svc.Name, Namespace: svc.Namespace, Kind: "kubernetes", Type: string(svc.Spec.Type), Host: host, ClusterIP: svc.Spec.ClusterIP, Ports: []EndpointPort{}}
		e.ExternalAddresses = append(e.ExternalAddresses, svc.Spec.ExternalIPs...)
		for _, ingress := range svc.Status.LoadBalancer.Ingress {
			if ingress.IP != "" {
				e.ExternalAddresses = append(e.ExternalAddresses, ingress.IP)
			}
			if ingress.Hostname != "" {
				e.ExternalAddresses = append(e.ExternalAddresses, ingress.Hostname)
			}
		}
		for _, port := range svc.Spec.Ports {
			protocol := string(port.Protocol)
			if protocol == "" {
				protocol = "TCP"
			}
			e.Ports = append(e.Ports, EndpointPort{Name: port.Name, Port: port.Port, Protocol: protocol, NodePort: port.NodePort, URL: strings.ToLower(protocol) + "://" + net.JoinHostPort(host, strconv.Itoa(int(port.Port)))})
		}
		out = append(out, e)
	}
	pods, err := k.podEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, pods...)
	return out, nil
}
