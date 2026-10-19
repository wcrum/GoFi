package listener

import (
	"context"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/wcrum/gofi/config"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/pipeline"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

// countingSink counts sends without any output.
type countingSink struct{ n atomic.Int64 }

func (s *countingSink) Send(*message.Message) error { s.n.Add(1); return nil }
func (s *countingSink) Close() error                { return nil }

func capacityListener(t *testing.T, network string, limits Limits, out sink.Sink) *Listener {
	t.Helper()
	l, err := New(spec.FlowDefinition{Name: "capacity-" + network, Source: spec.FlowSource{Listen: &spec.ListenSource{Network: network}}, Framing: spec.Framing{Mode: "line"}, Parser: spec.ParserCfg{Format: "json"}, Sinks: []spec.SinkCfg{{Type: "stdout"}}})
	if err != nil {
		t.Fatal(err)
	}
	l.sinks = []sink.Sink{out}
	l.limits = limits
	if l.pipe, err = pipeline.NewShared(l.def, l.sinks, nil, l.chain, l.Tap, nil); err != nil {
		t.Fatal(err)
	}
	return l
}

// Past the cap, new connections are accepted and closed at once (the sender
// reads EOF) while existing ones keep working; closing one frees a slot.
func TestConnectionCapRefusesCleanly(t *testing.T) {
	out := &countingSink{}
	limits := DefaultLimits()
	limits.MaxConnections = 2
	l := capacityListener(t, "tcp", limits, out)
	defer l.Close()
	go l.Run(context.Background())
	addr := l.ln.Addr().String()

	var conns []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
		fmt.Fprintf(c, `{"i":%d}`+"\n", i)
	}
	waitFor(t, func() bool { return out.n.Load() == 2 })

	refused, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer refused.Close()
	refused.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := refused.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("refused connection read err = %v, want EOF", err)
	}
	// Existing connections still deliver.
	fmt.Fprint(conns[0], `{"again":true}`+"\n")
	waitFor(t, func() bool { return out.n.Load() == 3 })

	// Closing one frees a slot.
	conns[1].Close()
	waitFor(t, func() bool { return l.connCount.Load() == 1 })
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprint(c, `{"slot":"freed"}`+"\n")
	waitFor(t, func() bool { return out.n.Load() == 4 })
}

// A connection that sends nothing for the idle timeout is closed; one that
// keeps sending is not.
func TestIdleTimeoutClosesSilentConnections(t *testing.T) {
	out := &countingSink{}
	limits := DefaultLimits()
	limits.IdleTimeout = 150 * time.Millisecond
	l := capacityListener(t, "tcp", limits, out)
	defer l.Close()
	go l.Run(context.Background())
	addr := l.ln.Addr().String()

	silent, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	silent.SetReadDeadline(time.Now().Add(3 * time.Second))
	started := time.Now()
	if _, err := silent.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("idle connection read err = %v, want EOF", err)
	}
	if elapsed := time.Since(started); elapsed < 100*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("idle close after %v", elapsed)
	}

	busy, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	for i := 0; i < 6; i++ {
		fmt.Fprintf(busy, `{"i":%d}`+"\n", i)
		time.Sleep(60 * time.Millisecond)
	}
	waitFor(t, func() bool { return out.n.Load() == 6 })
	if l.connCount.Load() != 1 {
		t.Fatalf("%d connections open, want the busy one", l.connCount.Load())
	}
}

// Idle connections must cost kilobytes each, not the frame size cap: 1000
// idle senders used to hold about 1 GiB of framer buffers.
func TestIdleConnectionsAreCheap(t *testing.T) {
	if testing.Short() {
		t.Skip("opens 1000 connections")
	}
	raiseFileLimit(t, 4096)
	const conns = 1000
	out := &countingSink{}
	limits := DefaultLimits()
	limits.MaxConnections = conns + 10
	l := capacityListener(t, "tcp", limits, out)
	defer l.Close()
	go l.Run(context.Background())
	addr := l.ln.Addr().String()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	clients := make([]net.Conn, 0, conns)
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()
	for i := 0; i < conns; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		clients = append(clients, c)
		fmt.Fprint(c, `{"a":1}`+"\n")
	}
	waitFor(t, func() bool { return out.n.Load() == conns })
	waitFor(t, func() bool { return l.connCount.Load() == conns })

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	inuse := func(m *runtime.MemStats) uint64 { return m.HeapInuse + m.StackInuse }
	perConn := float64(inuse(&after)-inuse(&before)) / conns
	t.Logf("%d idle connections: %.1f KiB each (heap+stack in use)", conns, perConn/1024)
	// Each server connection holds the configured initial framer buffer
	// (GOFI_FRAMER_INITIAL_BUFFER, 64 KiB by default, 4 KiB on the edge
	// profile) plus a goroutine; client sockets in this process count too.
	// The bound is far below the 1 MiB per connection the framer used to
	// preallocate.
	bound := float64(config.Get().Limits.FramerInitialBuffer + 32<<10)
	if perConn > bound {
		t.Fatalf("%.0f bytes per idle connection, bound %.0f", perConn, bound)
	}
}

func raiseFileLimit(t *testing.T, want uint64) {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return
	}
	if limit.Cur >= want {
		return
	}
	if limit.Max < want {
		t.Skipf("file descriptor hard limit %d too low", limit.Max)
	}
	limit.Cur = want
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Skipf("cannot raise file descriptor limit: %v", err)
	}
}

// UDP loss at rising send rates on loopback. The kernel buffer (4 MiB
// requested) absorbs bursts the single reader has not drained yet; at modest
// rates nothing may be lost. The test reports the received fraction per rate
// and fails only on a loss that no buffer explains.
func TestUDPLossAtRisingRates(t *testing.T) {
	if testing.Short() {
		t.Skip("sends tens of thousands of datagrams")
	}
	out := &countingSink{}
	l := capacityListener(t, "udp", DefaultLimits(), out)
	defer l.Close()
	go l.Run(context.Background())
	conn, err := net.Dial("udp", l.packet.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := []byte(`{"type":"a-f-G","lat":38.9,"lon":-77.0,"id":"sensor-42"}` + "\n")

	for _, rate := range []int{2000, 10000, 20000} {
		n := rate // one second's worth
		start := out.n.Load()
		gap := time.Second / time.Duration(rate)
		began := time.Now()
		for i := 0; i < n; i++ {
			conn.Write(payload)
			if d := gap*time.Duration(i+1) - time.Since(began); d > time.Millisecond {
				time.Sleep(d)
			}
		}
		time.Sleep(500 * time.Millisecond)
		got := out.n.Load() - start
		t.Logf("rate %6d/s: received %d of %d (%.1f%%)", rate, got, n, 100*float64(got)/float64(n))
		if rate <= 2000 && got < int64(n)*95/100 {
			t.Fatalf("rate %d/s: lost %d of %d datagrams on loopback", rate, int64(n)-got, n)
		}
	}
}

func TestLimitsFromConfig(t *testing.T) {
	l := FromConfig(config.Limits{MaxConnectionsPerListener: 7, ConnIdleTimeout: 90 * time.Second, UDPReadBuffer: 8 << 20, UDPWorkers: 4, SourceWorkers: 100000})
	if l.MaxConnections != 7 || l.IdleTimeout != 90*time.Second || l.UDPReadBuffer != 8<<20 || l.UDPWorkers != 4 || l.SourceWorkers != maxWorkers {
		t.Fatalf("%+v", l)
	}
	if d := FromConfig(config.Limits{}); d != DefaultLimits() {
		t.Fatalf("zero config: %+v", d)
	}
}

// With several UDP workers every datagram is still processed exactly once.
func TestUDPWorkersProcessEveryDatagram(t *testing.T) {
	out := &countingSink{}
	limits := DefaultLimits()
	limits.UDPWorkers = 4
	l := capacityListener(t, "udp", limits, out)
	defer l.Close()
	go l.Run(context.Background())
	conn, err := net.Dial("udp", l.packet.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for i := 0; i < 200; i++ {
		fmt.Fprintf(conn, `{"i":%d}`+"\n", i)
		if i%50 == 49 {
			time.Sleep(10 * time.Millisecond) // let the small loopback buffer drain
		}
	}
	waitFor(t, func() bool { return out.n.Load() == 200 })
}
