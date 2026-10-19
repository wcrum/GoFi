package driver

import (
	"context"
	"log/slog"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/wcrum/gofi/listener"
)

// DrainTimeoutEnv configures how long a stopping flow runtime may take to
// finish in-flight frames and flush its sinks (a Go duration, default 20s).
// The controller passes it on to Docker containers and Kubernetes Pods.
const DrainTimeoutEnv = "GOFI_DRAIN_TIMEOUT"

// DrainTimeout is the configured drain deadline.
func DrainTimeout() time.Duration {
	if raw := os.Getenv(DrainTimeoutEnv); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
			return d
		}
		slog.Warn("invalid drain timeout; using the default", "env", DrainTimeoutEnv, "value", raw)
	}
	return listener.DefaultDrainTimeout
}

// stopGrace is how long a platform should wait between asking a runtime to
// stop (SIGTERM) and killing it: the drain deadline, the Kubernetes preStop
// delay, and a margin for process exit.
func stopGrace() time.Duration {
	return DrainTimeout() + kubePreStopDelay + 7*time.Second
}

// Drainer is a driver that can stop every runtime it hosts gracefully when
// the controller itself shuts down (only the in-process driver: Docker and
// Kubernetes runtimes outlive the controller).
type Drainer interface {
	DrainAll(ctx context.Context) error
}

// kubePreStopDelay keeps a terminating listener Pod serving while Kubernetes
// removes it from Service endpoints, so senders stop picking it before it
// stops accepting.
const kubePreStopDelay = 3 * time.Second

// listenerLifecycle delays SIGTERM with a preStop sleep. The image has no
// shell, so this is the kubelet's own sleep action, not an exec.
func listenerLifecycle() *corev1.Lifecycle {
	return &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: int64(kubePreStopDelay / time.Second)}}}
}

// dockerStopTimeout is the container stop timeout in seconds: the drain
// deadline plus a margin (Docker has no preStop delay).
func dockerStopTimeout() *int {
	seconds := int((DrainTimeout() + 5*time.Second) / time.Second)
	return &seconds
}
