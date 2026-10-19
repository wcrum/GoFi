package driver

import (
	"context"
	"sort"

	"github.com/wcrum/gofi/kubeclient"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// objectReader serves the driver's reads of its own Pods and Services. In
// production it is the informer cache (no API requests); without informers
// (tests, or a cache that failed to start) it is the API server.
type objectReader interface {
	pod(ctx context.Context, name string) (*corev1.Pod, error)
	pods(ctx context.Context, selector map[string]string) ([]*corev1.Pod, error)
	service(ctx context.Context, name string) (*corev1.Service, error)
	services(ctx context.Context, selector map[string]string) ([]*corev1.Service, error)
	// cached reports whether reads are free (served from a watch cache), so
	// callers can afford to list where an API list would be wasteful.
	cached() bool
}

// apiReader reads through the API server.
type apiReader struct {
	cs        kubernetes.Interface
	namespace string
}

func (r apiReader) cached() bool { return false }

func (r apiReader) pod(ctx context.Context, name string) (*corev1.Pod, error) {
	ctx, cancel := apiCall(ctx)
	defer cancel()
	return r.cs.CoreV1().Pods(r.namespace).Get(ctx, name, metav1.GetOptions{})
}

func (r apiReader) pods(ctx context.Context, selector map[string]string) ([]*corev1.Pod, error) {
	ctx, cancel := apiCall(ctx)
	defer cancel()
	list, err := r.cs.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(selector).String()})
	if err != nil {
		return nil, err
	}
	out := make([]*corev1.Pod, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, &list.Items[i])
	}
	return out, nil
}

func (r apiReader) service(ctx context.Context, name string) (*corev1.Service, error) {
	ctx, cancel := apiCall(ctx)
	defer cancel()
	return r.cs.CoreV1().Services(r.namespace).Get(ctx, name, metav1.GetOptions{})
}

func (r apiReader) services(ctx context.Context, selector map[string]string) ([]*corev1.Service, error) {
	ctx, cancel := apiCall(ctx)
	defer cancel()
	list, err := r.cs.CoreV1().Services(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(selector).String()})
	if err != nil {
		return nil, err
	}
	out := make([]*corev1.Service, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, &list.Items[i])
	}
	return out, nil
}

// cacheReader reads the informer caches. Objects it returns are shared with
// the cache and must be deep-copied before mutation.
type cacheReader struct{ informers *kubeclient.Informers }

func (r cacheReader) cached() bool { return true }

func (r cacheReader) pod(_ context.Context, name string) (*corev1.Pod, error) {
	pod, ok := r.informers.Pod(name)
	if !ok {
		return nil, apierrors.NewNotFound(corev1.Resource("pods"), name)
	}
	return pod, nil
}

func (r cacheReader) pods(_ context.Context, selector map[string]string) ([]*corev1.Pod, error) {
	pods, err := r.informers.Pods(selector)
	if err != nil {
		return nil, err
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	return pods, nil
}

func (r cacheReader) service(_ context.Context, name string) (*corev1.Service, error) {
	svc, ok := r.informers.Service(name)
	if !ok {
		return nil, apierrors.NewNotFound(corev1.Resource("services"), name)
	}
	return svc, nil
}

func (r cacheReader) services(_ context.Context, selector map[string]string) ([]*corev1.Service, error) {
	svcs, err := r.informers.Services(selector)
	if err != nil {
		return nil, err
	}
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].Name < svcs[j].Name })
	return svcs, nil
}

// apiCall bounds one API request so a stalled API server cannot hold a
// reconcile indefinitely.
func apiCall(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, kubeclient.CallTimeout)
}
