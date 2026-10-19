package invariants

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

	"github.com/wcrum/gofi/spec"
)

// DockerObserver sees the containers the Docker driver and the Python
// sandbox manage on one daemon.
type DockerObserver struct {
	Client *client.Client
	// Network is the deployment's Docker network (GOFI_DOCKER_NETWORK);
	// empty observes every managed container on the daemon.
	Network string
}

// NewDockerObserver connects through DOCKER_HOST (or the default socket).
func NewDockerObserver(network string) (*DockerObserver, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &DockerObserver{Client: cli, Network: network}, nil
}

// Observe lists flow runtime containers (label gofi.managed-by=true) in any
// state (so exited leftovers count as unhealthy runtimes, and duplicates are
// visible), Python worker containers (gofi.managed-by=gofi + gofi.sandbox),
// and the bindings each runtime received. Desired must be supplied by the
// caller (file or API observer) to name orphans.
func (o *DockerObserver) Observe(ctx context.Context, desired []spec.FlowDefinition, libraries []string) (World, error) {
	w := World{Observed: "docker:" + o.Network, Desired: desired, Libraries: libraries, Bindings: map[string]map[string]string{}}
	want := map[string]bool{}
	for _, def := range desired {
		want[def.Name] = true
	}
	libs := map[string]bool{}
	for _, lib := range libraries {
		libs[lib] = true
	}
	args := filters.NewArgs(filters.Arg("label", "gofi.managed-by"))
	list, err := o.Client.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return w, fmt.Errorf("docker list: %w", err)
	}
	for _, c := range list {
		name := strings.TrimPrefix(firstName(c.Names), "/")
		managed := c.Labels["gofi.managed-by"]
		if o.Network != "" {
			if c.NetworkSettings == nil || c.NetworkSettings.Networks[o.Network] == nil {
				continue
			}
		}
		switch {
		case managed == "true" && c.Labels["gofi.listener"] != "":
			flow := c.Labels["gofi.listener"]
			rt := Runtime{Flow: flow, ID: name, Status: c.State}
			info, err := o.Client.ContainerInspect(ctx, c.ID)
			if err == nil && info.Config != nil {
				for _, env := range info.Config.Env {
					if raw, ok := strings.CutPrefix(env, spec.FlowSpecEnv+"="); ok {
						var def spec.FlowDefinition
						if json.Unmarshal([]byte(raw), &def) == nil {
							rt.Def = &def
						}
					}
					if raw, ok := strings.CutPrefix(env, "GOFI_PYTHON_BINDINGS="); ok {
						w.Bindings[flow] = parseBindings(raw)
					}
				}
			}
			w.Runtimes = append(w.Runtimes, rt)
		case managed == "gofi" && c.Labels["gofi.sandbox"] != "":
			lib := c.Labels["gofi.sandbox"]
			if len(libraries) > 0 && !libs[lib] && c.Labels["gofi.role"] != "sandbox-control" {
				w.Orphans = append(w.Orphans, "container/"+name)
			}
		}
	}
	return w, nil
}

func parseBindings(raw string) map[string]string {
	var bindings map[string]struct {
		Token string `json:"token"`
	}
	if json.Unmarshal([]byte(raw), &bindings) != nil {
		return nil
	}
	out := map[string]string{}
	for lib, b := range bindings {
		out[lib] = b.Token
	}
	return out
}

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
