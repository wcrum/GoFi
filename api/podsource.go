package api

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	"github.com/wcrum/gofi/driver"
)

// PodSource is the Kubernetes driver's informer-backed view of listener Pods.
// The streaming hubs use it, when the driver offers one, to re-resolve a
// flow's runtime the moment its Pod changes instead of polling the API
// server. Get is what driver.Tap should read from so a lookup costs no API
// call; Watch delivers add, update, and delete events.
type PodSource interface {
	Get(name string) (*corev1.Pod, error)
	Watch(func(event PodEvent)) (stop func())
}

// PodEvent is one informer event. Pod is the object as last seen.
type PodEvent struct {
	Pod     *corev1.Pod
	Deleted bool
}

// podWatcher is the structural contract the hubs detect on a driver (so the
// driver package need not import this one): the Kubernetes driver implements
// WatchPods once it owns a Pod informer. Until then, the hubs re-resolve
// targets on stream failure and every GOFI_HUB_RESYNC.
type podWatcher interface {
	WatchPods(onChange func(pod *corev1.Pod, deleted bool)) (stop func())
}

// podLabelFlow is the label the Kubernetes driver stamps on listener Pods
// with the flow's name.
const podLabelFlow = "gofi.io/listener"

// watchPods forwards Pod changes to the hubs as flow kicks until ctx ends.
// It returns false when the driver offers no watcher.
func watchPods(ctx context.Context, drv driver.Driver, kick func(flow string)) bool {
	watcher, ok := drv.(podWatcher)
	if !ok {
		return false
	}
	stop := watcher.WatchPods(func(pod *corev1.Pod, _ bool) {
		if pod == nil {
			return
		}
		if flow := pod.Labels[podLabelFlow]; flow != "" {
			kick(flow)
		}
	})
	if stop != nil {
		go func() {
			<-ctx.Done()
			stop()
		}()
	}
	return true
}
