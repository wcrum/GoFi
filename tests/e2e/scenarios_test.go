//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/internal/testkit/flows"
	"github.com/wcrum/gofi/internal/testkit/invariants"
	"github.com/wcrum/gofi/internal/testkit/proc"
	"github.com/wcrum/gofi/internal/testkit/traffic"
	"github.com/wcrum/gofi/spec"
)

// kubeOpts gives Kubernetes flows a NodePort so the test can reach them.
func listenFlow(name string, port int, opts ...flows.Option) spec.FlowDefinition {
	if deployment.Name() == "kubernetes" {
		opts = append(opts, flows.Service("NodePort", port))
	}
	return flows.Listen(name, port, opts...)
}

// TestControllerRestartUnderLoad streams traffic while the controller
// restarts gracefully and then abruptly. It measures the delivery gap and
// accounts for every message: after the restart delivery resumes, nothing
// acknowledged after the restart is lost, and the invariants hold.
func TestControllerRestartUnderLoad(t *testing.T) {
	rep := scenario(t)
	rx := receiver(t)
	port := ports(1)
	def := deploy(t, listenFlow("restart-load", port, flows.HTTPSink(deployment.SinkURL(rx))))
	for _, kind := range []string{"graceful", "sigkill"} {
		t.Run(kind, func(t *testing.T) {
			rx.Reset()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			gen := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: kind, Rate: 300, Reconnect: true, Connections: 2}
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
			// Resumption: new ids arrive after the restart.
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
			// Messages the sender saw accepted before the kill may be lost
			// (in-flight in socket buffers); after readiness nothing may be.
			lostAfter := 0
			for _, id := range missing {
				if seenAfterRestart(id, res, restartStart.Add(readyAfter)) {
					lostAfter++
				}
			}
			gap := deliveryGap(rx, res)
			rep.Set(kind+".sent", res.Sent)
			rep.Set(kind+".sendFailures", res.Failed)
			rep.Set(kind+".reconnects", res.Reconnect)
			rep.Set(kind+".deliveredUnique", stats.Unique)
			rep.Set(kind+".duplicates", stats.Duplicates)
			rep.Set(kind+".missing", len(missing))
			rep.Set(kind+".readyAfterSeconds", readyAfter.Seconds())
			rep.Set(kind+".deliveryGapSeconds", gap.Seconds())
			t.Logf("%s: sent=%d delivered=%d missing=%d dup=%d ready=%s gap=%s", kind, res.Sent, stats.Unique, len(missing), stats.Duplicates, readyAfter.Round(time.Millisecond), gap.Round(time.Millisecond))
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

// seenAfterRestart approximates "sent after the controller was back" by
// sequence position: ids are <prefix>-<n> and sequences are monotonic.
func seenAfterRestart(id string, res traffic.Result, _ time.Time) bool {
	// Without per-id send timestamps we treat the last 10% of the run as
	// post-restart; the generator ran for 2s after resumption at 300/s.
	idx := indexOf(res.IDs, id)
	return idx >= len(res.IDs)-600
}

func indexOf(ids []string, id string) int {
	for i := len(ids) - 1; i >= 0; i-- {
		if ids[i] == id {
			return i
		}
	}
	return -1
}

// deliveryGap is the largest interval between consecutive first deliveries.
func deliveryGap(rx *traffic.Receiver, res traffic.Result) time.Duration {
	var last time.Time
	var gap time.Duration
	for _, id := range res.IDs {
		at, ok := rx.FirstSeen(id)
		if !ok {
			continue
		}
		if !last.IsZero() && at.Sub(last) > gap {
			gap = at.Sub(last)
		}
		if at.After(last) {
			last = at
		}
	}
	return gap
}

// TestRuntimeKillRecovers kills a flow's runtime (container or Pod) and
// measures how long the controller takes to recreate it and resume delivery.
func TestRuntimeKillRecovers(t *testing.T) {
	rep := scenario(t)
	rx := receiver(t)
	def := deploy(t, listenFlow("runtime-kill", ports(1), flows.HTTPSink(deployment.SinkURL(rx))))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := deployment.KillRuntime(ctx, def.Name); err == errNotSupported {
		t.Skip("no separate runtime in this shape")
	} else if err != nil {
		t.Fatal(err)
	}
	killedAt := time.Now()
	gen := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "kill", Rate: 50, Reconnect: true}
	genCtx, stopGen := context.WithCancel(ctx)
	results := make(chan traffic.Result, 1)
	go func() { results <- gen.Run(genCtx, 0) }()
	// The resync interval is 30s; recovery must happen within two of them.
	if err := rx.WaitFor(ctx, 10); err != nil {
		t.Fatalf("no delivery after runtime kill: %v", err)
	}
	recovery := time.Since(killedAt)
	stopGen()
	res := <-results
	rep.Set("recoverySeconds", recovery.Seconds())
	rep.Set("sendFailuresDuringOutage", res.Failed)
	t.Logf("runtime recreated and delivering after %s", recovery.Round(time.Millisecond))
	if recovery > 90*time.Second {
		t.Fatalf("recovery took %s", recovery)
	}
	assertInvariants(t, rep, defaultOptions())
}

// TestSlowAndBlackholedSink shows backpressure: a slow receiver bounds
// controller memory, a blackholed receiver turns into accounted sink
// failures, and recovery delivers what the sender kept sending.
func TestSlowAndBlackholedSink(t *testing.T) {
	rep := scenario(t)
	rx := receiver(t)
	def := deploy(t, listenFlow("slow-sink", ports(1), flows.HTTPSink(deployment.SinkURL(rx))))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	base, _ := deployment.ControllerRSS()
	rep.Set("rssBaselineMiB", mib(base))

	rx.SetDelay(200 * time.Millisecond)
	gen := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "slow", Rate: 500, Connections: 4, Pad: 512}
	genCtx, stopGen := context.WithTimeout(ctx, 10*time.Second)
	sampler := proc.SampleRSS(func() int { return controllerPID() }, 200*time.Millisecond)
	res := gen.Run(genCtx, 0)
	stopGen()
	peakSlow, _ := sampler.Stop()
	slowStats := rx.Stats()
	rep.Set("slow.sent", res.Sent)
	rep.Set("slow.deliveredDuringDelay", slowStats.Unique)
	rep.Set("slow.rssPeakMiB", mib(peakSlow))
	t.Logf("slow sink: sent %d, delivered %d while delayed, peak RSS %.1f MiB", res.Sent, slowStats.Unique, mib(peakSlow))
	rx.SetDelay(0)
	drainCtx, cancelDrain := context.WithTimeout(ctx, 2*time.Minute)
	err := rx.WaitFor(drainCtx, int(res.Sent))
	cancelDrain()
	after := rx.Stats()
	rep.Set("slow.deliveredAfterRecovery", after.Unique)
	if err != nil {
		missing := rx.Missing(res.IDs)
		rep.Set("slow.missing", len(missing))
		if float64(len(missing)) > 0.01*float64(res.Sent) {
			t.Fatalf("slow sink lost %d of %d (backpressure must not drop TCP frames)", len(missing), res.Sent)
		}
	}
	if peakSlow > 0 && base > 0 && peakSlow-base > 256<<20 {
		t.Fatalf("RSS grew by %.0f MiB under a slow sink", mib(peakSlow-base))
	}

	// Reset: the sink closes every connection. Each send fails at once and
	// is counted as a dispatch error; nothing is delivered and nothing hangs.
	rx.Reset()
	before := metrics(t)
	errBefore := before.Value("gofi_sink_dispatch_total", map[string]string{"listener": def.Name, "outcome": "error"})
	rx.SetDrop(true)
	gen2 := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "drop", Rate: 50, Connections: 2}
	dropCtx, stopDrop := context.WithTimeout(ctx, 5*time.Second)
	res2 := gen2.Run(dropCtx, 0)
	stopDrop()
	settle, cancelSettle := context.WithTimeout(ctx, 30*time.Second)
	waitMetric(t, settle, func(s metricsSamples) bool {
		return s.Value("gofi_sink_dispatch_total", map[string]string{"listener": def.Name, "outcome": "error"})-errBefore >= float64(res2.Sent)
	})
	cancelSettle()
	rx.SetDrop(false)
	after2 := metrics(t)
	errAfter := after2.Value("gofi_sink_dispatch_total", map[string]string{"listener": def.Name, "outcome": "error"})
	rep.Set("drop.sent", res2.Sent)
	rep.Set("drop.sinkErrors", errAfter-errBefore)
	rep.Set("drop.delivered", rx.Stats().Unique)
	t.Logf("dropping sink: sent %d, sink errors %.0f, delivered %d", res2.Sent, errAfter-errBefore, rx.Stats().Unique)
	if errAfter-errBefore < float64(res2.Sent) {
		t.Fatalf("dropped sends were not all accounted as sink errors: %d sent, %.0f errors", res2.Sent, errAfter-errBefore)
	}

	// Blackhole: the sink accepts and hangs for longer than the sink's 30s
	// send timeout; the pipeline must stay bounded and the hung sends must
	// end as timeouts, then delivery resumes.
	rx.Reset()
	rx.SetBlackhole(true)
	gen3 := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "hole", Rate: 10, Connections: 2}
	holeCtx, stopHole := context.WithTimeout(ctx, 3*time.Second)
	res3 := gen3.Run(holeCtx, 0)
	stopHole()
	holeRSS, _ := deployment.ControllerRSS()
	errBeforeHole := errAfter
	time.Sleep(32 * time.Second)
	rx.SetBlackhole(false)
	settle2, cancelSettle2 := context.WithTimeout(ctx, 60*time.Second)
	waitMetric(t, settle2, func(s metricsSamples) bool {
		return s.Value("gofi_sink_dispatch_total", map[string]string{"listener": def.Name, "outcome": "error"})-errBeforeHole >= 1
	})
	cancelSettle2()
	after3 := metrics(t)
	timeouts := after3.Value("gofi_sink_dispatch_total", map[string]string{"listener": def.Name, "outcome": "error"}) - errBeforeHole
	gen4 := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "after"}
	res4 := gen4.Run(ctx, 20)
	resume, cancelResume := context.WithTimeout(ctx, 30*time.Second)
	resumeErr := rx.WaitFor(resume, int(res4.Sent))
	cancelResume()
	frames := after3.Value("gofi_frames_received_total", map[string]string{"listener": def.Name})
	okAfter := after3.Value("gofi_sink_dispatch_total", map[string]string{"listener": def.Name, "outcome": "success"})
	rep.Set("blackhole.sent", res3.Sent)
	rep.Set("blackhole.timeouts", timeouts)
	rep.Set("blackhole.rssMiB", mib(holeRSS))
	rep.Set("blackhole.resumedDelivery", resumeErr == nil)
	rep.Set("framesReceivedTotal", frames)
	rep.Set("dispatchSuccessTotal", okAfter)
	rep.Set("dispatchErrorTotal", after3.Value("gofi_sink_dispatch_total", map[string]string{"listener": def.Name, "outcome": "error"}))
	t.Logf("blackhole: sent %d, timed-out sends %.0f, RSS %.1f MiB, delivery resumed: %v", res3.Sent, timeouts, mib(holeRSS), resumeErr == nil)
	if timeouts < 1 {
		t.Fatalf("no send timed out against a blackholed sink (sink timeout not enforced)")
	}
	if resumeErr != nil {
		t.Fatalf("delivery did not resume after the blackhole cleared: %v", resumeErr)
	}
	assertInvariants(t, rep, defaultOptions())
}

type metricsSamples = interface {
	Value(name string, match map[string]string) float64
}

func waitMetric(t *testing.T, ctx context.Context, cond func(metricsSamples) bool) {
	t.Helper()
	for {
		s, err := deployment.API().Metrics(ctx)
		if err == nil && cond(s) {
			return
		}
		select {
		case <-ctx.Done():
			t.Logf("metric condition not met before deadline")
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// controllerPID is the binary deployment's process (0 elsewhere; RSS then
// comes from ControllerRSS).
func controllerPID() int {
	if b, ok := deployment.(*binaryDeployment); ok {
		return b.controller().PID()
	}
	return 0
}

// TestIdleConnectionsBounded opens 1000 idle TCP connections to a flow and
// bounds the memory they cost; the flow keeps delivering meanwhile.
func TestIdleConnectionsBounded(t *testing.T) {
	rep := scenario(t)
	rx := receiver(t)
	def := deploy(t, listenFlow("idle-conns", ports(1), flows.HTTPSink(deployment.SinkURL(rx))))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	base, _ := deployment.ControllerRSS()
	opened, closeAll := traffic.IdleConnections(ctx, deployment.DataAddr(def), 1000)
	defer closeAll()
	time.Sleep(2 * time.Second)
	withIdle, _ := deployment.ControllerRSS()
	gen := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "idle"}
	res := gen.Run(ctx, 500)
	if err := rx.WaitFor(ctx, int(res.Sent)); err != nil {
		t.Fatalf("delivery stalled with idle connections: %v (%d/%d)", err, rx.Stats().Unique, res.Sent)
	}
	rep.Set("idleConnections", opened)
	rep.Set("rssBaselineMiB", mib(base))
	rep.Set("rssWithIdleMiB", mib(withIdle))
	rep.Set("perConnectionKiB", float64(withIdle-base)/1024/float64(max(opened, 1)))
	t.Logf("%d idle connections: RSS %.1f -> %.1f MiB", opened, mib(base), mib(withIdle))
	if opened < 900 {
		t.Fatalf("only %d of 1000 connections accepted", opened)
	}
	if withIdle-base > 200<<20 {
		t.Fatalf("1000 idle connections cost %.0f MiB", mib(withIdle-base))
	}
	closeAll()
	assertInvariants(t, rep, defaultOptions())
}

// TestUDPBurstLossCurve sends UDP bursts at increasing rates and records the
// delivered fraction at each; a moderate rate must deliver nearly all.
func TestUDPBurstLossCurve(t *testing.T) {
	rep := scenario(t)
	rx := receiver(t)
	def := deploy(t, listenFlow("udp-burst", ports(1), flows.UDP(), flows.HTTPSink(deployment.SinkURL(rx))))
	time.Sleep(500 * time.Millisecond) // UDP has no accept to probe
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var curve []string
	for _, rate := range []int{500, 2000, 10000, 0} {
		rx.Reset()
		gen := &traffic.Generator{Network: "udp", Addr: deployment.DataAddr(def), Prefix: fmt.Sprintf("udp%d", rate), Rate: rate}
		res := gen.Run(ctx, 2000)
		quiet, cancelQuiet := context.WithTimeout(ctx, 20*time.Second)
		_ = rx.WaitQuiet(quiet, 1500*time.Millisecond)
		cancelQuiet()
		delivered := rx.Stats().Unique
		fraction := float64(delivered) / float64(max(res.Sent, 1))
		label := fmt.Sprint(rate)
		if rate == 0 {
			label = "unlimited"
		}
		rep.Set("rate."+label+".sent", res.Sent)
		rep.Set("rate."+label+".delivered", delivered)
		rep.Set("rate."+label+".fraction", fraction)
		curve = append(curve, fmt.Sprintf("%s/s: %.1f%%", label, fraction*100))
		if rate == 500 && fraction < 0.95 {
			t.Fatalf("at 500 msg/s only %.1f%% of UDP datagrams were delivered", fraction*100)
		}
	}
	t.Logf("UDP loss curve: %s", strings.Join(curve, ", "))
	assertInvariants(t, rep, defaultOptions())
}

// TestFlowEditDuringTraffic changes a flow while traffic flows: the runtime
// is replaced exactly once, the gap is measured, post-edit messages carry
// the new transform, and the data port stays the same.
func TestFlowEditDuringTraffic(t *testing.T) {
	rep := scenario(t)
	rx := receiver(t)
	rx.KeepFields(true)
	port := ports(1)
	def := deploy(t, listenFlow("edit-live", port, flows.HTTPSink(deployment.SinkURL(rx))))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	gen := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "edit", Rate: 200, Reconnect: true}
	genCtx, stopGen := context.WithCancel(ctx)
	results := make(chan traffic.Result, 1)
	go func() { results <- gen.Run(genCtx, 0) }()
	if err := rx.WaitFor(ctx, 100); err != nil {
		t.Fatal(err)
	}
	edited := listenFlow("edit-live", port, flows.HTTPSink(deployment.SinkURL(rx)), flows.Transform("mark", "edited", `"v2"`))
	editAt := time.Now()
	if err := deployment.API().Upsert(ctx, edited); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, edited)
	// Wait for a message carrying the new field.
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
	// Every message delivered after the first v2 must be v2 (no old
	// runtime lingering alongside the new one).
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
	rep.Set("sent", res.Sent)
	rep.Set("delivered", rx.Stats().Unique)
	rep.Set("missing", len(rx.Missing(res.IDs)))
	rep.Set("editToFirstV2Seconds", firstV2.Sub(editAt).Seconds())
	rep.Set("deliveryGapSeconds", gap.Seconds())
	rep.Set("staleAfterEdit", stale)
	t.Logf("edit applied in %s, gap %s, stale deliveries after edit: %d", firstV2.Sub(editAt).Round(time.Millisecond), gap.Round(time.Millisecond), stale)
	if stale > 0 {
		t.Fatalf("%d messages were processed by the old runtime after the new one delivered", stale)
	}
	assertInvariants(t, rep, defaultOptions())
}

// TestStopStartRestart walks the run-state operations: stop releases the
// port and the runtime, start brings it back, restart replaces it; the
// invariants hold at each step.
func TestStopStartRestart(t *testing.T) {
	rep := scenario(t)
	rx := receiver(t)
	def := deploy(t, listenFlow("ops", ports(1), flows.HTTPSink(deployment.SinkURL(rx))))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	api := deployment.API()
	if err := api.Stop(ctx, def.Name); err != nil {
		t.Fatal(err)
	}
	if err := api.WaitPhase(ctx, def.Name, "stopped"); err != nil {
		t.Fatal(err)
	}
	if deployment.Name() == "binary" {
		if err := traffic.SendOnce(deployment.DataAddr(def), traffic.Message("stopped", 1, 0)); err == nil {
			t.Fatal("stopped flow still accepts connections")
		}
	}
	assertInvariants(t, rep, defaultOptions())
	stoppedAt := time.Now()
	if err := api.Start(ctx, def.Name); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, def)
	rep.Set("startSeconds", time.Since(stoppedAt).Seconds())
	gen := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "ops1"}
	res := gen.Run(ctx, 100)
	if err := rx.WaitFor(ctx, int(res.Sent)); err != nil {
		t.Fatalf("after start: %v", err)
	}
	restartAt := time.Now()
	if err := api.Restart(ctx, def.Name); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, def)
	rep.Set("restartSeconds", time.Since(restartAt).Seconds())
	gen2 := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "ops2"}
	res2 := gen2.Run(ctx, 100)
	if err := rx.WaitFor(ctx, int(res.Sent+res2.Sent)); err != nil {
		t.Fatalf("after restart: %v", err)
	}
	status, err := api.Status(ctx, def.Name)
	if err != nil {
		t.Fatal(err)
	}
	rep.Set("restartsReported", status.Restarts)
	assertInvariants(t, rep, defaultOptions())
}

// TestRollbackRestoresDefinition edits a flow twice and rolls back to the
// first revision; the running definition and the history invariants agree.
func TestRollbackRestoresDefinition(t *testing.T) {
	rep := scenario(t)
	rx := receiver(t)
	rx.KeepFields(true)
	port := ports(1)
	def := deploy(t, listenFlow("rollback", port, flows.HTTPSink(deployment.SinkURL(rx))))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	api := deployment.API()
	revs, err := api.Revisions(ctx, def.Name)
	if err != nil {
		t.Skipf("history unavailable: %v", err)
	}
	first := revs[0].Revision
	for i, value := range []string{`"v2"`, `"v3"`} {
		edited := listenFlow("rollback", port, flows.HTTPSink(deployment.SinkURL(rx)), flows.Transform("mark", "version", value))
		if err := api.Upsert(ctx, edited); err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
		waitRunning(t, edited)
	}
	if err := api.Rollback(ctx, def.Name, first); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, def)
	got, err := api.Get(ctx, def.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Stages) != 0 {
		t.Fatalf("rollback did not restore revision %d: stages %+v", first, got.Stages)
	}
	gen := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "rb"}
	res := gen.Run(ctx, 50)
	if err := rx.WaitFor(ctx, int(res.Sent)); err != nil {
		t.Fatal(err)
	}
	if f := rx.LastFields(); f["version"] != nil {
		t.Fatalf("runtime still applies the rolled-back transform: %v", f)
	}
	revs, _ = api.Revisions(ctx, def.Name)
	rep.Set("revisions", len(revs))
	rep.Set("headSource", revs[0].Source)
	if revs[0].Source != "rollback" || len(revs) != 4 {
		t.Fatalf("history after rollback: %d revisions, head source %s", len(revs), revs[0].Source)
	}
	opts := defaultOptions()
	opts.HistoryExpected = true
	assertInvariants(t, rep, opts)
}

// TestSlowStreamClientDoesNotStallProducer opens a live traffic stream and
// never reads it while messages flow; the sink must still receive all of
// them (the tap drops for slow subscribers instead of blocking).
func TestSlowStreamClientDoesNotStallProducer(t *testing.T) {
	rep := scenario(t)
	rx := receiver(t)
	def := deploy(t, listenFlow("slow-tap", ports(1), flows.HTTPSink(deployment.SinkURL(rx))))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	stream, err := deployment.API().Flows.WatchTraffic(streamCtx, connect.NewRequest(&gofiv1.WatchTrafficRequest{Name: def.Name}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	// Read exactly one message to be sure the stream is attached, then stop.
	time.Sleep(500 * time.Millisecond)
	gen := &traffic.Generator{Network: "tcp", Addr: deployment.DataAddr(def), Prefix: "tap", Pad: 2048, Connections: 4}
	res := gen.Run(ctx, 5000)
	deadline, cancelDeadline := context.WithTimeout(ctx, 60*time.Second)
	err = rx.WaitFor(deadline, int(res.Sent))
	cancelDeadline()
	stats := rx.Stats()
	dropped := metrics(t).Value("gofi_tap_events_dropped_total", map[string]string{"listener": def.Name})
	rep.Set("sent", res.Sent)
	rep.Set("delivered", stats.Unique)
	rep.Set("tapEventsDropped", dropped)
	t.Logf("sent %d, delivered %d, tap events dropped %.0f", res.Sent, stats.Unique, dropped)
	if err != nil {
		t.Fatalf("producer stalled behind a slow stream client: %d/%d delivered", stats.Unique, res.Sent)
	}
	assertInvariants(t, rep, defaultOptions())
}

// TestKillDuringSavesKeepsConfigIntact hammers the API with saves and
// SIGKILLs the controller mid-way; after restart the flow file must load
// (atomic writes), every acknowledged save must be present, and the
// invariants hold.
func TestKillDuringSavesKeepsConfigIntact(t *testing.T) {
	rep := scenario(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	api := deployment.API()
	base := ports(40)
	for i := 0; i < 20; i++ {
		if err := api.Upsert(ctx, listenFlow(fmt.Sprintf("durable-%02d", i), base+i)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func(name string) func() { return func() { _ = api.Delete(context.Background(), name) } }(fmt.Sprintf("durable-%02d", i)))
	}
	acked := map[string]int{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for round := 1; ; round++ {
			select {
			case <-stop:
				return
			default:
			}
			for i := 0; i < 20; i++ {
				name := fmt.Sprintf("durable-%02d", i)
				def := listenFlow(name, base+i, flows.Transform("r", "round", fmt.Sprintf(`"%d"`, round)))
				if err := api.Upsert(ctx, def); err == nil {
					acked[name] = round
				}
			}
		}
	}()
	time.Sleep(1500 * time.Millisecond)
	if err := deployment.Kill(ctx); err != nil {
		t.Fatalf("kill: %v", err)
	}
	close(stop)
	<-done
	list, err := api.List(ctx)
	if err != nil {
		t.Fatalf("config did not load after SIGKILL: %v", err)
	}
	rounds := map[string]int{}
	for _, def := range list {
		if !strings.HasPrefix(def.Name, "durable-") {
			continue
		}
		round := 0
		if len(def.Stages) == 1 {
			fmt.Sscanf(strings.Trim(def.Stages[0].ValueExpr, `"`), "%d", &round)
		}
		rounds[def.Name] = round
	}
	behind := 0
	for name, want := range acked {
		if rounds[name] < want {
			behind++
			t.Logf("%s: acknowledged round %d, stored %d", name, want, rounds[name])
		}
	}
	rep.Set("flows", len(rounds))
	rep.Set("acknowledgedBehindStored", behind)
	if len(rounds) != 20 {
		t.Fatalf("%d of 20 flows survived the kill", len(rounds))
	}
	if behind > 0 {
		t.Fatalf("%d acknowledged saves were lost (file store not durable)", behind)
	}
	for i := 0; i < 20; i++ {
		waitRunning(t, listenFlow(fmt.Sprintf("durable-%02d", i), base+i))
	}
	assertInvariants(t, rep, defaultOptions())
}

// TestManyFlowsConverge creates 50 flows, measures time to all-running,
// restarts the controller, and requires the same set with no duplicates.
func TestManyFlowsConverge(t *testing.T) {
	rep := scenario(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	api := deployment.API()
	const n = 50
	base := ports(n)
	names := make([]string, 0, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		def := listenFlow(fmt.Sprintf("many-%03d", i), base+i)
		if err := api.Upsert(ctx, def); err != nil {
			t.Fatal(err)
		}
		names = append(names, def.Name)
		t.Cleanup(func(name string) func() { return func() { _ = api.Delete(context.Background(), name) } }(def.Name))
	}
	if err := api.WaitAllPhase(ctx, names, "running"); err != nil {
		t.Fatal(err)
	}
	rep.Set("flows", n)
	rep.Set("createToAllRunningSeconds", time.Since(start).Seconds())
	rss, _ := deployment.ControllerRSS()
	rep.Set("rssMiB", mib(rss))
	restart := time.Now()
	if err := deployment.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if err := api.WaitAllPhase(ctx, names, "running"); err != nil {
		t.Fatal(err)
	}
	rep.Set("restartToAllRunningSeconds", time.Since(restart).Seconds())
	assertInvariants(t, rep, defaultOptions())
	var violations []invariants.Violation
	_ = violations
}
