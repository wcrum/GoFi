package sandbox

import (
	"context"
	"encoding/base64"
	"fmt"
	core "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"time"
)

type Kubernetes struct {
	Client           kubernetes.Interface
	Namespace, Image string
}

func NewKubernetes(namespace, image string) (*Kubernetes, error) {
	if namespace == "" {
		return nil, fmt.Errorf("sandbox namespace required")
	}
	cfg, e := rest.InClusterConfig()
	if e != nil {
		return nil, e
	}
	c, e := kubernetes.NewForConfig(cfg)
	if e != nil {
		return nil, e
	}
	if image == "" {
		image = DefaultImage
	}
	return &Kubernetes{Client: c, Namespace: namespace, Image: image}, nil
}
func (k *Kubernetes) Start(ctx context.Context, d Definition, token string) (string, error) {
	if e := k.Stop(ctx, d.Name); e != nil {
		return "", e
	}
	name := "gofi-python-" + d.Name
	labels := map[string]string{"app.kubernetes.io/name": "gofi-python", "gofi.sandbox": d.Name, "gofi.managed-by": "gofi"}
	m := metav1.ObjectMeta{Name: name, Namespace: k.Namespace, Labels: labels}
	_, e := k.Client.CoreV1().Secrets(k.Namespace).Create(ctx, &core.Secret{ObjectMeta: m, StringData: map[string]string{"token": token}}, metav1.CreateOptions{})
	if e != nil {
		return "", e
	}
	_, e = k.Client.CoreV1().Services(k.Namespace).Create(ctx, &core.Service{ObjectMeta: m, Spec: core.ServiceSpec{Selector: labels, Ports: []core.ServicePort{{Name: "rpc", Port: 8080, TargetPort: intstr.FromInt(8080)}}}}, metav1.CreateOptions{})
	if e != nil {
		return "", e
	}
	port := intstr.FromInt(8080)
	protocol := core.ProtocolTCP
	_, e = k.Client.NetworkingV1().NetworkPolicies(k.Namespace).Create(ctx, &netv1.NetworkPolicy{ObjectMeta: m, Spec: netv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: labels}, PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress, netv1.PolicyTypeEgress}, Ingress: []netv1.NetworkPolicyIngressRule{{From: []netv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "gofi"}}}, {PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"gofi.io/managed-by": "true"}}}}, Ports: []netv1.NetworkPolicyPort{{Protocol: &protocol, Port: &port}}}}, Egress: []netv1.NetworkPolicyEgressRule{}}}, metav1.CreateOptions{})
	if e != nil {
		return "", e
	}
	uid, gid := int64(65532), int64(65532)
	nonroot, readOnly, escalation, automount := true, true, false, false
	pod := &core.Pod{ObjectMeta: m, Spec: core.PodSpec{AutomountServiceAccountToken: &automount, RestartPolicy: core.RestartPolicyAlways, SecurityContext: &core.PodSecurityContext{RunAsUser: &uid, RunAsGroup: &gid, RunAsNonRoot: &nonroot, SeccompProfile: &core.SeccompProfile{Type: core.SeccompProfileTypeRuntimeDefault}}, Containers: []core.Container{{Name: "worker", Image: k.Image, ImagePullPolicy: core.PullIfNotPresent, Env: []core.EnvVar{{Name: "GOFI_PYTHON_SOURCE", Value: base64.StdEncoding.EncodeToString([]byte(d.SourceCode))}, {Name: "GOFI_TIMEOUT_SECONDS", Value: fmt.Sprint(d.TimeoutSeconds)}, {Name: "PYTHONDONTWRITEBYTECODE", Value: "1"}, {Name: "GOFI_SANDBOX_TOKEN", ValueFrom: &core.EnvVarSource{SecretKeyRef: &core.SecretKeySelector{LocalObjectReference: core.LocalObjectReference{Name: name}, Key: "token"}}}}, Ports: []core.ContainerPort{{Name: "rpc", ContainerPort: 8080}}, SecurityContext: &core.SecurityContext{ReadOnlyRootFilesystem: &readOnly, AllowPrivilegeEscalation: &escalation, Capabilities: &core.Capabilities{Drop: []core.Capability{"ALL"}}}, Resources: core.ResourceRequirements{Requests: core.ResourceList{core.ResourceCPU: resource.MustParse("50m"), core.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", d.MemoryMiB))}, Limits: core.ResourceList{core.ResourceCPU: resource.MustParse("250m"), core.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", d.MemoryMiB))}}, VolumeMounts: []core.VolumeMount{{Name: "tmp", MountPath: "/tmp"}}, ReadinessProbe: &core.Probe{ProbeHandler: core.ProbeHandler{HTTPGet: &core.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt(8080)}}, PeriodSeconds: 1}}}, Volumes: []core.Volume{{Name: "tmp", VolumeSource: core.VolumeSource{EmptyDir: &core.EmptyDirVolumeSource{Medium: core.StorageMediumMemory, SizeLimit: resource.NewQuantity(16*1024*1024, resource.BinarySI)}}}}}}
	if _, e = k.Client.CoreV1().Pods(k.Namespace).Create(ctx, pod, metav1.CreateOptions{}); e != nil {
		return "", e
	}
	endpoint := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", name, k.Namespace)
	if e = waitReady(ctx, endpoint, token); e != nil {
		return "", e
	}
	return endpoint, nil
}
func (k *Kubernetes) Stop(ctx context.Context, name string) error {
	name = "gofi-python-" + name
	// Never replace an unrelated resource merely because its name collides.
	getters := []func() (metav1.Object, error){
		func() (metav1.Object, error) {
			return k.Client.CoreV1().Pods(k.Namespace).Get(ctx, name, metav1.GetOptions{})
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
	operations := []func() error{func() error { return k.Client.CoreV1().Pods(k.Namespace).Delete(ctx, name, opts) }, func() error { return k.Client.CoreV1().Services(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{}) }, func() error { return k.Client.CoreV1().Secrets(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{}) }, func() error {
		return k.Client.NetworkingV1().NetworkPolicies(k.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	}}
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
