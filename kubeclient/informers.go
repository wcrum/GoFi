package kubeclient

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/wcrum/gofi/observability"
)

// ManagedLabel marks every Pod and Service the Kubernetes driver creates.
// The typed informers watch only objects carrying it, so a busy namespace
// does not inflate the controller's cache.
const ManagedLabel = "gofi.io/managed-by"

// DefaultResync is the informers' periodic full re-delivery. Informer events
// drive reconciliation; the resync is a safety net for missed events and
// for repairing drift in objects nobody edited through the API server.
const DefaultResync = 5 * time.Minute

// Informers are the shared watch caches: managed Pods and Services (typed),
// plus any namespaced custom resource on demand (dynamic). Reads served from
// here cost no API request.
type Informers struct {
	Namespace string
	typed     informers.SharedInformerFactory
	dynamic   dynamicinformer.DynamicSharedInformerFactory
	pods      corelisters.PodLister
	services  corelisters.ServiceLister

	mu       sync.Mutex
	generic  map[schema.GroupVersionResource]informers.GenericInformer
	stop     chan struct{}
	started  bool
	synced   atomic.Bool
	resyncIn time.Duration
	// lastWatchError and previousWatchError (unix nanoseconds) track watch
	// failures for Healthy.
	lastWatchError, previousWatchError atomic.Int64
}

// PodReader is the part of the Pod cache other components (the traffic tap,
// the observability hub) consume: list managed Pods in GoFi's namespace or
// get one by name, without an API request. It is satisfied by Informers.
type PodReader interface {
	// Pod returns a managed Pod by name; ok is false when the cache has none.
	Pod(name string) (*corev1.Pod, bool)
	// Pods returns the managed Pods matching selector (nil: all of them).
	Pods(selector map[string]string) ([]*corev1.Pod, error)
	// HasSynced reports whether the caches have completed their initial list.
	HasSynced() bool
}

// NewInformers builds (but does not start) informers for namespace.
func NewInformers(cs kubernetes.Interface, dyn dynamic.Interface, namespace string, resync time.Duration) *Informers {
	if resync <= 0 {
		resync = DefaultResync
	}
	managedOnly := func(opts *metav1.ListOptions) { opts.LabelSelector = ManagedLabel + "=true" }
	typed := informers.NewSharedInformerFactoryWithOptions(cs, resync, informers.WithNamespace(namespace), informers.WithTweakListOptions(managedOnly))
	inf := &Informers{Namespace: namespace, typed: typed, generic: map[schema.GroupVersionResource]informers.GenericInformer{}, stop: make(chan struct{}), resyncIn: resync}
	inf.pods = typed.Core().V1().Pods().Lister()
	inf.services = typed.Core().V1().Services().Lister()
	if dyn != nil {
		inf.dynamic = dynamicinformer.NewFilteredDynamicSharedInformerFactory(dyn, resync, namespace, nil)
	}
	return inf
}

// Lister returns the cache for a namespaced custom resource, creating and
// syncing its informer on first use. Call it for every resource before Start
// when possible; later calls start the new informer and wait for its sync.
func (i *Informers) Lister(ctx context.Context, gvr schema.GroupVersionResource) (cache.GenericNamespaceLister, error) {
	if i.dynamic == nil {
		return nil, fmt.Errorf("informers: no dynamic client for %s", gvr)
	}
	i.mu.Lock()
	informer, ok := i.generic[gvr]
	if !ok {
		informer = i.dynamic.ForResource(gvr)
		i.generic[gvr] = informer
	}
	started := i.started
	i.mu.Unlock()
	if started && !ok {
		i.dynamic.Start(i.stop)
		if !cache.WaitForCacheSync(withTimeout(ctx, i.stop), informer.Informer().HasSynced) {
			return nil, fmt.Errorf("informers: %s cache did not sync", gvr)
		}
	}
	return informer.Lister().ByNamespace(i.Namespace), nil
}

// Informer returns the shared informer of a custom resource so callers can
// add event handlers. It must be requested before Start to be synced by it.
func (i *Informers) Informer(gvr schema.GroupVersionResource) (cache.SharedIndexInformer, error) {
	if i.dynamic == nil {
		return nil, fmt.Errorf("informers: no dynamic client for %s", gvr)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	informer, ok := i.generic[gvr]
	if !ok {
		informer = i.dynamic.ForResource(gvr)
		i.generic[gvr] = informer
	}
	return informer.Informer(), nil
}

// PodInformer and ServiceInformer expose the typed informers for handlers.
func (i *Informers) PodInformer() cache.SharedIndexInformer {
	return i.typed.Core().V1().Pods().Informer()
}
func (i *Informers) ServiceInformer() cache.SharedIndexInformer {
	return i.typed.Core().V1().Services().Informer()
}

// Start runs every informer requested so far and blocks until their caches
// are synced or ctx ends. Informers stop when ctx is cancelled.
func (i *Informers) Start(ctx context.Context) error {
	i.mu.Lock()
	if i.started {
		i.mu.Unlock()
		return nil
	}
	i.started = true
	i.mu.Unlock()
	// Register the typed informers before Start so the factory runs them.
	i.PodInformer()
	i.ServiceInformer()
	go func() {
		<-ctx.Done()
		close(i.stop)
	}()
	// Count list/watch failures and remember when a watch last failed, so
	// readiness can report a cache that has been stale for too long.
	i.PodInformer().SetWatchErrorHandler(i.watchFailed)
	i.ServiceInformer().SetWatchErrorHandler(i.watchFailed)
	i.mu.Lock()
	for _, informer := range i.generic {
		_ = informer.Informer().SetWatchErrorHandler(i.watchFailed)
	}
	i.mu.Unlock()
	i.typed.Start(i.stop)
	if i.dynamic != nil {
		i.dynamic.Start(i.stop)
	}
	syncCtx := withTimeout(ctx, i.stop)
	for kind, ok := range i.typed.WaitForCacheSync(syncCtx) {
		if !ok {
			return fmt.Errorf("informers: %s cache did not sync", kind)
		}
	}
	if i.dynamic != nil {
		for gvr, ok := range i.dynamic.WaitForCacheSync(syncCtx) {
			if !ok {
				return fmt.Errorf("informers: %s cache did not sync", gvr)
			}
		}
	}
	i.synced.Store(true)
	slog.Info("kubernetes informers synced", "namespace", i.Namespace, "resync", i.resyncIn)
	return nil
}

// withTimeout bounds a cache sync wait: the shorter of stop and 2 minutes.
func withTimeout(ctx context.Context, stop <-chan struct{}) <-chan struct{} {
	out := make(chan struct{})
	go func() {
		defer close(out)
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-stop:
		case <-timer.C:
		}
	}()
	return out
}

// HasSynced reports whether Start completed its initial cache sync. The
// readiness probe uses it: a controller whose caches are empty would see
// every flow as missing.
func (i *Informers) HasSynced() bool { return i.synced.Load() }

// watchFailed is the informers' list/watch error handler.
func (i *Informers) watchFailed(r *cache.Reflector, err error) {
	observability.WatchError()
	i.previousWatchError.Store(i.lastWatchError.Load())
	i.lastWatchError.Store(time.Now().UnixNano())
	cache.DefaultWatchErrorHandler(context.Background(), r, err)
}

// Healthy reports whether the caches are synced and no watch has been
// failing for longer than the resync period; the readiness probe answers
// 503 otherwise, since the controller would then act on stale state.
func (i *Informers) Healthy() bool {
	if !i.HasSynced() {
		return false
	}
	last := i.lastWatchError.Load()
	if last == 0 {
		return true
	}
	failing := time.Since(time.Unix(0, last))
	// A watch that failed recovers silently (the reflector relists); treat a
	// failure as ongoing for the resync period only when it recurs.
	return failing > i.resyncIn || !i.watchRecurring()
}

// watchRecurring reports whether watch failures keep happening (more than one
// within the resync period).
func (i *Informers) watchRecurring() bool {
	now := time.Now().UnixNano()
	last := i.lastWatchError.Load()
	previous := i.previousWatchError.Load()
	return last != 0 && previous != 0 && now-previous < int64(i.resyncIn)
}

// Pod returns a managed Pod from the cache.
func (i *Informers) Pod(name string) (*corev1.Pod, bool) {
	pod, err := i.pods.Pods(i.Namespace).Get(name)
	if err != nil {
		return nil, false
	}
	return pod, true
}

// Pods lists managed Pods matching selector (nil: all).
func (i *Informers) Pods(selector map[string]string) ([]*corev1.Pod, error) {
	return i.pods.Pods(i.Namespace).List(selectorOf(selector))
}

// Service returns a managed Service from the cache.
func (i *Informers) Service(name string) (*corev1.Service, bool) {
	svc, err := i.services.Services(i.Namespace).Get(name)
	if err != nil {
		return nil, false
	}
	return svc, true
}

// Services lists managed Services matching selector (nil: all).
func (i *Informers) Services(selector map[string]string) ([]*corev1.Service, error) {
	return i.services.Services(i.Namespace).List(selectorOf(selector))
}

// PodLister exposes the underlying lister for the observability hub.
func (i *Informers) PodLister() corelisters.PodNamespaceLister { return i.pods.Pods(i.Namespace) }
