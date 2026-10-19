package driver

import (
	"context"
	"fmt"
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/wcrum/gofi/spec"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// QUIC runs over UDP: every port publication maps network/type "quic" to UDP
// while the URL scheme and sink type stay "quic" for the data path.

func TestTransportOfMapsQUICToUDP(t *testing.T) {
	for network, want := range map[string]corev1.Protocol{"": corev1.ProtocolTCP, "tcp": corev1.ProtocolTCP, "udp": corev1.ProtocolUDP, "quic": corev1.ProtocolUDP, "QUIC": corev1.ProtocolUDP, "http": corev1.ProtocolTCP, "nats": corev1.ProtocolTCP} {
		if got := transportOf(network); got != want {
			t.Errorf("transportOf(%q) = %s, want %s", network, got, want)
		}
	}
	if kind, resource := routeKind("quic"); kind != "UDPRoute" || resource != "udproutes" {
		t.Errorf("routeKind(quic) = %s/%s, want UDPRoute/udproutes", kind, resource)
	}
	if kind, _ := routeKind("tcp"); kind != "TCPRoute" {
		t.Errorf("routeKind(tcp) = %s, want TCPRoute", kind)
	}
}

func TestDockerDataPortPublishesQUICAsUDP(t *testing.T) {
	def := testDef("quic-ingest", 4433)
	def.Source.Listen.Network = "quic"
	exposed, bindings, err := dockerDataPort(def)
	if err != nil {
		t.Fatalf("dockerDataPort: %v", err)
	}
	want, _ := nat.NewPort("udp", "4433")
	if _, ok := exposed[want]; !ok || len(exposed) != 1 {
		t.Errorf("ExposedPorts = %v, want exactly %v", exposed, want)
	}
	if b := bindings[want]; len(b) != 1 || b[0].HostPort != "4433" {
		t.Errorf("PortBindings = %v, want 1:1 host port 4433/udp", bindings)
	}
}

func TestKubeServiceForQUICListenerIsUDP(t *testing.T) {
	k, _ := newTestKube("gofi")
	def := testDef("quic-ingest", 4433)
	def.Source.Listen.Network = "quic"
	def.Source.Listen.Service = &spec.ServiceExposure{Type: "NodePort", NodePort: 31433}
	svc := k.desiredService(def)
	port := svc.Spec.Ports[0]
	if port.Protocol != corev1.ProtocolUDP || port.Port != 4433 || port.TargetPort.IntValue() != 4433 || port.NodePort != 31433 {
		t.Fatalf("quic Service port = %+v, want UDP 4433 -> 4433 nodePort 31433", port)
	}
}

func TestUDSAllowsTreatQUICAsUDP(t *testing.T) {
	defs := []spec.FlowDefinition{
		{Name: "q", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 4433, Network: "quic", Service: &spec.ServiceExposure{Type: "LoadBalancer"}}},
			Sinks: []spec.SinkCfg{
				{Type: "quic", URL: "quic://10.1.2.3:4433"},
				{Type: "quic", URL: "quic://receiver.apps.svc.cluster.local:4434"},
				{Type: "quic", Kubernetes: &spec.KubernetesTarget{Namespace: "apps", Selector: map[string]string{"app": "receiver"}, Port: 4435}},
			}},
		{Name: "qgw", Source: spec.FlowSource{Listen: &spec.ListenSource{Port: 4436, Network: "quic", Service: &spec.ServiceExposure{Type: "Gateway", Gateway: &spec.GatewayExposure{Namespace: "envoy-gateway-system", Name: "edge"}}}}},
	}
	allows := allowsByDescription(defs)
	if len(allows) != 5 {
		t.Fatalf("want 5 allows, got %d: %v", len(allows), allows)
	}
	for description, wantPort := range map[string]int64{
		"GoFi flow q LoadBalancer ingress":                             4433,
		"GoFi flow q to 10.1.2.3":                                      4433,
		"GoFi flow q to receiver.apps.svc.cluster.local":               4434,
		"GoFi flow q to apps pods":                                     4435,
		"GoFi flow qgw ingress from Gateway envoy-gateway-system/edge": 4436,
	} {
		entry := allows[description]
		if entry == nil || entry["remoteProtocol"] != "UDP" || entry["port"] != wantPort {
			t.Errorf("%s: want remoteProtocol UDP port %d, got %v", description, wantPort, entry)
		}
	}
	if ip := allows["GoFi flow q to 10.1.2.3"]; ip["remoteCidr"] != "10.1.2.3/32" {
		t.Errorf("quic IP egress keeps the /32: %v", ip)
	}
}

func TestKubernetesTargetsForQUICSinkCarryUDPPorts(t *testing.T) {
	ctx := context.Background()
	k, cs := newTestKube("apps")
	if _, err := cs.CoreV1().Pods("apps").Create(ctx, receiverPod("first", "10.0.0.1", true), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	target := spec.KubernetesTarget{Selector: map[string]string{"app": "receiver"}, Port: 4433}
	endpoints, err := k.ResolveTargets(ctx, target, "quic")
	if err != nil || len(endpoints) != 1 {
		t.Fatalf("quic targets: %+v %v", endpoints, err)
	}
	if p := endpoints[0].Ports[0]; p.Protocol != "UDP" || p.URL != "quic://10.0.0.1:4433" {
		t.Fatalf("quic endpoint port: %+v, want protocol UDP and a quic:// URL", p)
	}
	if _, err := k.ResolveTargets(ctx, target, "sctp"); err == nil {
		t.Fatal("unknown target network accepted")
	}
	cfg := spec.SinkCfg{Type: "quic", Kubernetes: &target}
	svc := targetService("q-target", "apps", "q", nil, cfg)
	if svc.Spec.Ports[0].Protocol != corev1.ProtocolUDP || svc.Spec.Ports[0].Port != 4433 {
		t.Fatalf("quic target Service port: %+v", svc.Spec.Ports[0])
	}
	if fmt.Sprint(svc.Spec.Selector) != "map[app:receiver]" {
		t.Fatalf("target selector: %v", svc.Spec.Selector)
	}
}
