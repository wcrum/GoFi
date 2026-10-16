package controller

import (
	"context"
	"fmt"

	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/spec"
)

func (c *Controller) ValidateTarget(target spec.KubernetesTarget, network string) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if network != "tcp" && network != "udp" {
		return fmt.Errorf("network must be tcp or udp")
	}
	if c.drv.Kind() != "kubernetes" {
		return fmt.Errorf("Kubernetes selector targets require the Kubernetes driver")
	}
	if scoped, ok := c.drv.(interface{ Namespace() string }); ok && target.Namespace != "" && target.Namespace != scoped.Namespace() {
		return fmt.Errorf("Kubernetes targets must use the controller namespace %q", scoped.Namespace())
	}
	if _, ok := c.drv.(driver.TargetResolver); !ok {
		return fmt.Errorf("driver does not support Kubernetes selector resolution")
	}
	return nil
}
func (c *Controller) ResolveTargets(ctx context.Context, target spec.KubernetesTarget, network string) ([]driver.Endpoint, error) {
	if err := c.ValidateTarget(target, network); err != nil {
		return nil, err
	}
	return c.drv.(driver.TargetResolver).ResolveTargets(ctx, target, network)
}
