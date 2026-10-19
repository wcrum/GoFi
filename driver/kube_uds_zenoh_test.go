package driver

import (
	"testing"

	"github.com/wcrum/gofi/spec"
)

func TestZenohSourceEgressAllowsRouterRESTPort(t *testing.T) {
	src := spec.FlowSource{Subscribe: &spec.SubscribeSource{Zenoh: &spec.ZenohSubscribe{URL: "http://zenoh-router.iot.svc.cluster.local:8000", KeyExpr: "demo/**"}}}
	allow := sourceEgress("telemetry", src)
	if allow["remoteNamespace"] != "iot" || allow["port"] != int64(8000) || allow["remoteProtocol"] != "TCP" {
		t.Fatalf("allow %+v", allow)
	}
	if sourceURL(src) != "http://zenoh-router.iot.svc.cluster.local:8000" {
		t.Fatal("connectivity must probe the router REST origin")
	}
}
