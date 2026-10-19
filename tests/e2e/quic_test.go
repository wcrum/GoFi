//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/wcrum/gofi/internal/testkit/flows"
	"github.com/wcrum/gofi/internal/testkit/traffic"
	"github.com/wcrum/gofi/spec"
)

// QUIC scenarios (docs/quic.md). QUIC is a listen network (quic over UDP on
// the flow's port, one accepted stream = one framed byte stream) and a sink
// type (quic://host:port). Every test here skips while the running GoFi
// build rejects the quic value, so the suite stays green until the data
// plane lands.

// requireQUIC skips the test when the controller does not accept a QUIC
// listen source. It validates (without saving) a flow shaped exactly like the
// tests' flows but with network quic; an invalid-argument answer naming the
// network means the build predates QUIC.
func requireQUIC(t *testing.T, opts ...flows.Option) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	probe := listenFlow("quic-probe", ports(1), append([]flows.Option{flows.QUIC()}, opts...)...)
	_, err := deployment.API().Validate(ctx, probe)
	if err == nil {
		return
	}
	msg := strings.ToLower(err.Error())
	if connect.CodeOf(err) == connect.CodeInvalidArgument && (strings.Contains(msg, "network") || strings.Contains(msg, "quic") || strings.Contains(msg, "sink")) {
		t.Skipf("this GoFi build does not accept QUIC flows yet; skipping until the QUIC data plane lands: %v", err)
	}
	t.Fatalf("validate quic flow: %v", err)
}

// quicGenerator is the tests' QUIC client: one connection, streams parallel
// streams, newline-delimited JSON with ids, default TLS (skip verification,
// ALPN gofi/1) against the flow's ephemeral certificate.
func quicGenerator(def spec.FlowDefinition, prefix string, rate, streams int, reconnect bool) *traffic.Generator {
	return &traffic.Generator{Network: "quic", Addr: deployment.DataAddr(def), Prefix: prefix, Rate: rate, Connections: streams, Reconnect: reconnect}
}

// TestQUICRestartUnderLoad is TestControllerRestartUnderLoad over QUIC: two
// streams on one connection send while the controller restarts gracefully
// and then abruptly. The client notices the dead connection through its idle
// timeout, redials, delivery resumes, and the invariants hold.
func TestQUICRestartUnderLoad(t *testing.T) {
	requireQUIC(t)
	rep := scenario(t)
	rx := receiver(t)
	port := ports(1)
	def := deploy(t, listenFlow("quic-restart", port, flows.QUIC(), flows.HTTPSink(deployment.SinkURL(rx))))
	rep.Set("network", "quic")
	for _, kind := range []string{"graceful", "sigkill"} {
		t.Run(kind, func(t *testing.T) {
			rx.Reset()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			gen := quicGenerator(def, kind, 300, 2, true)
			genCtx, stopGen := context.WithCancel(ctx)
			results := make(chan traffic.Result, 1)
			go func() { results <- gen.Run(genCtx, 0) }()
			if err := rx.WaitFor(ctx, 100); err != nil {
				t.Fatalf("no traffic before restart: %v", err)
			}
			before := rx.Stats()
			restartStart := time.Now()
			var err error
			if kind == "graceful" {
				err = deployment.Restart(ctx)
			} else {
				err = deployment.Kill(ctx)
			}
			if err != nil {
				t.Fatalf("%s restart: %v", kind, err)
			}
			readyAfter := time.Since(restartStart)
			waitRunning(t, def)
			resumeDeadline, cancelResume := context.WithTimeout(ctx, 60*time.Second)
			if err := rx.WaitFor(resumeDeadline, before.Unique+200); err != nil {
				cancelResume()
				t.Fatalf("delivery did not resume after %s restart: %v", kind, err)
			}
			cancelResume()
			time.Sleep(2 * time.Second)
			stopGen()
			res := <-results
			ctxQuiet, cancelQuiet := context.WithTimeout(ctx, 20*time.Second)
			_ = rx.WaitQuiet(ctxQuiet, 2*time.Second)
			cancelQuiet()
			stats := rx.Stats()
			missing := rx.Missing(res.IDs)
			lostAfter := 0
			for _, id := range missing {
				if seenAfterRestart(id, res, restartStart.Add(readyAfter)) {
					lostAfter++
				}
			}
			gap := deliveryGap(rx, res)
			rep.Set(kind+".streams", 2)
			rep.Set(kind+".sent", res.Sent)
			rep.Set(kind+".sendFailures", res.Failed)
			rep.Set(kind+".reconnects", res.Reconnect)
			rep.Set(kind+".deliveredUnique", stats.Unique)
			rep.Set(kind+".duplicates", stats.Duplicates)
			rep.Set(kind+".missing", len(missing))
			rep.Set(kind+".missingAfterReady", lostAfter)
			rep.Set(kind+".readyAfterSeconds", readyAfter.Seconds())
			rep.Set(kind+".deliveryGapSeconds", gap.Seconds())
			t.Logf("%s: sent=%d delivered=%d missing=%d (after ready: %d) dup=%d reconnects=%d ready=%s gap=%s", kind, res.Sent, stats.Unique, len(missing), lostAfter, stats.Duplicates, res.Reconnect, readyAfter.Round(time.Millisecond), gap.Round(time.Millisecond))
			if stats.Unique < before.Unique+200 {
				t.Fatalf("delivery did not resume: %+v", stats)
			}
			if kind == "graceful" && float64(len(missing)) > 0.02*float64(res.Sent) {
				t.Fatalf("graceful restart lost %d of %d acknowledged messages (>2%%)", len(missing), res.Sent)
			}
			if gap > 45*time.Second {
				t.Fatalf("delivery gap %s exceeds 45s", gap)
			}
			assertInvariants(t, rep, defaultOptions())
		})
	}
}

// TestQUICManyStreams multiplexes 64 streams on one QUIC connection and
// sends 10k messages as fast as the flow accepts them; every id must arrive.
func TestQUICManyStreams(t *testing.T) {
	requireQUIC(t)
	rep := scenario(t)
	rx := receiver(t)
	def := deploy(t, listenFlow("quic-streams", ports(1), flows.QUIC(), flows.HTTPSink(deployment.SinkURL(rx))))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	const streams, total = 64, 10000
	gen := quicGenerator(def, "ms", 0, streams, false)
	start := time.Now()
	res := gen.Run(ctx, total)
	sendDuration := time.Since(start)
	deadline, cancelDeadline := context.WithTimeout(ctx, 90*time.Second)
	err := rx.WaitFor(deadline, int(res.Sent))
	cancelDeadline()
	stats := rx.Stats()
	missing := rx.Missing(res.IDs)
	rep.Set("network", "quic")
	rep.Set("connections", 1)
	rep.Set("streams", streams)
	rep.Set("sent", res.Sent)
	rep.Set("sendFailures", res.Failed)
	rep.Set("sendSeconds", sendDuration.Seconds())
	rep.Set("sendMsgsPerSecond", float64(res.Sent)/max(sendDuration.Seconds(), 1e-9))
	rep.Set("delivered", stats.Unique)
	rep.Set("duplicates", stats.Duplicates)
	rep.Set("missing", len(missing))
	rep.Set("deliveredFraction", float64(stats.Unique)/float64(max(res.Sent, 1)))
	t.Logf("%d streams: sent %d in %s (%.0f/s), delivered %d, missing %d, dup %d", streams, res.Sent, sendDuration.Round(time.Millisecond), float64(res.Sent)/sendDuration.Seconds(), stats.Unique, len(missing), stats.Duplicates)
	if res.Sent != total || res.Failed != 0 {
		t.Fatalf("generator sent %d of %d with %d failures: %s", res.Sent, total, res.Failed, res.FirstErr)
	}
	if err != nil || len(missing) != 0 {
		t.Fatalf("%d of %d ids were not delivered (first: %v)", len(missing), res.Sent, head(missing, 5))
	}
	assertInvariants(t, rep, defaultOptions())
}

// TestQUICToQUICSink chains two flows over QUIC: A listens on QUIC and
// forwards each message's fields over a QUIC sink to B's QUIC port; B posts
// to the HTTP receiver. Every id sent to A must reach the receiver through B.
func TestQUICToQUICSink(t *testing.T) {
	peerHost := peerFlowHost(t)
	portA := ports(2)
	portB := portA + 1
	sinkURL := fmt.Sprintf("quic://%s:%d", peerHost, portB)
	requireQUIC(t)
	requireQUIC(t, flows.QUICSink(sinkURL))
	rep := scenario(t)
	rx := receiver(t)
	deploy(t, listenFlow("quic-chain-b", portB, flows.QUIC(), flows.HTTPSink(deployment.SinkURL(rx))))
	// A's sink skips verification (flows.QUICSink): B serves an ephemeral
	// self-signed certificate.
	a := deploy(t, listenFlow("quic-chain-a", portA, flows.QUIC(), flows.QUICSink(sinkURL)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	const total = 2000
	gen := quicGenerator(a, "chain", 0, 4, false)
	start := time.Now()
	res := gen.Run(ctx, total)
	deadline, cancelDeadline := context.WithTimeout(ctx, 60*time.Second)
	err := rx.WaitFor(deadline, int(res.Sent))
	cancelDeadline()
	endToEnd := time.Since(start)
	stats := rx.Stats()
	missing := rx.Missing(res.IDs)
	rep.Set("network", "quic")
	rep.Set("hops", 2)
	rep.Set("sent", res.Sent)
	rep.Set("sendFailures", res.Failed)
	rep.Set("delivered", stats.Unique)
	rep.Set("duplicates", stats.Duplicates)
	rep.Set("missing", len(missing))
	rep.Set("endToEndSeconds", endToEnd.Seconds())
	t.Logf("A(quic) -> quic sink -> B(quic) -> http: sent %d, delivered %d, missing %d, dup %d in %s", res.Sent, stats.Unique, len(missing), stats.Duplicates, endToEnd.Round(time.Millisecond))
	if res.Failed != 0 {
		t.Fatalf("%d sends to flow A failed: %s", res.Failed, res.FirstErr)
	}
	if err != nil || len(missing) != 0 {
		hint := ""
		if stats.Unique == 0 {
			hint = " (nothing arrived through B: check that the quic sink accepts B's self-signed certificate with tls.insecureSkipVerify and writes newline-delimited fields)"
		}
		t.Fatalf("%d of %d ids did not arrive end to end%s; first missing: %v", len(missing), res.Sent, hint, head(missing, 5))
	}
	assertInvariants(t, rep, defaultOptions())
}

// TestQUICFlowEditDuringTraffic edits a QUIC flow while a stream is sending:
// the runtime is replaced once, the client reconnects, post-edit messages
// carry the new transform, and nothing is delivered by the old runtime after
// the new one started.
func TestQUICFlowEditDuringTraffic(t *testing.T) {
	requireQUIC(t)
	rep := scenario(t)
	rx := receiver(t)
	rx.KeepFields(true)
	port := ports(1)
	def := deploy(t, listenFlow("quic-edit", port, flows.QUIC(), flows.HTTPSink(deployment.SinkURL(rx))))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	gen := quicGenerator(def, "qedit", 200, 1, true)
	genCtx, stopGen := context.WithCancel(ctx)
	results := make(chan traffic.Result, 1)
	go func() { results <- gen.Run(genCtx, 0) }()
	if err := rx.WaitFor(ctx, 100); err != nil {
		t.Fatal(err)
	}
	edited := listenFlow("quic-edit", port, flows.QUIC(), flows.HTTPSink(deployment.SinkURL(rx)), flows.Transform("mark", "edited", `"v2"`))
	editAt := time.Now()
	if err := deployment.API().Upsert(ctx, edited); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, edited)
	deadline := time.Now().Add(60 * time.Second)
	var firstV2 time.Time
	for time.Now().Before(deadline) {
		if f := rx.LastFields(); f["edited"] == "v2" {
			firstV2 = time.Now()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if firstV2.IsZero() {
		t.Fatalf("no message with the edited field arrived; last fields: %v", rx.LastFields())
	}
	time.Sleep(time.Second)
	stopGen()
	res := <-results
	quiet, cancelQuiet := context.WithTimeout(ctx, 20*time.Second)
	_ = rx.WaitQuiet(quiet, time.Second)
	cancelQuiet()
	gap := deliveryGap(rx, res)
	stale := 0
	for _, id := range res.IDs {
		at, ok := rx.FirstSeen(id)
		if !ok || !at.After(firstV2) {
			continue
		}
		if f, _ := rx.Fields(id); f["edited"] != "v2" {
			stale++
		}
	}
	rep.Set("network", "quic")
	rep.Set("sent", res.Sent)
	rep.Set("reconnects", res.Reconnect)
	rep.Set("delivered", rx.Stats().Unique)
	rep.Set("missing", len(rx.Missing(res.IDs)))
	rep.Set("editToFirstV2Seconds", firstV2.Sub(editAt).Seconds())
	rep.Set("deliveryGapSeconds", gap.Seconds())
	rep.Set("staleAfterEdit", stale)
	t.Logf("edit applied in %s, gap %s, reconnects %d, stale deliveries after edit: %d", firstV2.Sub(editAt).Round(time.Millisecond), gap.Round(time.Millisecond), res.Reconnect, stale)
	if stale > 0 {
		t.Fatalf("%d messages were processed by the old runtime after the new one delivered", stale)
	}
	assertInvariants(t, rep, defaultOptions())
}

// peerFlowHost is the host a flow runtime uses to reach another flow's data
// port in this deployment shape.
func peerFlowHost(t *testing.T) string {
	t.Helper()
	switch d := deployment.(type) {
	case *binaryDeployment:
		return "127.0.0.1"
	case *composeDeployment:
		return d.gateway // sibling containers reach published ports via the host
	default:
		t.Skipf("flow-to-flow addressing is not modeled for the %s shape", deployment.Name())
		return ""
	}
}

func head(ids []string, n int) []string {
	if len(ids) > n {
		return ids[:n]
	}
	return ids
}
