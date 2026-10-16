package spec

import "fmt"

// Validate checks portable endpoint settings. Port zero is allowed for local
// ephemeral listeners; management APIs must require a persistent nonzero port.
func (s Source) Validate() error {
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("source.port must be between 0 and 65535")
	}
	if s.Network != "" && s.Network != "tcp" && s.Network != "udp" {
		return fmt.Errorf("source.network must be tcp or udp")
	}
	if s.Service == nil {
		return nil
	}
	if s.Port == 0 {
		return fmt.Errorf("source.port must be nonzero for a Service")
	}
	if s.Service.Type != "ClusterIP" && s.Service.Type != "NodePort" {
		return fmt.Errorf("source.service.type must be ClusterIP or NodePort")
	}
	if s.Service.Port < 0 || s.Service.Port > 65535 {
		return fmt.Errorf("source.service.port must be between 1 and 65535 when specified")
	}
	if s.Service.NodePort < 0 || s.Service.NodePort > 65535 {
		return fmt.Errorf("source.service.nodePort must be between 1 and 65535 when specified")
	}
	if s.Service.Type != "NodePort" && s.Service.NodePort != 0 {
		return fmt.Errorf("source.service.nodePort requires type NodePort")
	}
	return nil
}
