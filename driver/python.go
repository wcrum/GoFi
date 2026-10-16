package driver

import (
	"context"
	"fmt"
	"strings"

	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/spec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PythonBindingsChecker compares the private environment used by a remote
// listener with its currently ready workers. In-process listeners resolve
// bindings on each call and do not need restarting.
type PythonBindingsChecker interface {
	PythonBindingsCurrent(context.Context, spec.ListenerDefinition) (bool, error)
}

func (d *Docker) PythonBindingsCurrent(ctx context.Context, def spec.ListenerDefinition) (bool, error) {
	expected, err := functionrpc.Environment(ctx, def)
	if err != nil {
		return false, err
	}
	info, err := d.cli.ContainerInspect(ctx, containerName(def.Name))
	if err != nil {
		return false, err
	}
	if !d.owns(info, def.Name) {
		return false, fmt.Errorf("listener runtime is outside this deployment")
	}
	for _, env := range info.Config.Env {
		if value, ok := strings.CutPrefix(env, "GOFI_PYTHON_BINDINGS="); ok {
			return value == expected, nil
		}
	}
	return false, nil
}

func (k *Kube) PythonBindingsCurrent(ctx context.Context, def spec.ListenerDefinition) (bool, error) {
	expected, err := functionrpc.Environment(ctx, def)
	if err != nil {
		return false, err
	}
	pod, err := k.cs.CoreV1().Pods(k.namespace).Get(ctx, podName(def.Name), metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if pod.Labels[kubeLabelManaged] != "true" || pod.Labels[kubeLabelListener] != def.Name {
		return false, fmt.Errorf("listener runtime is outside this deployment")
	}
	for _, container := range pod.Spec.Containers {
		if container.Name != "gofi-listener" {
			continue
		}
		for _, env := range container.Env {
			if env.Name == "GOFI_PYTHON_BINDINGS" {
				return env.ValueFrom == nil && env.Value == expected, nil
			}
		}
	}
	return false, nil
}
