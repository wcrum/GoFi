// Package deploymode detects which deployment environment the gofi
// controller process is running in: a plain binary, Docker Compose, or
// Kubernetes. This is orthogonal to the "process role" concept in main.go
// (controller vs. listener) — deploymode describes the surrounding
// infrastructure, not what the process itself is doing.
package deploymode

import (
	"os"
	"strings"
)

// Mode identifies the deployment environment the controller is running in.
type Mode string

const (
	// ModeBinary is the default: a plain, unwrapped OS process.
	ModeBinary Mode = "binary"
	// ModeCompose is a container managed by Docker Compose.
	ModeCompose Mode = "compose"
	// ModeKubernetes is a pod running inside a Kubernetes cluster.
	ModeKubernetes Mode = "kubernetes"
	// ModeDocker is a generic container fallback: we can tell we're inside
	// a container, but not specifically Compose or Kubernetes.
	ModeDocker Mode = "docker"
)

// dockerEnvExists reports whether /.dockerenv is present, which the Docker
// (and Docker-compatible) container runtime creates inside every container.
// Stubbed as a var so tests can override it without touching the filesystem.
var dockerEnvExists = func() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// cgroupIndicatesContainer reports whether /proc/1/cgroup mentions "docker"
// or "containerd", which is the case when PID 1 is running inside a
// container managed by either runtime.
// Stubbed as a var so tests can override it without touching the filesystem.
var cgroupIndicatesContainer = func() bool {
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd")
}

// Detect determines the current deployment mode using the following
// precedence, from most to least authoritative:
//
//  1. Explicit override: the GOFI_DEPLOY_MODE env var, if set to a valid
//     value ("binary", "compose", "kubernetes"), wins unconditionally.
//  2. Kubernetes auto-detect: the KUBERNETES_SERVICE_HOST env var is
//     injected by the kubelet into every pod, so its presence means we're
//     running in Kubernetes.
//  3. Compose auto-detect: COMPOSE_PROJECT_NAME or COMPOSE_FILE are set by
//     `docker compose` in the container environment it creates.
//  4. Generic container fallback: /.dockerenv exists, or /proc/1/cgroup
//     mentions "docker"/"containerd" — we're in *some* container, but
//     can't tell it's specifically Compose.
//  5. Default: ModeBinary, a plain unwrapped process.
func Detect() Mode {
	// 1. Explicit override.
	if v := os.Getenv("GOFI_DEPLOY_MODE"); v != "" {
		switch Mode(v) {
		case ModeBinary, ModeCompose, ModeKubernetes:
			return Mode(v)
		}
	}

	// 2. Kubernetes auto-detect.
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return ModeKubernetes
	}

	// 3. Compose auto-detect.
	if os.Getenv("COMPOSE_PROJECT_NAME") != "" || os.Getenv("COMPOSE_FILE") != "" {
		return ModeCompose
	}

	// 4. Generic container fallback.
	if dockerEnvExists() || cgroupIndicatesContainer() {
		return ModeDocker
	}

	// 5. Default.
	return ModeBinary
}
