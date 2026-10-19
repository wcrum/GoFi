package observability

import "github.com/prometheus/client_golang/prometheus"

// Register adds collectors from other packages (for example client-go request
// metrics) to the registry /metrics serves. A collector that is already
// registered is left in place; any other registration error is returned.
func Register(collectors ...prometheus.Collector) error {
	for _, c := range collectors {
		if err := registry.Register(c); err != nil {
			if _, exists := err.(prometheus.AlreadyRegisteredError); exists {
				continue
			}
			return err
		}
	}
	return nil
}
