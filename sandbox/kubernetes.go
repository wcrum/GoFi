package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"maps"
	"net"
	"os"
	"slices"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/wcrum/gofi/kubeclient"
)

// Kubernetes runs each Python library as a worker Pod (or, with Replicas > 1,
// a Deployment) behind a Service, with its token in a Secret and a
// NetworkPolicy that admits only GoFi and denies egress except the library's
// declared rules.
//
// The Secret also records a hash of the library's runtime definition. On a
// controller restart Adopt finds the worker, reads its token, checks its
// health, and reuses it: workers (and the listener Pods whose bindings carry
// the token) are not recreated when only the controller restarted. The token
// rotates only when the definition changes (Start creates everything anew).
type Kubernetes struct {
	Client           kubernetes.Interface
	Namespace, Image string
	// Defaults are the chart's resources, scheduling, and probes for worker
	// Pods (GOFI_WORKER_RESOURCES/_SCHEDULING/_PROBES); the Sandbox's memoryMib
	// overrides the memory request and limit.
	Defaults kubeclient.PodDefaults
	// Replicas > 1 runs each library as a Deployment (GOFI_WORKER_REPLICAS).
	Replicas int32
	// health probes a worker (checkRPC); tests replace it.
	health func(context.Context, Binding) error
}

// annotationDefinitionHash records the runtime definition a worker runs.
const annotationDefinitionHash = "gofi.wcrum.dev/definition-hash"

// NewKubernetes uses the shared in-cluster client (kubeclient) and the
// chart's worker defaults.
func NewKubernetes(namespace, image string) (*Kubernetes, error) {
	if namespace == "" {
		return nil, fmt.Errorf("sandbox namespace required")
	}
	clients, e := kubeclient.Get()
	if e != nil {
		return nil, e
	}
	if image == "" {
		image = DefaultImage
	}
	defaults, e := kubeclient.LoadPodDefaults("WORKER")
	if e != nil {
		return nil, e
	}
	k := &Kubernetes{Client: clients.Clientset, Namespace: namespace, Image: image, Defaults: defaults, Replicas: 1}
	if raw := os.Getenv("GOFI_WORKER_REPLICAS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 1 {
			k.Replicas = int32(n)
		}
	}
	return k, nil
}

// definitionHash identifies what a worker runs: the fields runtimeEqual
// compares (including the AI environment, so a changed endpoint or key is
// a changed definition), plus the image. The AI egress rules are derived
// from that environment and left out, so a re-resolved address alone does
// not rotate a worker.
func definitionHash(d Definition, image string) string {
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\x00%s\x00%d\x00%d\x00%s\x00", d.Name, d.SourceCode, d.TimeoutSeconds, d.MemoryMiB, image)
	for _, r := range d.Egress {
		if r.Description == AIEgressReason {
			continue
		}
		fmt.Fprintf(sum, "%s|%d|%s|%s|%v\x00", r.CIDR, r.Port, r.Protocol, r.Namespace, r.PodSelector)
	}
	for _, pair := range sortedEnv(d.Env) {
		fmt.Fprintf(sum, "env:%s\x00", pair)
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

func (k *Kubernetes) image(d Definition) string {
	if d.Image != "" {
		return d.Image
	}
	return k.Image
}

func workerLabels(name string) map[string]string {
	return map[string]string{"app.kubernetes.io/name": "gofi-python", "gofi.sandbox": name, "gofi.managed-by": "gofi"}
}

func (k *Kubernetes) endpoint(name string) string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", name, k.Namespace)
}

func (k *Kubernetes) Start(ctx context.Context, d Definition, token string) (string, error) {
	if e := k.Stop(ctx, d.Name); e != nil {
		return "", e
	}
	name := "gofi-python-" + d.Name
	labels := workerLabels(d.Name)
	hash := definitionHash(d, k.image(d))
	m := metav1.ObjectMeta{Name: name, Namespace: k.Namespace, Labels: labels, Annotations: map[string]string{annotationDefinitionHash: hash}}
	_, e := k.Client.CoreV1().Secrets(k.Namespace).Create(ctx, &core.Secret{ObjectMeta: m, StringData: map[string]string{"token": token}}, metav1.CreateOptions{})
	if e != nil {
		return "", e
	}
	_, e = k.Client.CoreV1().Services(k.Namespace).Create(ctx, &core.Service{ObjectMeta: m, Spec: core.ServiceSpec{Selector: labels, Ports: []core.ServicePort{{Name: "rpc", Port: 8080, TargetPort: intstr.FromInt(8080)}}}}, metav1.CreateOptions{})
	if e != nil {
		return "", e
	}
	_, e = k.Client.NetworkingV1().NetworkPolicies(k.Namespace).Create(ctx, WorkerNetworkPolicy(m, labels, d.Egress), metav1.CreateOptions{})
	if e != nil {
		return "", e
	}
	podSpec := k.workerPodSpec(d, name)
	if k.Replicas > 1 && !d.Scratch {
		replicas := k.Replicas
		deployment := &appsv1.Deployment{ObjectMeta: m, Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: core.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: map[string]string{annotationDefinitionHash: hash}}, Spec: podSpec},
		}}
		if _, e = k.Client.AppsV1().Deployments(k.Namespace).Create(ctx, deployment, metav1.CreateOptions{}); e != nil {
			return "", e
		}
	} else {
		pod := &core.Pod{ObjectMeta: m, Spec: podSpec}
		if _, e = k.Client.CoreV1().Pods(k.Namespace).Create(ctx, pod, metav1.CreateOptions{}); e != nil {
			return "", e
		}
	}
	endpoint := k.endpoint(name)
	if e = waitReady(ctx, endpoint, token, func(ctx context.Context) error { return k.crashed(ctx, d.Name) }); e != nil {
		return "", e
	}
	return endpoint, nil
}

// workerPodSpec renders the worker Pod: the chart's defaults, the library's
// memory, probes on the RPC port, and GOMEMLIMIT from the memory limit.
func (k *Kubernetes) workerPodSpec(d Definition, secret string) core.PodSpec {
	uid, gid := int64(65532), int64(65532)
	nonroot, readOnly, escalation, automount := true, true, false, false
	memory := resource.MustParse(fmt.Sprintf("%dMi", d.MemoryMiB))
	resources := k.Defaults.ResourcesOr(core.ResourceRequirements{
		Requests: core.ResourceList{core.ResourceCPU: resource.MustParse("50m")},
		Limits:   core.ResourceList{core.ResourceCPU: resource.MustParse("250m")}})
	if resources.Requests == nil {
		resources.Requests = core.ResourceList{}
	}
	if resources.Limits == nil {
		resources.Limits = core.ResourceList{}
	}
	resources.Requests[core.ResourceMemory] = memory
	resources.Limits[core.ResourceMemory] = memory
	container := core.Container{Name: "worker", Image: k.image(d), ImagePullPolicy: pullPolicy(),
		Env: []core.EnvVar{
			{Name: "GOFI_PYTHON_SOURCE", Value: base64.StdEncoding.EncodeToString([]byte(d.SourceCode))},
			{Name: "GOFI_TIMEOUT_SECONDS", Value: fmt.Sprint(d.TimeoutSeconds)},
			{Name: "PYTHONDONTWRITEBYTECODE", Value: "1"},
			{Name: "GOFI_LAZY_LOAD", Value: lazyValue(d)},
			{Name: "GOFI_SANDBOX_TOKEN", ValueFrom: &core.EnvVarSource{SecretKeyRef: &core.SecretKeySelector{LocalObjectReference: core.LocalObjectReference{Name: secret}, Key: "token"}}},
		},
		Ports:                    []core.ContainerPort{{Name: "rpc", ContainerPort: 8080}},
		TerminationMessagePolicy: core.TerminationMessageFallbackToLogsOnError,
		SecurityContext:          &core.SecurityContext{ReadOnlyRootFilesystem: &readOnly, AllowPrivilegeEscalation: &escalation, Capabilities: &core.Capabilities{Drop: []core.Capability{"ALL"}}},
		Resources:                resources,
		VolumeMounts:             []core.VolumeMount{{Name: "tmp", MountPath: "/tmp"}},
		// Replaced by the chart's probes when configured.
		ReadinessProbe: &core.Probe{ProbeHandler: core.ProbeHandler{HTTPGet: &core.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt(8080)}}, PeriodSeconds: 1},
	}
	if limit := kubeclient.GoMemLimit(resources.Limits); limit != "" {
		container.Env = append(container.Env, core.EnvVar{Name: "GOMEMLIMIT", Value: limit})
	}
	// An AI library's proxy address, provider, and model (no secret: the
	// worker authenticates to the proxy with its token).
	for _, key := range slices.Sorted(maps.Keys(d.Env)) {
		container.Env = append(container.Env, core.EnvVar{Name: key, Value: d.Env[key]})
	}
	k.Defaults.ApplyProbes(&container, 8080, "/healthz")
	// Operator environment for every worker (chart workers.extraEnv); the
	// source, token, and timeout variables above stay as set.
	k.Defaults.ApplyEnv(&container)
	spec := core.PodSpec{AutomountServiceAccountToken: &automount, RestartPolicy: core.RestartPolicyAlways,
		SecurityContext: &core.PodSecurityContext{RunAsUser: &uid, RunAsGroup: &gid, RunAsNonRoot: &nonroot, SeccompProfile: &core.SeccompProfile{Type: core.SeccompProfileTypeRuntimeDefault}},
		Containers:      []core.Container{container},
		Volumes:         []core.Volume{{Name: "tmp", VolumeSource: core.VolumeSource{EmptyDir: &core.EmptyDirVolumeSource{Medium: core.StorageMediumMemory, SizeLimit: resource.NewQuantity(16*1024*1024, resource.BinarySI)}}}}}
	k.Defaults.ApplyScheduling(&spec)
	return spec
}

// Adopt reuses a worker an earlier controller started for the same
// definition: its Secret must be GoFi's, record the same definition hash,
// and the worker must answer with that token. ok is false when there is no
// such worker (Start then creates one).
func (k *Kubernetes) Adopt(ctx context.Context, d Definition) (endpoint, token string, ok bool, err error) {
	name := "gofi-python-" + d.Name
	secret, e := k.Client.CoreV1().Secrets(k.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(e) {
		return "", "", false, nil
	}
	if e != nil {
		return "", "", false, e
	}
	if secret.Labels["gofi.managed-by"] != "gofi" || secret.Labels["gofi.sandbox"] != d.Name {
		return "", "", false, nil
	}
	if secret.Annotations[annotationDefinitionHash] != definitionHash(d, k.image(d)) {
		return "", "", false, nil // the definition changed: rotate
	}
	token = string(secret.Data["token"])
	if token == "" {
		token = secret.StringData["token"]
	}
	if token == "" {
		return "", "", false, nil
	}
	endpoint = k.endpoint(name)
	health := k.health
	if health == nil {
		health = checkRPC
	}
	if e := health(ctx, Binding{Endpoint: endpoint, Token: token, TimeoutSeconds: d.TimeoutSeconds}); e != nil {
		return "", "", false, nil // not healthy (or still starting): recreate
	}
	return endpoint, token, true, nil
}

// ControllerAddress is how a worker reaches the AI proxy: the Service of
// this namespace that selects the controller's own Pod, on the Service port
// whose target is the proxy port (or GOFI_LLM_PROXY_HOST, host:port, when
// set). The egress rule admits the controller Pods on the proxy port only,
// selected by the running Pod's own labels (NetworkPolicy matches the Pods
// behind the Service, after its DNAT).
func (k *Kubernetes) ControllerAddress(ctx context.Context, proxyPort int) (string, []EgressRule, error) {
	self, err := k.Client.CoreV1().Pods(k.Namespace).Get(ctx, os.Getenv("HOSTNAME"), metav1.GetOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("controller Pod %q lookup: %w", os.Getenv("HOSTNAME"), err)
	}
	selector := controllerSelector(self.Labels)
	if len(selector) == 0 {
		return "", nil, fmt.Errorf("controller Pod %q has no labels to select it by", self.Name)
	}
	rules := []EgressRule{{Namespace: k.Namespace, PodSelector: selector, Port: proxyPort, Protocol: "tcp"}}
	if host := os.Getenv("GOFI_LLM_PROXY_HOST"); host != "" {
		return host, rules, nil
	}
	services, err := k.Client.CoreV1().Services(k.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", nil, err
	}
	for _, svc := range services.Items {
		if len(svc.Spec.Selector) == 0 || !labels.SelectorFromSet(svc.Spec.Selector).Matches(labels.Set(self.Labels)) {
			continue
		}
		for _, p := range svc.Spec.Ports {
			if targetPortNumber(p, self) == proxyPort {
				// The ClusterIP, so a worker needs no DNS to reach the proxy
				// (a worker with only this rule gets no DNS egress at all).
				if ip := net.ParseIP(svc.Spec.ClusterIP); ip != nil {
					return net.JoinHostPort(ip.String(), strconv.Itoa(int(p.Port))), rules, nil
				}
				return fmt.Sprintf("%s.%s.svc.cluster.local:%d", svc.Name, k.Namespace, p.Port), rules, nil
			}
		}
	}
	return "", nil, fmt.Errorf("no Service selecting the controller Pod exposes the AI proxy port %d (chart llmProxy.port); set GOFI_LLM_PROXY_HOST", proxyPort)
}

// controllerSelector derives a pod selector from the controller's own
// labels: the app.kubernetes.io name/instance/component labels when present
// (what the chart's Service uses), else every label but the ReplicaSet and
// revision hashes.
func controllerSelector(podLabels map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"app.kubernetes.io/name", "app.kubernetes.io/instance", "app.kubernetes.io/component"} {
		if v, ok := podLabels[key]; ok {
			out[key] = v
		}
	}
	if len(out) > 0 {
		return out
	}
	for key, v := range podLabels {
		if key != "pod-template-hash" && key != "controller-revision-hash" {
			out[key] = v
		}
	}
	return out
}

// targetPortNumber resolves a Service port's target on pod: a number, a
// named container port, or the Service port itself.
func targetPortNumber(p core.ServicePort, pod *core.Pod) int {
	if p.TargetPort.Type == intstr.String && p.TargetPort.StrVal != "" {
		for _, c := range pod.Spec.Containers {
			for _, cp := range c.Ports {
				if cp.Name == p.TargetPort.StrVal {
					return int(cp.ContainerPort)
				}
			}
		}
		return 0
	}
	if n := p.TargetPort.IntValue(); n > 0 {
		return n
	}
	return int(p.Port)
}

// KubernetesEgressPolicy describes Kubernetes-mode egress for DescribeEnvironment.
const KubernetesEgressPolicy = "Kubernetes: each worker Pod has a NetworkPolicy that denies all egress. A library's egress rules become per-destination NetworkPolicy egress rules (CIDR and/or namespace + pod selector, port, protocol); DNS to kube-system/kube-dns is allowed only when the library declares a rule of its own (the controller's AI proxy is reached by IP). NetworkPolicy cannot match hostnames."

func lazyValue(d Definition) string {
	if d.Scratch {
		return "1"
	}
	return "0"
}

// WorkerNetworkPolicy admits only GoFi to the worker's RPC port and denies
// all egress except the library's declared rules (plus cluster DNS when the
// library declared any rule of its own, so in-cluster names resolve). The
// controller's AI proxy rule alone brings no DNS: the proxy is reached by
// IP, and DNS would otherwise be a covert channel for every worker.
func WorkerNetworkPolicy(m metav1.ObjectMeta, labels map[string]string, egress []EgressRule) *netv1.NetworkPolicy {
	port := intstr.FromInt(8080)
	protocol := core.ProtocolTCP
	rules := []netv1.NetworkPolicyEgressRule{}
	needsDNS := false
	for _, r := range egress {
		if r.Description != AIEgressReason {
			needsDNS = true
		}
		rule := netv1.NetworkPolicyEgressRule{}
		peer := netv1.NetworkPolicyPeer{}
		if r.CIDR != "" {
			peer.IPBlock = &netv1.IPBlock{CIDR: r.CIDR}
		} else {
			if r.Namespace != "" {
				peer.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": r.Namespace}}
			}
			// An empty pod selector within a namespace selects every Pod there.
			peer.PodSelector = &metav1.LabelSelector{MatchLabels: r.PodSelector}
		}
		rule.To = []netv1.NetworkPolicyPeer{peer}
		ruleProtocol := core.ProtocolTCP
		if r.Protocol == "udp" {
			ruleProtocol = core.ProtocolUDP
		}
		policyPort := netv1.NetworkPolicyPort{Protocol: &ruleProtocol}
		if r.Port > 0 {
			value := intstr.FromInt(r.Port)
			policyPort.Port = &value
		}
		rule.Ports = []netv1.NetworkPolicyPort{policyPort}
		rules = append(rules, rule)
	}
	if needsDNS {
		dnsPort := intstr.FromInt(53)
		udp, tcp := core.ProtocolUDP, core.ProtocolTCP
		rules = append(rules, netv1.NetworkPolicyEgressRule{
			To: []netv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}},
				PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}},
			}},
			Ports: []netv1.NetworkPolicyPort{{Protocol: &udp, Port: &dnsPort}, {Protocol: &tcp, Port: &dnsPort}},
		})
	}
	return &netv1.NetworkPolicy{ObjectMeta: m, Spec: netv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: labels},
		PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress, netv1.PolicyTypeEgress},
		Ingress: []netv1.NetworkPolicyIngressRule{{From: []netv1.NetworkPolicyPeer{
			{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "gofi"}}},
			{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"gofi.io/managed-by": "true"}}},
		}, Ports: []netv1.NetworkPolicyPort{{Protocol: &protocol, Port: &port}}}},
		Egress: rules,
	}}
}

func (k *Kubernetes) Stop(ctx context.Context, name string) error {
	name = "gofi-python-" + name
	// Never replace an unrelated resource merely because its name collides.
	getters := []func() (metav1.Object, error){
		func() (metav1.Object, error) {
			return k.Client.CoreV1().Pods(k.Namespace).Get(ctx, name, metav1.GetOptions{})
		},
		func() (metav1.Object, error) {
			return k.Client.AppsV1().Deployments(k.Namespace).Get(ctx, name, metav1.GetOptions{})
		},
		func() (metav1.Object, error) {
			return k.Client.CoreV1().Services(k.Namespace).Get(ctx, name, metav1.GetOptions{})
		},
		func() (metav1.Object, error) {
			return k.Client.CoreV1().Secrets(k.Namespace).Get(ctx, name, metav1.GetOptions{})
		},
		func() (metav1.Object, error) {
			return k.Client.NetworkingV1().NetworkPolicies(k.Namespace).Get(ctx, name, metav1.GetOptions{})
		},
	}
	for _, get := range getters {
		obj, err := get()
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if obj.GetLabels()["gofi.managed-by"] != "gofi" || obj.GetLabels()["gofi.sandbox"] == "" {
			return fmt.Errorf("refusing to replace unmanaged sandbox resource %s", name)
		}
	}
	zero := int64(0)
	opts := metav1.DeleteOptions{GracePeriodSeconds: &zero}
	background := metav1.DeletePropagationBackground
	operations := []func() error{
		func() error {
			return k.Client.AppsV1().Deployments(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &background})
		},
		func() error { return k.Client.CoreV1().Pods(k.Namespace).Delete(ctx, name, opts) },
		func() error { return k.Client.CoreV1().Services(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{}) },
		func() error { return k.Client.CoreV1().Secrets(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{}) },
		func() error {
			return k.Client.NetworkingV1().NetworkPolicies(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
		},
	}
	for _, operation := range operations {
		if e := operation(); e != nil && !apierrors.IsNotFound(e) {
			return e
		}
	}
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, e := k.Client.CoreV1().Pods(k.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(e) {
			return nil
		}
		if e != nil {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("worker Pod deletion timed out")
		case <-ticker.C:
		}
	}
}

func (k *Kubernetes) Health(ctx context.Context, b Binding) error { return checkRPC(ctx, b) }

// crashed reports a worker Pod whose container terminated while starting.
// FallbackToLogsOnError puts the tail of its output (the Python traceback)
// in the termination message. Deployment-run workers are found by label.
func (k *Kubernetes) crashed(ctx context.Context, library string) error {
	pods, err := k.Client.CoreV1().Pods(k.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(workerLabels(library)).String(), Limit: 10})
	if err != nil {
		return nil
	}
	for _, pod := range pods.Items {
		for _, status := range pod.Status.ContainerStatuses {
			terminated := status.State.Terminated
			if terminated == nil {
				terminated = status.LastTerminationState.Terminated
			}
			if terminated != nil && terminated.ExitCode != 0 {
				return workerExited(int(terminated.ExitCode), terminated.Message)
			}
		}
	}
	return nil
}

// pullPolicy follows the controller's (GOFI_IMAGE_PULL_POLICY), so a dev
// install that rebuilds the same tag does not run a stale cached worker.
func pullPolicy() core.PullPolicy {
	if p := os.Getenv("GOFI_IMAGE_PULL_POLICY"); p != "" {
		return core.PullPolicy(p)
	}
	return core.PullIfNotPresent
}
