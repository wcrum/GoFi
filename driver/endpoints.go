package driver

import (
	"context"
	"github.com/wcrum/gofi/spec"
)

// Endpoint is a routable service or container discovered in the current
// runtime. URLs use service/container DNS instead of destination labels.
type Endpoint struct {
	Labels            map[string]string `json:"labels,omitempty"`
	Ready             bool              `json:"ready,omitempty"`
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	Kind              string            `json:"kind"`
	Type              string            `json:"type,omitempty"`
	Host              string            `json:"host"`
	ClusterIP         string            `json:"clusterIP,omitempty"`
	ExternalAddresses []string          `json:"externalAddresses,omitempty"`
	Ports             []EndpointPort    `json:"ports"`
}

type EndpointPort struct {
	Name     string `json:"name,omitempty"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
	NodePort int32  `json:"nodePort,omitempty"`
	URL      string `json:"url"`
}

// EndpointDiscoverer is optional so local binaries and third-party drivers
// need no discovery integration.
type EndpointDiscoverer interface {
	Endpoints(context.Context) ([]Endpoint, error)
}

// TargetResolver previews and resolves current Pod membership for a selector.
type TargetResolver interface {
	ResolveTargets(context.Context, spec.KubernetesTarget, string) ([]Endpoint, error)
}
