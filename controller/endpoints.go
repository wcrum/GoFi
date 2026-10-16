package controller

import (
	"context"
	"github.com/wcrum/gofi/driver"
)

func (c *Controller) Endpoints(ctx context.Context) ([]driver.Endpoint, error) {
	if discoverer, ok := c.drv.(driver.EndpointDiscoverer); ok {
		return discoverer.Endpoints(ctx)
	}
	return []driver.Endpoint{}, nil
}
