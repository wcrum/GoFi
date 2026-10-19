//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/wcrum/gofi/internal/testkit/gofiapi"
	"github.com/wcrum/gofi/internal/testkit/invariants"
	"github.com/wcrum/gofi/internal/testkit/traffic"
	"github.com/wcrum/gofi/spec"
)

// kubeDeployment targets a cluster that scripts/load/k3d-up.sh prepared: GoFi
// installed from charts/gofi in namespace GOFI_E2E_NAMESPACE (default gofi),
// the controller reachable at GOFI_E2E_API (the chart Service published
// through the k3d load balancer), flow NodePorts published on
// GOFI_E2E_NODE_HOST, and the host reachable from Pods as
// GOFI_E2E_HOST_ALIAS (k3d: host.k3d.internal).
type kubeDeployment struct {
	api       *gofiapi.Client
	namespace string
	nodeHost  string
	hostAlias string
	cs        kubernetes.Interface
	dyn       dynamic.Interface
	observer  *invariants.KubeObserver
}

func newKubeDeployment(ctx context.Context) (*kubeDeployment, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		return nil, fmt.Errorf("KUBECONFIG is required for GOFI_E2E_MODE=kubernetes (scripts/load/k3d-up.sh prints it)")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	d := &kubeDeployment{cs: cs, dyn: dyn,
		namespace: envOr("GOFI_E2E_NAMESPACE", "gofi"),
		nodeHost:  envOr("GOFI_E2E_NODE_HOST", "127.0.0.1"),
		hostAlias: envOr("GOFI_E2E_HOST_ALIAS", "host.k3d.internal"),
		api:       gofiapi.New(envOr("GOFI_E2E_API", "http://127.0.0.1:30090")),
	}
	d.observer = &invariants.KubeObserver{Clientset: cs, Dynamic: dyn, Namespace: d.namespace, PackageName: os.Getenv("GOFI_E2E_UDS_PACKAGE")}
	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return d, d.api.WaitReady(readyCtx)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (d *kubeDeployment) Name() string         { return "kubernetes" }
func (d *kubeDeployment) API() *gofiapi.Client { return d.api }

// DataAddr uses the flow's NodePort (scenarios give Kubernetes flows a
// NodePort Service in the range the k3d load balancer publishes).
func (d *kubeDeployment) DataAddr(def spec.FlowDefinition) string {
	listen := def.Source.Listening()
	if listen.Service != nil && listen.Service.NodePort != 0 {
		return fmt.Sprintf("%s:%d", d.nodeHost, listen.Service.NodePort)
	}
	return fmt.Sprintf("%s:%d", d.nodeHost, listen.Port)
}

func (d *kubeDeployment) SinkURL(r *traffic.Receiver) string {
	addr := r.Addr()
	port := addr[strings.LastIndex(addr, ":")+1:]
	return "http://" + d.hostAlias + ":" + port
}

// Restart deletes the controller Pod (the Deployment recreates it).
func (d *kubeDeployment) Restart(ctx context.Context) error {
	pods, err := d.cs.CoreV1().Pods(d.namespace).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gofi"})
	if err != nil {
		return err
	}
	for _, pod := range pods.Items {
		if err := d.cs.CoreV1().Pods(d.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}
	readyCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	time.Sleep(2 * time.Second)
	return d.api.WaitReady(readyCtx)
}

// Kill deletes the controller Pod with no grace period.
func (d *kubeDeployment) Kill(ctx context.Context) error {
	pods, err := d.cs.CoreV1().Pods(d.namespace).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gofi"})
	if err != nil {
		return err
	}
	zero := int64(0)
	for _, pod := range pods.Items {
		if err := d.cs.CoreV1().Pods(d.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{GracePeriodSeconds: &zero}); err != nil {
			return err
		}
	}
	readyCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	time.Sleep(2 * time.Second)
	return d.api.WaitReady(readyCtx)
}

// KillRuntime force-deletes the flow's Pod.
func (d *kubeDeployment) KillRuntime(ctx context.Context, flow string) error {
	zero := int64(0)
	pods, err := d.cs.CoreV1().Pods(d.namespace).List(ctx, metav1.ListOptions{LabelSelector: "gofi.io/listener=" + flow})
	if err != nil {
		return err
	}
	for _, pod := range pods.Items {
		if err := d.cs.CoreV1().Pods(d.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{GracePeriodSeconds: &zero}); err != nil {
			return err
		}
	}
	return nil
}

func (d *kubeDeployment) Observe(ctx context.Context) (invariants.World, error) {
	w, err := invariants.ObserveAPI(ctx, d.api, invariants.APIOptions{})
	if err != nil {
		return w, err
	}
	kw, err := d.observer.Observe(ctx)
	if err != nil {
		return w, err
	}
	kw.Ready, kw.APIAnswers, kw.Metrics = w.Ready, w.APIAnswers, w.Metrics
	kw.Observed = w.Observed + "+" + kw.Observed
	return kw, nil
}

func (d *kubeDeployment) ControllerRSS() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := d.api.Metrics(ctx)
	if err != nil {
		return 0, err
	}
	return int64(s.Value("process_resident_memory_bytes", nil)), nil
}

func (d *kubeDeployment) Close() {}

// drainNode cordons and evicts the node running flow's Pod (node drain
// scenario). It shells out to kubectl (GOFI_E2E_KUBECTL, default kubectl).
func (d *kubeDeployment) drainNode(ctx context.Context, node string) error {
	kubectl := envOr("GOFI_E2E_KUBECTL", "kubectl")
	cmd := exec.CommandContext(ctx, kubectl, "drain", node, "--ignore-daemonsets", "--delete-emptydir-data", "--force", "--timeout=120s")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("drain %s: %w\n%s", node, err, out)
	}
	return nil
}

func (d *kubeDeployment) uncordon(ctx context.Context, node string) error {
	kubectl := envOr("GOFI_E2E_KUBECTL", "kubectl")
	out, err := exec.CommandContext(ctx, kubectl, "uncordon", node).CombinedOutput()
	if err != nil {
		return fmt.Errorf("uncordon %s: %w\n%s", node, err, out)
	}
	return nil
}
