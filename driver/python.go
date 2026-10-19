package driver

import (
	"context"
	"fmt"
	"strings"

	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/spec"
)

// PythonBindingsChecker compares the private environment used by a remote
// listener with its currently ready workers. In-process listeners resolve
// bindings on each call and do not need restarting.
type PythonBindingsChecker interface {
	PythonBindingsCurrent(context.Context, spec.FlowDefinition) (bool, error)
}

func (d *Docker) PythonBindingsCurrent(ctx context.Context, def spec.FlowDefinition) (bool, error) {
	expected, err := functionrpc.Environment(ctx, def)
	if err != nil {
		return false, err
	}
	info, err := d.cli.ContainerInspect(ctx, containerName(def.Name))
	if err != nil {
		return false, err
	}
	if !d.owns(info, def.Name) {
		return false, fmt.Errorf("flow runtime is outside this deployment")
	}
	for _, env := range info.Config.Env {
		if value, ok := strings.CutPrefix(env, "GOFI_PYTHON_BINDINGS="); ok {
			return value == expected, nil
		}
	}
	return false, nil
}

func (k *Kube) PythonBindingsCurrent(ctx context.Context, def spec.FlowDefinition) (bool, error) {
	expected, err := functionrpc.Environment(ctx, def)
	if err != nil {
		return false, err
	}
	pod, _, err := k.currentPod(ctx, def.Name)
	if err != nil {
		return false, err
	}
	if pod == nil {
		return false, fmt.Errorf("flow runtime has no live pod")
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
