package source

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/wcrum/gofi/spec"
)

// Needs a NATS server: GOFI_TEST_NATS_URL=nats://127.0.0.1:4222 go test ./source
func TestNATSSubscribeWithQueueGroup(t *testing.T) {
	url := os.Getenv("GOFI_TEST_NATS_URL")
	if url == "" {
		t.Skip("set GOFI_TEST_NATS_URL to run against a NATS server")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	go Run(ctx, "n", spec.FlowSource{Subscribe: &spec.SubscribeSource{NATS: &spec.NATSSubscribe{URL: url, Subject: "gofi.test.>", Queue: "q"}}}, c.emit)
	pub, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(c.got) == 0 && time.Now().Before(deadline) {
		pub.Publish("gofi.test.a", []byte("hello"))
		pub.Flush()
		time.Sleep(100 * time.Millisecond)
	}
	if frames := c.wait(t, 1); frames[0] != "hello" {
		t.Fatalf("frames %q", frames)
	}
}
