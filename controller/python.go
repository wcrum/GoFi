package controller

// NotifyRuntimeChange schedules reconciliation after worker readiness or
// credentials change. Only listeners carrying stale bindings are recreated.
func (c *Controller) NotifyRuntimeChange() {
	c.trigger()
}
