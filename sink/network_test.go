package sink

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestNetworkSinkForwardsToExplicitEndpoint(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			var address string
			received := make(chan string, 1)
			if network == "tcp" {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer ln.Close()
				address = ln.Addr().String()
				go func() {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					defer conn.Close()
					conn.SetReadDeadline(time.Now().Add(time.Second))
					buf := make([]byte, 8)
					if _, err := io.ReadFull(conn, buf); err == nil {
						received <- string(buf)
					}
				}()
			} else {
				conn, err := net.ListenPacket("udp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				address = conn.LocalAddr().String()
				go func() {
					buf := make([]byte, 64)
					conn.SetReadDeadline(time.Now().Add(time.Second))
					n, _, err := conn.ReadFrom(buf)
					if err == nil {
						received <- string(buf[:n])
					}
				}()
			}
			sk, err := New(spec.SinkCfg{Type: network, URL: network + "://" + address, Delimiter: "\n"}, "test")
			if err != nil {
				t.Fatal(err)
			}
			defer sk.Close()
			raw := []byte("payload")
			if err := sk.Send(&message.Message{Raw: raw}); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-received:
				if got != "payload\n" {
					t.Fatalf("received %q", got)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("message not forwarded")
			}
			if string(raw) != "payload" {
				t.Fatal("sink modified message bytes")
			}
		})
	}
}

func TestNetworkSinkRejectsMalformedEndpoints(t *testing.T) {
	for _, address := range []string{"", "http://host:1234", "tcp://host", "tcp://host:99999", "tcp://user:pass@host:1234", "tcp://host:1234/path"} {
		if _, err := New(spec.SinkCfg{Type: "tcp", URL: address}, "test"); err == nil {
			t.Errorf("accepted %q", address)
		}
	}
}

func TestNetworkSinkUsesDeclarativeSelectorService(t *testing.T) {
	t.Setenv("GOFI_K8S_NAMESPACE", "apps")
	target := &spec.KubernetesTarget{Selector: map[string]string{"app": "receiver"}, Port: 7777, Delivery: "one"}
	cfg := spec.SinkCfg{Type: "tcp", Kubernetes: target}
	sk, err := newNetwork(cfg, "route")
	want := spec.KubernetesServiceName("route", "tcp", target) + ".apps.svc.cluster.local:7777"
	if single, ok := sk.(*networkSink); err != nil || !ok || single.address != want {
		t.Fatalf("selector route: %+v %v", sk, err)
	}
	if cfg.URL != "" {
		t.Fatal("runtime addressing mutated desired resource")
	}
	// Pods in another namespace sit behind the same GoFi-namespace Service.
	target.Namespace = "outside"
	target.Delivery = ""
	sk, err = newNetwork(cfg, "route")
	fan, ok := sk.(*fanoutSink)
	if err != nil || !ok || fan.host != spec.KubernetesServiceName("route", "tcp", target)+".apps.svc.cluster.local" || fan.port != "7777" {
		t.Fatalf("cross-namespace fan-out target: %+v %v", sk, err)
	}
}
