package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/wcrum/gofi/clusterinfo"
	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/kubeclient"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// Kube runs one bare Pod per FlowDefinition, plus an optional ClusterIP,
// NodePort, LoadBalancer, or Gateway-routed Service when source.service is
// configured.
//
// Deliberately bare Pods, not Deployment+Service: a Deployment's own
// ReplicaSet reconciler would fight GoFi's own controller.sync reconciler
// over the same object (both would "want" to keep exactly one replica
// alive, double-managing recreation on crash). Services provide stable DNS
// and optional NodePort ingress; tap traffic still uses the private Pod IP.
//
// Pods are named after the flow plus a hash of what runs in them (the
// definition, the image, the Python bindings), so a changed definition gets
// a new Pod next to the old one (Replace); the old one is removed once the
// new one runs. The Service, routes, and target Services keep their stable
// names across such updates and are owned by the Flow resource rather than
// by the Pod, so an update never drops the flow's address.
//
// The spec is injected directly as a Pod env var (FLOW_SPEC), not via a
// ConfigMap — a ConfigMap per listener would be one more object for the
// controller to garbage-collect in lockstep with the Pod, for no benefit
// over an inline env var.
//
// Reads go through objectReader: the informer cache in production (no API
// requests at steady state), the API server otherwise.
type Kube struct {
	cs          kubernetes.Interface
	namespace   string
	image       string
	podTemplate ListenerPodTemplate
	// dyn reads and writes CRD-backed objects (Gateway API routes and
	// listeners, UDS Packages); cluster describes cluster networking.
	dyn     dynamic.Interface
	cluster *clusterinfo.Client
	// reader serves Pod and Service reads; informers holds the caches when
	// the driver runs with them (nil in tests and when they failed to start).
	reader    objectReader
	informers *kubeclient.Informers
	// flowOwner resolves the Flow resource a listener belongs to, so Pods,
	// Services, and routes can carry an ownerReference to it. nil means the
	// desired state is not a Kubernetes resource (no owner is set).
	flowOwner func(name string) *metav1.OwnerReference
	// listenerDefaults are the chart's resources, scheduling, and probes for
	// listener Pods (GOFI_LISTENER_RESOURCES/_SCHEDULING/_PROBES).
	listenerDefaults kubeclient.PodDefaults
	// exposure defaults for LoadBalancer Services (GOFI_LB_ALLOCATE_NODE_PORTS,
	// GOFI_LB_ANNOTATIONS) and the Gateway listener cap (GOFI_GATEWAY_MAX_LISTENERS).
	lbAllocateNodePorts *bool
	lbAnnotations       map[string]string
	gatewayMaxListeners int

	errMu            sync.Mutex
	networkingErrors map[string]string

	routeMu     sync.Mutex
	routeGVRs   map[string]routeDiscovery
	udsMu       sync.Mutex
	udsLastHash string
	udsLastSeen time.Time
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
	kubeLabelManaged  = kubeclient.ManagedLabel

	// annotationSpecHash is the hash of everything a listener Pod runs; it is
	// also the Pod name's suffix, so an idempotent Create finds its own Pod.
	annotationSpecHash = "gofi.io/spec-hash"
	// annotationActivated orders a flow's Pods: the most recently activated
	// one is the flow's current runtime. Create stamps it, and stamps it again
	// when an edit reverts to a Pod that still exists.
	annotationActivated = "gofi.io/activated-at"

	// kubeTapRelayPort must match TAP_RELAY_ADDR's default in main.go.
	kubeTapRelayPort = 9091

	// maxDiscoveryItems bounds endpoint discovery listings (Services and
	// Pods in the namespace); beyond it the result is truncated with a warning.
	maxDiscoveryItems = 2000
)

// ErrPending reports a driver operation that could not complete yet because
// a previous runtime is still being removed; the caller retries later (the
// controller requeues with backoff) instead of blocking.
var ErrPending = errors.New("previous runtime is still being removed")

// NewKube builds a Kubernetes driver using the shared in-cluster clients
// (this is meant to run as the controller Pod itself, inside the cluster it
// manages).
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
	clients, err := kubeclient.Get()
	if err != nil {
		return nil, fmt.Errorf("kube driver: %w", err)
	}
	return newKube(clients.Clientset, clients.Dynamic, namespace, image, template)
}

// defaultGatewayMaxListeners is how many listeners GoFi holds on one Gateway
// when the chart does not say (implementations cap Gateways around 64).
const defaultGatewayMaxListeners = 64

func newKube(cs kubernetes.Interface, dyn dynamic.Interface, namespace, image string, template ListenerPodTemplate) (*Kube, error) {
	cluster := clusterinfo.New(cs, dyn, clusterinfo.Options{Namespace: namespace, NodePortMin: spec.MinNodePort, NodePortMax: spec.MaxNodePort,
		ClusterWide:            envBool("GOFI_CLUSTER_DISCOVERY", true),
		ManageGatewayListeners: envBool("GOFI_MANAGE_GATEWAY_LISTENERS", false),
		ManageUDSAllows:        envBool("GOFI_MANAGE_UDS_ALLOWS", false)})
	defaults, err := kubeclient.LoadPodDefaults("LISTENER")
	if err != nil {
		return nil, fmt.Errorf("kube driver: %w", err)
	}
	k := &Kube{cs: cs, namespace: namespace, image: image, podTemplate: template, dyn: dyn, cluster: cluster, reader: apiReader{cs: cs, namespace: namespace},
		listenerDefaults: defaults, gatewayMaxListeners: defaultGatewayMaxListeners}
	if raw := os.Getenv("GOFI_LB_ALLOCATE_NODE_PORTS"); raw != "" {
		allocate := envBool("GOFI_LB_ALLOCATE_NODE_PORTS", true)
		k.lbAllocateNodePorts = &allocate
	}
	if raw := strings.TrimSpace(os.Getenv("GOFI_LB_ANNOTATIONS")); raw != "" && raw != "null" {
		if err := json.Unmarshal([]byte(raw), &k.lbAnnotations); err != nil {
			return nil, fmt.Errorf("kube driver: GOFI_LB_ANNOTATIONS: %w", err)
		}
	}
	if raw := os.Getenv("GOFI_GATEWAY_MAX_LISTENERS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			k.gatewayMaxListeners = n
		}
	}
	return k, nil
}

// UseInformers serves the driver's reads from the shared watch caches. Call
// it before the caches are started; Start syncs the Pod and Service caches
// the driver registered.
func (k *Kube) UseInformers(informers *kubeclient.Informers) {
	k.informers = informers
	if informers == nil {
		k.reader = apiReader{cs: k.cs, namespace: k.namespace}
		return
	}
	k.reader = cacheReader{informers: informers}
}

// Informers returns the watch caches the driver reads, or nil.
func (k *Kube) Informers() *kubeclient.Informers { return k.informers }

// SetFlowOwner installs the resolver of a flow's owning resource.
func (k *Kube) SetFlowOwner(owner func(name string) *metav1.OwnerReference) { k.flowOwner = owner }

// Namespace is where the driver creates its objects.
func (k *Kube) Namespace() string { return k.namespace }

// read returns the reader in use (the API when none was set, e.g. tests).
func (k *Kube) read() objectReader {
	if k.reader == nil {
		k.reader = apiReader{cs: k.cs, namespace: k.namespace}
	}
	return k.reader
}

// podName derives the stable, valid object name of a listener: its Service,
// routes, and (before hashed names) its Pod.
func podName(name string) string {
	base := "gofi-listener-" + sanitizeName(name)
	if len(base) > 63 {
		sum := sha256.Sum256([]byte(name))
		base = strings.TrimRight(base[:54], "-") + fmt.Sprintf("-%x", sum[:4])
	}
	return base
}

// runtimePodName is a listener Pod's name: the stable name plus the spec
// hash, so a changed definition gets a fresh Pod beside the old one.
func runtimePodName(name, hash string) string {
	base := podName(name)
	suffix := "-" + hash
	if len(base)+len(suffix) > 63 {
		base = strings.TrimRight(base[:63-len(suffix)], "-")
	}
	return base + suffix
}

// specHash identifies what a Pod runs: the serialized definition, the image,
// and the Python bindings (worker endpoints and credentials). Eight hex
// characters keep names short; collisions only cost an unnecessary restart.
func specHash(raw []byte, image, bindings string) string {
	sum := sha256.New()
	sum.Write(raw)
	sum.Write([]byte{0})
	sum.Write([]byte(image))
	sum.Write([]byte{0})
	sum.Write([]byte(bindings))
	return hex.EncodeToString(sum.Sum(nil))[:8]
}

// ownerOf is the ownerReference objects of a flow carry (nil when the flow
// is not a Kubernetes resource).
func (k *Kube) ownerOf(name string) []metav1.OwnerReference {
	if k.flowOwner == nil {
		return nil
	}
	if ref := k.flowOwner(name); ref != nil {
		return []metav1.OwnerReference{*ref}
	}
	return nil
}

// Create starts a new bare Pod running the given listener definition and
// brings the flow's Service, routes, and target Services in line. It is
// idempotent: a Pod already running this exact definition is kept.
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
	bindings := ""
	if def.UsesPython() {
		if bindings, err = functionrpc.Environment(ctx, def); err != nil {
			return err
		}
	}
	hash := specHash(raw, k.image, bindings)
	pod := k.listenerPod(def, raw, bindings, hash)

	createCtx, cancel := apiCall(ctx)
	_, err = k.cs.CoreV1().Pods(k.namespace).Create(createCtx, pod, metav1.CreateOptions{})
	cancel()
	if apierrors.IsAlreadyExists(err) {
		err = k.adoptExisting(ctx, def.Name, pod)
	}
	if err != nil {
		return fmt.Errorf("kube driver: create pod for %q: %w", def.Name, err)
	}
	err = k.reconcileService(ctx, def)
	if err == nil {
		err = k.reconcileTargetServices(ctx, def)
	}
	if err != nil {
		// A partial create must be retryable by the controller.
		cleanupCtx, cancel := apiCall(ctx)
		defer cancel()
		if cleanupErr := k.cs.CoreV1().Pods(k.namespace).Delete(cleanupCtx, pod.Name, metav1.DeleteOptions{}); cleanupErr != nil && !apierrors.IsNotFound(cleanupErr) {
			return fmt.Errorf("%w; pod rollback: %v", err, cleanupErr)
		}
		return err
	}
	return nil
}

// adoptExisting handles Create finding a Pod with the desired name: a Pod
// that already runs this definition is kept (and re-stamped as the flow's
// current one); a finished or terminating one is removed and ErrPending asks
// the controller to retry once it is gone.
func (k *Kube) adoptExisting(ctx context.Context, flow string, desired *corev1.Pod) error {
	getCtx, cancel := apiCall(ctx)
	old, err := k.cs.CoreV1().Pods(k.namespace).Get(getCtx, desired.Name, metav1.GetOptions{})
	cancel()
	if err != nil {
		return err
	}
	if old.Labels[kubeLabelManaged] != "true" || old.Labels[kubeLabelListener] != flow {
		return fmt.Errorf("pod %q exists and is not managed by GoFi", desired.Name)
	}
	if old.DeletionTimestamp != nil {
		return ErrPending
	}
	if old.Status.Phase == corev1.PodFailed || old.Status.Phase == corev1.PodSucceeded {
		if err := k.deletePod(ctx, old.Name); err != nil {
			return err
		}
		return ErrPending
	}
	// The same definition is already running (an edit reverted to it):
	// make it the flow's current Pod again.
	patch := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`, annotationActivated, desired.Annotations[annotationActivated])
	patchCtx, cancel := apiCall(ctx)
	defer cancel()
	_, err = k.cs.CoreV1().Pods(k.namespace).Patch(patchCtx, old.Name, mergePatch, []byte(patch), metav1.PatchOptions{})
	return err
}

// listenerPod renders the Pod for a definition.
func (k *Kube) listenerPod(def spec.FlowDefinition, raw []byte, bindings, hash string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runtimePodName(def.Name, hash),
			Namespace: k.namespace,
			Labels: map[string]string{
				kubeLabelListener: def.Name,
				kubeLabelManaged:  "true",
			},
			Annotations:     map[string]string{annotationSpecHash: hash, annotationActivated: time.Now().UTC().Format(time.RFC3339Nano)},
			OwnerReferences: k.ownerOf(def.Name),
		},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: boolPtr(false),
			SecurityContext:              &corev1.PodSecurityContext{RunAsNonRoot: boolPtr(true), RunAsUser: int64Ptr(65532), RunAsGroup: int64Ptr(65532), FSGroup: int64Ptr(65532), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			// GoFi's own reconcile (controller.Controller.Run), not
			// Kubernetes, is the recreate path on crash — this keeps
			// crashes visible instead of silently hidden by kubelet restarts.
			RestartPolicy: corev1.RestartPolicyNever,
			// The runtime drains on SIGTERM (see listenerLifecycle).
			TerminationGracePeriodSeconds: int64Ptr(int64(stopGrace() / time.Second)),
			Containers: []corev1.Container{
				{
					Name:  "gofi-listener",
					Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 9091, Protocol: corev1.ProtocolTCP}},
					Image: k.image,
					// Empty keeps the Kubernetes default; dev installs reusing one tag set Always.
					ImagePullPolicy: corev1.PullPolicy(os.Getenv("GOFI_IMAGE_PULL_POLICY")),
					SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: boolPtr(false), ReadOnlyRootFilesystem: boolPtr(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
					Resources:       k.listenerDefaults.ResourcesOr(corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")}, Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")}}),
					Lifecycle:       listenerLifecycle(),
					Env: []corev1.EnvVar{
						{Name: spec.FlowSpecEnv, Value: string(raw)},
						{Name: "GOFI_K8S_NAMESPACE", Value: k.namespace},
						{Name: DrainTimeoutEnv, Value: DrainTimeout().String()},
					},
				},
			},
		},
	}
	if bindings != "" {
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{Name: "GOFI_PYTHON_BINDINGS", Value: bindings})
	}
	// The Go runtime collects before the kernel's OOM killer acts.
	if limit := kubeclient.GoMemLimit(pod.Spec.Containers[0].Resources.Limits); limit != "" {
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{Name: "GOMEMLIMIT", Value: limit})
	}
	// Probes hit the relay's /healthz (taprelay), which serves once the
	// listener runs.
	k.listenerDefaults.ApplyProbes(&pod.Spec.Containers[0], kubeTapRelayPort, "/healthz")
	k.listenerDefaults.ApplyScheduling(&pod.Spec)
	// Operator environment for every listener (chart listenerPods.extraEnv and
	// GOFI_PROFILE); FLOW_SPEC and the other driver-set names stay as set.
	k.listenerDefaults.ApplyEnv(&pod.Spec.Containers[0])
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
	return pod
}

// Replace starts a Pod for the new definition beside the running one. The
// old Pod keeps serving until the new one is Running; List then removes it.
// The Service keeps its address and selector throughout, so an update is
// not an outage. A Create that finds its Pod already present is a no-op.
func (k *Kube) Replace(ctx context.Context, def spec.FlowDefinition) error {
	return k.Create(ctx, def)
}

// Delete removes the listener's Pods, Service, routes, and target Services.
// Deletion is asynchronous: the Pods are marked for termination and drain on
// their own; List ignores terminating Pods, and Create of the same
// definition reports ErrPending until they are gone.
func (k *Kube) Delete(ctx context.Context, name string) error {
	if err := k.deleteTargetServices(ctx, name); err != nil {
		return err
	}
	if err := k.deleteGatewayRoute(ctx, name); err != nil {
		return err
	}
	if err := k.deleteService(ctx, name); err != nil {
		return err
	}
	pods, err := k.flowPods(ctx, name)
	if err != nil {
		return err
	}
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			continue
		}
		if err := k.deletePod(ctx, pod.Name); err != nil {
			return err
		}
	}
	// Pods named before hashed names (one per flow, no suffix) are also the
	// flow's; the label listing above includes them.
	return nil
}

func (k *Kube) deletePod(ctx context.Context, name string) error {
	ctx, cancel := apiCall(ctx)
	defer cancel()
	if err := k.cs.CoreV1().Pods(k.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("kube driver: delete pod %q: %w", name, err)
	}
	return nil
}

// flowPods lists every Pod labeled for a flow (any phase).
func (k *Kube) flowPods(ctx context.Context, name string) ([]*corev1.Pod, error) {
	pods, err := k.read().pods(ctx, map[string]string{kubeLabelManaged: "true", kubeLabelListener: name})
	if err != nil {
		return nil, fmt.Errorf("kube driver: list pods of %q: %w", name, err)
	}
	return pods, nil
}

// flowRuntime is a flow's Pods sorted by activation (newest first) with the
// current runtime picked out.
type flowRuntime struct {
	// current is the newest live (Pending or Running, not terminating) Pod;
	// nil when the flow has none.
	current *corev1.Pod
	// latest is the newest Pod of any phase (a Failed one reports a crash).
	latest *corev1.Pod
	// superseded are live Pods older than current.
	superseded []*corev1.Pod
}

func podLive(pod *corev1.Pod) bool {
	return pod.DeletionTimestamp == nil && (pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodRunning)
}

// activation orders Pods: the activation stamp, then creation, then name.
func activation(pod *corev1.Pod) string {
	if stamp := pod.Annotations[annotationActivated]; stamp != "" {
		return stamp
	}
	return pod.CreationTimestamp.UTC().Format(time.RFC3339Nano)
}

func newestFirst(pods []*corev1.Pod) {
	sort.SliceStable(pods, func(i, j int) bool {
		a, b := activation(pods[i]), activation(pods[j])
		if a != b {
			return a > b
		}
		return pods[i].Name > pods[j].Name
	})
}

func runtimeOf(pods []*corev1.Pod) flowRuntime {
	sorted := append([]*corev1.Pod(nil), pods...)
	newestFirst(sorted)
	var out flowRuntime
	for _, pod := range sorted {
		if pod.DeletionTimestamp != nil {
			continue
		}
		if out.latest == nil {
			out.latest = pod
		}
		if !podLive(pod) {
			continue
		}
		if out.current == nil {
			out.current = pod
		} else {
			out.superseded = append(out.superseded, pod)
		}
	}
	return out
}

// List returns the definitions of all gofi-managed flows with a live Pod,
// decoded back from each Pod's FLOW_SPEC env var, and repairs each flow's
// Service, routes, and target Services (no writes when they already match).
// A flow whose newest Pod is Running has its older Pods removed here: that
// completes a Replace once the new runtime is up.
func (k *Kube) List(ctx context.Context) ([]spec.FlowDefinition, error) {
	pods, err := k.read().pods(ctx, map[string]string{kubeLabelManaged: "true"})
	if err != nil {
		return nil, fmt.Errorf("kube driver: list pods: %w", err)
	}
	byFlow := map[string][]*corev1.Pod{}
	for _, pod := range pods {
		byFlow[pod.Labels[kubeLabelListener]] = append(byFlow[pod.Labels[kubeLabelListener]], pod)
	}
	names := make([]string, 0, len(byFlow))
	for name := range byFlow {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]spec.FlowDefinition, 0, len(names))
	for _, name := range names {
		def, ok := k.listFlow(ctx, name, byFlow[name])
		if ok {
			out = append(out, def)
		}
	}
	return out, nil
}

// Lookup is List for one flow: its running definition (ok false when none
// is live) after repairing its exposure objects.
func (k *Kube) Lookup(ctx context.Context, name string) (spec.FlowDefinition, bool, error) {
	pods, err := k.flowPods(ctx, name)
	if err != nil {
		return spec.FlowDefinition{}, false, err
	}
	def, ok := k.listFlow(ctx, name, pods)
	return def, ok, nil
}

func (k *Kube) listFlow(ctx context.Context, name string, pods []*corev1.Pod) (spec.FlowDefinition, bool) {
	rt := runtimeOf(pods)
	if rt.current == nil {
		return spec.FlowDefinition{}, false
	}
	if rt.current.Status.Phase == corev1.PodRunning {
		for _, old := range rt.superseded {
			if err := k.deletePod(ctx, old.Name); err != nil {
				slog.Warn("kube driver: remove superseded pod", "flow", name, "pod", old.Name, "error", err)
			} else {
				slog.Info("kube driver: superseded pod removed", "flow", name, "pod", old.Name, "current", rt.current.Name)
			}
		}
	}
	def, ok := decodePodListenerSpec(rt.current)
	if !ok {
		slog.Warn("kube driver: managed pod missing/invalid flow spec, skipping", "pod", rt.current.Name)
		return spec.FlowDefinition{}, false
	}
	// A Service, route, or destination problem belongs to this flow: it is
	// reported on the flow, and the rest keep reconciling.
	err := k.reconcileService(ctx, def)
	if err == nil {
		err = k.reconcileTargetServices(ctx, def)
	}
	k.recordNetworking(def.Name, err)
	return def, true
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

// currentPod is a flow's current runtime Pod (nil when none is live) and
// its newest Pod of any phase.
func (k *Kube) currentPod(ctx context.Context, name string) (current, latest *corev1.Pod, err error) {
	pods, err := k.flowPods(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	rt := runtimeOf(pods)
	return rt.current, rt.latest, nil
}

// Tap returns the listener Pod's IP as the tap relay address, if the Pod is
// Running and has an assigned IP. With informers this costs no API call.
func (k *Kube) Tap(ctx context.Context, name string) (TapTarget, bool, error) {
	pod, _, err := k.currentPod(ctx, name)
	if err != nil {
		return TapTarget{}, false, err
	}
	if pod == nil || pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
		return TapTarget{}, false, nil
	}
	return TapTarget{Addr: fmt.Sprintf("%s:%d", pod.Status.PodIP, kubeTapRelayPort)}, true, nil
}

// RuntimeStatus reports the flow's current Pod: phase, name, node, and IP.
// With no live Pod it reports the newest finished one (a crash the
// controller will restart); ok is false when the flow has no Pod at all.
func (k *Kube) RuntimeStatus(ctx context.Context, name string) (RuntimeInfo, bool, error) {
	current, latest, err := k.currentPod(ctx, name)
	if err != nil {
		return RuntimeInfo{}, false, err
	}
	pod := current
	if pod == nil {
		pod = latest
	}
	if pod == nil {
		return RuntimeInfo{}, false, nil
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
		// A Problem on the flow: the portal shows it beside the runtime.
		observability.Report(name, observability.SeverityError, observability.ComponentRuntime, "networking: "+err.Error())
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
	svc, err := k.read().service(ctx, podName(name))
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
	ctx, cancel := apiCall(ctx)
	defer cancel()
	err = k.cs.CoreV1().Services(k.namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// desiredService renders the flow's exposure Service.
func (k *Kube) desiredService(def spec.FlowDefinition) *corev1.Service {
	cfg := def.Source.Listening().Service
	port := cfg.Port
	if port == 0 {
		port = def.Source.Listening().Port
	}
	protocol := transportOf(def.Source.Listening().Network) // udp and quic: UDP
	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: podName(def.Name), Namespace: k.namespace,
			Labels:          map[string]string{kubeLabelManaged: "true", kubeLabelListener: def.Name},
			OwnerReferences: k.ownerOf(def.Name)},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceType(cfg.Type),
			Selector: map[string]string{kubeLabelManaged: "true", kubeLabelListener: def.Name},
			Ports:    []corev1.ServicePort{{Name: "data", Protocol: protocol, Port: int32(port), TargetPort: intstr.FromInt(def.Source.Listening().Port), NodePort: int32(cfg.NodePort)}}},
	}
	applyExposure(desired, cfg, k.lbAnnotations)
	if desired.Spec.Type == corev1.ServiceTypeLoadBalancer && k.lbAllocateNodePorts != nil {
		allocate := *k.lbAllocateNodePorts
		desired.Spec.AllocateLoadBalancerNodePorts = &allocate
	}
	return desired
}

func (k *Kube) reconcileService(ctx context.Context, def spec.FlowDefinition) error {
	if def.Source.Listening().Service == nil {
		if err := k.deleteGatewayRoute(ctx, def.Name); err != nil {
			return err
		}
		return k.deleteService(ctx, def.Name)
	}
	cfg := def.Source.Listening().Service
	desired := k.desiredService(def)
	services := k.cs.CoreV1().Services(k.namespace)
	current, err := k.read().service(ctx, desired.Name)
	if apierrors.IsNotFound(err) {
		createCtx, cancel := apiCall(ctx)
		_, err = services.Create(createCtx, desired, metav1.CreateOptions{})
		cancel()
		if apierrors.IsAlreadyExists(err) {
			// Not in the managed cache, yet present: someone else's Service.
			return fmt.Errorf("kube driver: service %q already exists and is unmanaged", desired.Name)
		}
		if err != nil {
			return err
		}
		return k.reconcileGatewayRoute(ctx, def)
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
	// the declared routing fields; write only when something differs.
	merged := current.DeepCopy()
	merged.Spec.Type = desired.Spec.Type
	merged.Spec.Selector = desired.Spec.Selector
	merged.Spec.Ports = desired.Spec.Ports
	merged.OwnerReferences = desired.OwnerReferences
	merged.Spec.AllocateLoadBalancerNodePorts = desired.Spec.AllocateLoadBalancerNodePorts
	if merged.Spec.Type != corev1.ServiceTypeLoadBalancer {
		merged.Spec.AllocateLoadBalancerNodePorts = nil
	}
	updateExposure(merged, desired)
	if !serviceEqual(current, merged) {
		updateCtx, cancel := apiCall(ctx)
		_, err = services.Update(updateCtx, merged, metav1.UpdateOptions{})
		cancel()
		if err != nil {
			return err
		}
	}
	return k.reconcileGatewayRoute(ctx, def)
}

// serviceEqual compares the fields GoFi manages on a Service. Server-defaulted
// port fields are tolerated so an unchanged Service is never rewritten.
func serviceEqual(current, desired *corev1.Service) bool {
	if current.Spec.Type != desired.Spec.Type || current.Spec.ExternalTrafficPolicy != desired.Spec.ExternalTrafficPolicy {
		return false
	}
	if !apiequality.Semantic.DeepEqual(current.Spec.Selector, desired.Spec.Selector) || !apiequality.Semantic.DeepEqual(current.Spec.LoadBalancerClass, desired.Spec.LoadBalancerClass) {
		return false
	}
	if current.Spec.Type == corev1.ServiceTypeLoadBalancer && !apiequality.Semantic.DeepEqual(current.Spec.AllocateLoadBalancerNodePorts, desired.Spec.AllocateLoadBalancerNodePorts) {
		return false
	}
	if !apiequality.Semantic.DeepEqual(current.OwnerReferences, desired.OwnerReferences) || !apiequality.Semantic.DeepEqual(current.Labels, desired.Labels) || !apiequality.Semantic.DeepEqual(current.Annotations, desired.Annotations) {
		return false
	}
	if len(current.Spec.Ports) != len(desired.Spec.Ports) {
		return false
	}
	for i := range current.Spec.Ports {
		a, b := current.Spec.Ports[i], desired.Spec.Ports[i]
		if a.Name != b.Name || a.Port != b.Port || a.NodePort != b.NodePort || a.TargetPort != b.TargetPort {
			return false
		}
		if protocolOrTCP(a.Protocol) != protocolOrTCP(b.Protocol) {
			return false
		}
	}
	return true
}

func protocolOrTCP(p corev1.Protocol) corev1.Protocol {
	if p == "" {
		return corev1.ProtocolTCP
	}
	return p
}

// Endpoints lists the Services and Pods of the namespace as destinations a
// flow can send to. The listing pages through the API and stops at
// maxDiscoveryItems with a warning rather than failing.
func (k *Kube) Endpoints(ctx context.Context) ([]Endpoint, error) {
	services, truncated, err := kubeclient.ListAll(ctx, maxDiscoveryItems, "", func(ctx context.Context, opts metav1.ListOptions) ([]corev1.Service, string, error) {
		ctx, cancel := apiCall(ctx)
		defer cancel()
		list, err := k.cs.CoreV1().Services(k.namespace).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return list.Items, list.Continue, nil
	})
	if err != nil {
		return nil, fmt.Errorf("kube driver: discover services: %w", err)
	}
	if truncated {
		slog.Warn("kube driver: " + kubeclient.TruncationWarning("Service discovery", maxDiscoveryItems))
	}
	out := make([]Endpoint, 0, len(services))
	for _, svc := range services {
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
