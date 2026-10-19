package sink

import (
	"context"
	"net"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/wcrum/gofi/message"
)

func TestFanoutSendsToEveryPod(t *testing.T) {
	// Pods of a headless Service share a port; locally each listener needs its
	// own port, so the peers are installed directly instead of through DNS.
	f := newFanout("udp", "target", "", func(address string) Sink {
		return &networkSink{network: "udp", address: address, bodyMode: "raw"}
	})
	var listeners []net.PacketConn
	for range 3 {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		listeners = append(listeners, pc)
		f.peers[pc.LocalAddr().String()] = f.newPeer(pc.LocalAddr().String())
	}
	f.resolved = time.Now()
	if err := f.Send(&message.Message{Raw: []byte("hello")}); err != nil {
		t.Fatal(err)
	}
	for i, pc := range listeners {
		buf := make([]byte, 64)
		_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := pc.ReadFrom(buf)
		if err != nil || string(buf[:n]) != "hello" {
			t.Fatalf("pod %d did not receive the message: %q %v", i, buf[:n], err)
		}
	}
}

func TestFanoutRefreshDropsGonePods(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	_, port, _ := net.SplitHostPort(pc.LocalAddr().String())
	ips := []string{"127.0.0.1", "127.0.0.2"}
	f := newFanout("udp", "target", port, func(address string) Sink {
		return &networkSink{network: "udp", address: address, bodyMode: "raw"}
	})
	f.lookup = func(context.Context, string) ([]string, error) { return ips, nil }
	peers, err := f.targets()
	if err != nil || len(peers) != 2 {
		t.Fatalf("want 2 peers: %v %v", peers, err)
	}
	ips = []string{"127.0.0.1"}
	f.resolved = time.Time{}
	peers, _ = f.targets()
	keys := []string{}
	for key := range f.peers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(peers) != 1 || keys[0] != net.JoinHostPort("127.0.0.1", port) {
		t.Fatalf("gone pod not dropped: %v", keys)
	}
	if _, err := strconv.Atoi(port); err != nil {
		t.Fatal(err)
	}
	if err := f.Send(&message.Message{Raw: []byte("x")}); err != nil {
		t.Fatal(err)
	}
}
