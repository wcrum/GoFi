package spec

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// The NodePort range flow Services may request: the Kubernetes default
// (kube-apiserver --service-node-port-range), or GOFI_NODE_PORT_RANGE
// ("min-max") for clusters configured with a different range.
var MinNodePort, MaxNodePort = nodePortRange(os.Getenv("GOFI_NODE_PORT_RANGE"))

func nodePortRange(value string) (int, int) {
	lo, hi, ok := strings.Cut(value, "-")
	min, errMin := strconv.Atoi(strings.TrimSpace(lo))
	max, errMax := strconv.Atoi(strings.TrimSpace(hi))
	if !ok || errMin != nil || errMax != nil || min < 1 || max > 65535 || min > max {
		return 30000, 32767
	}
	return min, max
}

// Validate checks portable endpoint settings. Port zero is allowed for local
// ephemeral flows; management APIs must require a persistent nonzero port.

func (s ListenSource) Validate() error {
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("source.listen.port must be between 0 and 65535")
	}
	if s.Network != "" && s.Network != "tcp" && s.Network != "udp" && s.Network != NetworkQUIC {
		return fmt.Errorf("source.listen.network must be tcp, udp, or quic")
	}
	if err := s.validateQUIC(); err != nil {
		return err
	}
	if s.Service == nil {
		return nil
	}
	if s.Port == 0 {
		return fmt.Errorf("source.listen.port must be nonzero for a Service")
	}
	switch s.Service.Type {
	case "ClusterIP", "NodePort", "LoadBalancer", "Gateway":
	default:
		return fmt.Errorf("source.listen.service.type must be ClusterIP, NodePort, LoadBalancer, or Gateway")
	}
	if s.Service.Port < 0 || s.Service.Port > 65535 {
		return fmt.Errorf("source.listen.service.port must be between 1 and 65535 when specified")
	}
	if s.Service.NodePort < 0 || s.Service.NodePort > 65535 {
		return fmt.Errorf("source.listen.service.nodePort must be between 1 and 65535 when specified")
	}
	if s.Service.NodePort != 0 && (s.Service.NodePort < MinNodePort || s.Service.NodePort > MaxNodePort) {
		return fmt.Errorf("source.listen.service.nodePort must be between %d and %d (the Kubernetes NodePort range), or 0 to allocate one", MinNodePort, MaxNodePort)
	}
	if (s.Service.Type == "ClusterIP" || s.Service.Type == "Gateway") && s.Service.NodePort != 0 {
		return fmt.Errorf("source.listen.service.nodePort requires type NodePort or LoadBalancer")
	}
	switch s.Service.ExternalTrafficPolicy {
	case "", "Cluster":
	case "Local":
		if s.Service.Type != "NodePort" && s.Service.Type != "LoadBalancer" {
			return fmt.Errorf("source.listen.service.externalTrafficPolicy Local requires NodePort or LoadBalancer")
		}
	default:
		return fmt.Errorf("source.listen.service.externalTrafficPolicy must be Cluster or Local")
	}
	for key := range s.Service.Annotations {
		if errs := validation.IsQualifiedName(key); len(errs) > 0 {
			return fmt.Errorf("source.listen.service.annotations %q: %s", key, strings.Join(errs, "; "))
		}
	}
	if lb := s.Service.LoadBalancer; lb != nil {
		if s.Service.Type != "LoadBalancer" {
			return fmt.Errorf("source.listen.service.loadBalancer requires type LoadBalancer")
		}
		if lb.IP != "" && net.ParseIP(lb.IP) == nil {
			return fmt.Errorf("source.listen.service.loadBalancer.ip must be an IP address")
		}
	}
	for i, peer := range s.Service.AllowFrom {
		if peer.Namespace == "" || len(validation.IsDNS1123Label(peer.Namespace)) != 0 {
			return fmt.Errorf("source.listen.service.allowFrom[%d].namespace must be a namespace", i)
		}
		for key, value := range peer.Selector {
			if len(validation.IsQualifiedName(key)) != 0 || len(validation.IsValidLabelValue(value)) != 0 {
				return fmt.Errorf("source.listen.service.allowFrom[%d].selector contains an invalid label", i)
			}
		}
	}
	if gw := s.Service.Gateway; s.Service.Type == "Gateway" {
		if gw == nil || gw.Namespace == "" || gw.Name == "" {
			return fmt.Errorf("source.listen.service.gateway needs the Gateway's namespace and name")
		}
		if gw.Port < 0 || gw.Port > 65535 {
			return fmt.Errorf("source.listen.service.gateway.port must be between 1 and 65535 when specified")
		}
	} else if gw != nil {
		return fmt.Errorf("source.listen.service.gateway requires type Gateway")
	}
	return nil
}
