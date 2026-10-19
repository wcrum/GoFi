package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/expressions"
	"github.com/wcrum/gofi/kubeclient"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sandbox"
	"github.com/wcrum/gofi/spec"
)

// kubeControlPlane is the Kubernetes-mode control plane around the controller:
// shared informer caches (Pods, Services, Flow, CelExpression, Sandbox),
// event-driven reconciliation, leader election, and the readiness and
// write-gate HTTP middleware. It is nil outside Kubernetes, and every method
// is nil-safe so main.go needs no mode checks.
type kubeControlPlane struct {
	namespace string
	kube      *driver.Kube
	informers *kubeclient.Informers
	election  *kubeclient.Election
	electing  bool
	// storeLoaded flips once the controller loaded desired state; readiness
	// waits for it.
	storeLoaded atomic.Bool
}

// kubeRuntime is the process-wide instance, read by the store constructors in
// the other runtime files (nil outside Kubernetes).
var kubeRuntime *kubeControlPlane

// informerResync is the informers' periodic re-delivery and the controller's
// full-pass period on Kubernetes: events drive everything in between.
const informerResync = 5 * time.Minute

// startKubernetes builds and syncs the informer caches for the Kubernetes
// driver. It returns nil for other drivers. Syncing happens before desired
// state loads, so the first reconcile never sees an empty cache.
func startKubernetes(ctx context.Context, d driver.Driver, ctrl *controller.Controller) *kubeControlPlane {
	kube, ok := d.(*driver.Kube)
	if !ok {
		return nil
	}
	clients, err := kubeclient.Get()
	if err != nil {
		slog.Error("kubernetes clients unavailable", "error", err)
		os.Exit(1)
	}
	rt := &kubeControlPlane{namespace: kube.Namespace(), kube: kube}
	rt.informers = kubeclient.NewInformers(clients.WatchClientset, clients.WatchDynamic, rt.namespace, informerResync)
	// Register every cache before Start so one sync covers them all.
	for _, gvr := range []schema.GroupVersionResource{desired.FlowGVR, expressions.GVR, sandbox.SandboxGVR} {
		if _, err := rt.informers.Informer(gvr); err != nil {
			slog.Error("informer registration failed", "resource", gvr.String(), "error", err)
			os.Exit(1)
		}
	}
	kube.UseInformers(rt.informers)
	kube.SetFlowOwner(rt.flowOwner)
	rt.addHandlers(ctrl)
	if err := rt.informers.Start(ctx); err != nil {
		slog.Error("informer caches did not sync; falling back to API reads", "error", err)
		// The driver keeps working through the API server (slower, but correct).
		rt.informers = nil
		kube.UseInformers(nil)
		return rt
	}
	ctrl.SetResyncInterval(informerResync)
	return rt
}

// lister returns the cache of a custom resource, or nil outside Kubernetes
// (stores then list through the API).
func (rt *kubeControlPlane) lister(gvr schema.GroupVersionResource) cache.GenericNamespaceLister {
	if rt == nil || rt.informers == nil {
		return nil
	}
	lister, err := rt.informers.Lister(context.Background(), gvr)
	if err != nil {
		slog.Warn("informer cache unavailable; reading through the API", "resource", gvr.String(), "error", err)
		return nil
	}
	return lister
}

// flowStore is the Flow store backed by the informer cache.
func (rt *kubeControlPlane) flowStore(namespace string) (desired.Store, error) {
	store, err := desired.NewKubernetes(namespace)
	if err != nil {
		return nil, err
	}
	store.Lister = rt.lister(desired.FlowGVR)
	return &loadedStore{Kubernetes: store, rt: rt}, nil
}

// loadedStore marks the runtime ready once desired state has been loaded.
type loadedStore struct {
	*desired.Kubernetes
	rt *kubeControlPlane
}

func (s *loadedStore) Load(ctx context.Context) ([]spec.FlowDefinition, error) {
	defs, err := s.Kubernetes.Load(ctx)
	if err == nil && s.rt != nil {
		s.rt.storeLoaded.Store(true)
	}
	return defs, err
}

// flowOwner is the ownerReference of a flow's objects: its Flow resource.
func (rt *kubeControlPlane) flowOwner(name string) *metav1.OwnerReference {
	lister := rt.lister(desired.FlowGVR)
	if lister == nil {
		return nil
	}
	obj, err := lister.Get(name)
	if err != nil {
		return nil
	}
	flow, ok := obj.(*unstructured.Unstructured)
	if !ok || flow.GetUID() == "" {
		return nil
	}
	return &metav1.OwnerReference{APIVersion: desired.APIVersion, Kind: desired.KindFlow, Name: flow.GetName(), UID: flow.GetUID()}
}

// addHandlers connects informer events to the controller: a Pod or Service
// event enqueues its flow; a Flow, CelExpression, or Sandbox event triggers
// a full pass (desired state changed).
func (rt *kubeControlPlane) addHandlers(ctrl *controller.Controller) {
	flowOf := func(obj any) string {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tombstone.Obj
		}
		meta, err := metaAccessor(obj)
		if err != nil {
			return ""
		}
		return meta.GetLabels()["gofi.io/listener"]
	}
	owned := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { ctrl.Enqueue(flowOf(obj)) },
		UpdateFunc: func(_, obj any) { ctrl.Enqueue(flowOf(obj)) },
		DeleteFunc: func(obj any) { ctrl.Enqueue(flowOf(obj)) },
	}
	_, _ = rt.informers.PodInformer().AddEventHandler(owned)
	_, _ = rt.informers.ServiceInformer().AddEventHandler(owned)
	desiredChanged := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { ctrl.NotifyRuntimeChange() },
		UpdateFunc: func(_, _ any) { ctrl.NotifyRuntimeChange() },
		DeleteFunc: func(any) { ctrl.NotifyRuntimeChange() },
	}
	for _, gvr := range []schema.GroupVersionResource{desired.FlowGVR, expressions.GVR, sandbox.SandboxGVR} {
		if informer, err := rt.informers.Informer(gvr); err == nil {
			_, _ = informer.AddEventHandler(desiredChanged)
		}
	}
}

func metaAccessor(obj any) (metav1.Object, error) {
	if accessor, ok := obj.(metav1.Object); ok {
		return accessor, nil
	}
	return nil, fmt.Errorf("object has no metadata")
}

// elect starts leader election when the chart enabled it. Until this
// replica leads, the controller and the Python supervisor are passive (they
// observe, serve reads, and act on nothing). Losing the lease exits the
// process: another replica may already be reconciling, and a restart is the
// clean way back to a passive follower.
func (rt *kubeControlPlane) elect(ctx context.Context, ctrl *controller.Controller, manager *sandbox.Supervisor) {
	if rt == nil {
		return
	}
	cfg := kubeclient.ElectionFromEnv()
	identity := cfg.Identity
	if !cfg.Enabled {
		observability.SetLeader(identity, true)
		return
	}
	clients, err := kubeclient.Get()
	if err != nil {
		slog.Error("leader election unavailable", "error", err)
		os.Exit(1)
	}
	ctrl.SetPassive(true)
	if manager != nil {
		manager.SetPassive(true)
	}
	rt.electing = true
	rt.election, err = kubeclient.RunElection(ctx, clients.Clientset, rt.namespace, cfg, kubeclient.LeaderCallbacks{
		OnStartedLeading: func(context.Context) {
			if manager != nil {
				manager.SetPassive(false)
			}
			ctrl.SetPassive(false)
			ctrl.NotifyRuntimeChange()
		},
		OnStoppedLeading: func() {
			if ctx.Err() != nil {
				return // normal shutdown
			}
			slog.Error("leader election: lease lost; exiting so a clean standby takes over")
			os.Exit(0)
		},
	})
	if err != nil {
		slog.Error("leader election failed to start", "error", err)
		os.Exit(1)
	}
	slog.Info("leader election enabled", "lease", cfg.LeaseName, "identity", identity)
}

// isLeader reports whether this replica reconciles (always, without election).
func (rt *kubeControlPlane) isLeader() bool {
	return rt == nil || !rt.electing || (rt.election != nil && rt.election.IsLeader())
}

// ready reports readiness: informer caches synced and healthy, desired state
// loaded. Standby replicas are ready too (they serve reads).
func (rt *kubeControlPlane) ready() (bool, string) {
	if rt == nil {
		return true, ""
	}
	if rt.informers != nil && !rt.informers.Healthy() {
		if !rt.informers.HasSynced() {
			return false, "informer caches syncing"
		}
		return false, "informer watch failing"
	}
	if !rt.storeLoaded.Load() {
		return false, "desired state not loaded"
	}
	return true, ""
}

// wrap adds the readiness probe and the standby write gate in front of the
// API. /healthz stays the plain liveness answer the API serves.
func (rt *kubeControlPlane) wrap(next http.Handler) http.Handler {
	if rt == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			if ok, reason := rt.ready(); !ok {
				http.Error(w, "not ready: "+reason, http.StatusServiceUnavailable)
				return
			}
			// The API's handler then applies the controller's own checks
			// (first reconcile pass completed, driver answering).
			next.ServeHTTP(w, r)
			return
		}
		if rt.electing && !rt.isLeader() && mutatingRPC(r) {
			// Desired state lives in the Kubernetes API, so a standby could
			// persist it; refusing keeps one writer and one validator (admission
			// runs on the leader, with its Python tooling worker).
			w.Header().Set("Retry-After", "5")
			if holder := rt.election.Holder(); holder != "" {
				w.Header().Set("X-GoFi-Leader", holder)
			}
			http.Error(w, "this replica is a standby; retry against the leader", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// readOnlyRPCPrefixes are the ConnectRPC procedures a standby may serve.
// Everything else on an RPC path changes state (or runs code) and belongs to
// the leader.
var readOnlyRPCPrefixes = []string{"Get", "List", "Describe", "Watch", "Stream", "Analyze", "Plan", "Preview", "Resolve", "Check", "Evaluate", "Explain", "Render", "Validate", "Simulate", "Replay", "Fetch"}

// mutatingRPC classifies a request: ConnectRPC procedures are POST
// /<package>.<Service>/<Method>; the method name says what it does.
func mutatingRPC(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return false
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	service, method, ok := strings.Cut(path, "/")
	if !ok || !strings.Contains(service, ".") {
		return false
	}
	for _, prefix := range readOnlyRPCPrefixes {
		if strings.HasPrefix(method, prefix) {
			return false
		}
	}
	return true
}
