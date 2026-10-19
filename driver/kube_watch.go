package driver

import (
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
)

// WatchPods delivers every change of a managed listener Pod from the shared
// Pod informer: onChange runs with the Pod as last seen and deleted true when
// it went away. The observability hub detects this method structurally
// (api/podsource.go) and uses it to re-resolve a flow's tap target the moment
// its Pod changes, at no API cost; Tap and RuntimeStatus read the same cache.
// Existing Pods are delivered once on registration. The returned stop removes
// the handler. Without informers (they failed to start) nothing is delivered;
// the hub's periodic resync still re-resolves targets through the API.
func (k *Kube) WatchPods(onChange func(pod *corev1.Pod, deleted bool)) (stop func()) {
	if k.informers == nil {
		slog.Warn("kube driver: WatchPods without informers; Pod changes are not pushed")
		return func() {}
	}
	podOf := func(obj any) *corev1.Pod {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tombstone.Obj
		}
		pod, _ := obj.(*corev1.Pod)
		return pod
	}
	informer := k.informers.PodInformer()
	registration, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if pod := podOf(obj); pod != nil {
				onChange(pod, false)
			}
		},
		UpdateFunc: func(_, obj any) {
			if pod := podOf(obj); pod != nil {
				onChange(pod, false)
			}
		},
		DeleteFunc: func(obj any) {
			if pod := podOf(obj); pod != nil {
				onChange(pod, true)
			}
		},
	})
	if err != nil {
		slog.Warn("kube driver: WatchPods handler not registered", "error", err)
		return func() {}
	}
	return func() { _ = informer.RemoveEventHandler(registration) }
}
