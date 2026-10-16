package api

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/spec"
)

type resolvingDriver struct {
	streamDriver
	endpoints []driver.Endpoint
}

func (d *resolvingDriver) Kind() string      { return "kubernetes" }
func (d *resolvingDriver) Namespace() string { return "apps" }
func (d *resolvingDriver) ResolveTargets(context.Context, spec.KubernetesTarget, string) ([]driver.Endpoint, error) {
	return d.endpoints, nil
}
func targetClient(endpoints []driver.Endpoint) gofiv1connect.GoFiServiceClient {
	ctrl := controller.New(&resolvingDriver{endpoints: endpoints})
	return gofiv1connect.NewGoFiServiceClient(handlerClient{New(ctrl, nil, deploymode.ModeKubernetes)}, "http://gofi.test")
}
func replayReceiver(t *testing.T, network string) (string, <-chan []byte) {
	t.Helper()
	received := make(chan []byte, 1)
	if network == "tcp" {
		ln, err := net.Listen(network, "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			raw, err := io.ReadAll(conn)
			if err == nil {
				received <- raw
			}
		}()
		return ln.Addr().String(), received
	}
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		raw := make([]byte, 65536)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _, err := conn.ReadFrom(raw)
		if err == nil {
			received <- append([]byte(nil), raw[:n]...)
		}
	}()
	return conn.LocalAddr().String(), received
}
func TestConnectReplayPreservesExactTCPUDPWireBytes(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			client, ctrl, _ := newTestClient()
			address, received := replayReceiver(t, network)
			raw := []byte{0, 1, 'G', 'o', 'F', 'i', '\n'}
			response, err := client.Replay(context.Background(), connect.NewRequest(&gofiv1.ReplayRequest{Network: network, Url: network + "://" + address, Raw: raw}))
			if err != nil || len(response.Msg.Deliveries) != 1 || !response.Msg.Deliveries[0].Sent {
				t.Fatalf("replay result: %+v %v", response, err)
			}
			select {
			case got := <-received:
				if !bytes.Equal(raw, got) {
					t.Fatalf("replay changed payload %v into %v", raw, got)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("replay never arrived")
			}
			if len(ctrl.List()) != 0 {
				t.Fatal("replay changed durable listeners")
			}
		})
	}
}
func TestConnectSelectorReplayFansOutToEveryResolvedPod(t *testing.T) {
	target := &gofiv1.KubernetesTarget{Selector: map[string]string{"app": "receiver"}, Port: 7777}
	endpoints := []driver.Endpoint{}
	receives := []<-chan []byte{}
	for _, name := range []string{"receiver-a", "receiver-b"} {
		address, received := replayReceiver(t, "tcp")
		host, portString, _ := net.SplitHostPort(address)
		port, _ := strconv.Atoi(portString)
		endpoints = append(endpoints, driver.Endpoint{Name: name, Namespace: "apps", Kind: "kubernetes", Type: "Pod", Ready: true, Host: host, Ports: []driver.EndpointPort{{Port: int32(port), URL: "tcp://" + address, Protocol: "TCP"}}})
		receives = append(receives, received)
	}
	client := targetClient(endpoints)
	preview, err := client.ResolveTargets(context.Background(), connect.NewRequest(&gofiv1.ResolveTargetsRequest{Target: target, Network: "tcp"}))
	if err != nil || len(preview.Msg.Endpoints) != 2 {
		t.Fatalf("preview: %v %v", preview, err)
	}
	raw := []byte("sample\n")
	response, err := client.Replay(context.Background(), connect.NewRequest(&gofiv1.ReplayRequest{Target: target, Network: "tcp", Raw: raw}))
	if err != nil || len(response.Msg.Deliveries) != 2 {
		t.Fatalf("fan-out: %v %v", response, err)
	}
	for i, delivery := range response.Msg.Deliveries {
		if !delivery.Sent {
			t.Fatalf("destination %d failed: %+v", i, delivery)
		}
		select {
		case got := <-receives[i]:
			if !bytes.Equal(raw, got) {
				t.Fatalf("destination %d altered payload", i)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("destination %d did not receive", i)
		}
	}
}
func TestConnectReplayRejectsInvalidOrUnresolvedDestinations(t *testing.T) {
	target := &gofiv1.KubernetesTarget{Selector: map[string]string{"app": "receiver"}, Port: 7777}
	requests := []*gofiv1.ReplayRequest{
		{Network: "sctp", Url: "tcp://127.0.0.1:9000", Raw: []byte("x")},
		{Network: "tcp", Url: "http://127.0.0.1:9000", Raw: []byte("x")},
		{Network: "tcp", Url: "tcp://127.0.0.1:9000"},
		{Network: "tcp", Url: "tcp://127.0.0.1:9000", Raw: make([]byte, maxReplayBytes+1)},
		{Network: "udp", Url: "udp://127.0.0.1:9000", Raw: make([]byte, 65508)},
		{Network: "tcp", Target: target, Raw: []byte("x")}, // Ready membership is empty.
		{Network: "tcp", Target: target, Url: "tcp://127.0.0.1:9000", Raw: []byte("x")},
		{Network: "tcp", Target: &gofiv1.KubernetesTarget{Namespace: "other", Selector: map[string]string{"app": "receiver"}, Port: 7777}, Raw: []byte("x")},
	}
	client := targetClient(nil)
	for _, request := range requests {
		_, err := client.Replay(context.Background(), connect.NewRequest(request))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("accepted invalid replay %+v: %v", request, err)
		}
	}
	binary, _, _ := newTestClient()
	_, err := binary.ResolveTargets(context.Background(), connect.NewRequest(&gofiv1.ResolveTargetsRequest{Target: target, Network: "tcp"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("binary accepted Kubernetes target: %v", err)
	}
}
