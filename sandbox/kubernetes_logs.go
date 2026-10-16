package sandbox

import (
	"context"
	"fmt"

	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Follow streams a worker Pod's container log until the Pod stops or is
// deleted, or ctx ends. Kubernetes combines stdout and stderr into one
// stream; worker JSON lines still carry their original stream name.
func (k *Kubernetes) Follow(ctx context.Context, worker string, tail int, emit func(RawLine) error) error {
	pod, err := k.Client.CoreV1().Pods(k.Namespace).Get(ctx, worker, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return ErrNoWorker
	}
	if err != nil {
		return err
	}
	if pod.Labels["gofi.managed-by"] != "gofi" || pod.Labels["app.kubernetes.io/name"] != "gofi-python" {
		return fmt.Errorf("refusing to read logs of a Pod GoFi does not manage as a worker")
	}
	lines := int64(tail)
	stream, err := k.Client.CoreV1().Pods(k.Namespace).GetLogs(worker, &core.PodLogOptions{Container: "worker", Follow: true, Timestamps: true, TailLines: &lines}).Stream(ctx)
	if apierrors.IsNotFound(err) || apierrors.IsBadRequest(err) {
		// Deleted, or the container has not started yet ("waiting to start").
		return ErrNoWorker
	}
	if err != nil {
		return err
	}
	defer stream.Close()
	if err = scanLines(stream, "stdout", true, emit); err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}
