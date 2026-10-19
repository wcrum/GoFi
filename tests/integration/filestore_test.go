//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wcrum/gofi/atomicfile"
	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/history"
	"github.com/wcrum/gofi/internal/testkit/fakedriver"
	"github.com/wcrum/gofi/internal/testkit/flows"
	"github.com/wcrum/gofi/internal/testkit/invariants"
	"github.com/wcrum/gofi/spec"
)

// TestConvergenceFromFileStore starts a controller over N stored flows and
// checks that every flow gets exactly one runtime, with exactly N creates
// and no deletes, at 10, 100, and 500 flows.
func TestConvergenceFromFileStore(t *testing.T) {
	for _, n := range []int{10, 100, 500} {
		t.Run(fmt.Sprintf("%d-flows", n), func(t *testing.T) {
			rep := scenario(t, "integration")
			store, _ := fileStore(t)
			defs := flows.Many("flow", n, 20000)
			seedStart := time.Now()
			seed(t, store, defs)
			rep.Set("seedSeconds", time.Since(seedStart).Seconds())
			d := fakedriver.New("fake")
			start := time.Now()
			_, stop := startController(t, store, d)
			defer stop()
			waitFor(t, 60*time.Second, func() bool { return converged(d, defs) }, fmt.Sprintf("%d flows running", n))
			elapsed := time.Since(start)
			rep.Set("flows", n)
			rep.Set("convergeSeconds", elapsed.Seconds())
			verbs := d.Verbs()
			rep.Set("creates", verbs.Creates)
			rep.Set("deletes", verbs.Deletes)
			rep.Set("lists", verbs.Lists)
			if verbs.Creates != int64(n) || verbs.Deletes != 0 {
				t.Fatalf("expected exactly %d creates and 0 deletes, got %+v", n, verbs)
			}
			checkInvariants(t, store, d, nil)
			t.Logf("%d flows converged in %s (%s)", n, elapsed.Round(time.Millisecond), verbsString(verbs))
		})
	}
}

// TestSecondSyncWritesNothing restarts the controller over a converged
// world: the fresh controller's first sync (and every later resync) must
// not create or delete anything.
func TestSecondSyncWritesNothing(t *testing.T) {
	rep := scenario(t, "integration")
	store, _ := fileStore(t)
	defs := flows.Many("idem", 50, 21000, flows.Transform("tag", "seen", `"yes"`))
	defs = append(defs, flows.Listen("idem-stopped", 21999, flows.Stopped()))
	seed(t, store, defs)
	d := fakedriver.New("fake")
	_, stop := startController(t, store, d)
	waitFor(t, 30*time.Second, func() bool { return converged(d, defs) }, "first convergence")
	stop()

	d.ResetVerbs()
	_, stop = startController(t, store, d)
	defer stop()
	// Wait for the first sync to finish: List was called and the controller
	// had time to act on its result.
	waitFor(t, 10*time.Second, func() bool { return d.Verbs().Lists >= 1 }, "restarted controller to list runtimes")
	time.Sleep(300 * time.Millisecond)
	verbs := d.Verbs()
	rep.Set("writesAfterRestart", verbs.Writes())
	rep.Set("listsAfterRestart", verbs.Lists)
	if verbs.Writes() != 0 {
		t.Fatalf("idempotence violated: restarted controller issued %d writes (%s)\nhistory: %s", verbs.Writes(), verbsString(verbs), strings.Join(d.History(), ", "))
	}
	checkInvariants(t, store, d, nil)
}

// TestRestartRecoveryMidSync kills the controller while it is still creating
// runtimes (slow driver), restarts it over the same store and the same
// surviving runtimes (as Docker containers and Pods survive a controller
// restart), and requires convergence without a single duplicate create.
func TestRestartRecoveryMidSync(t *testing.T) {
	rep := scenario(t, "integration")
	store, _ := fileStore(t)
	const n = 120
	defs := flows.Many("recover", n, 22000)
	seed(t, store, defs)
	d := fakedriver.New("fake")
	d.Latency = 5 * time.Millisecond
	_, stop := startController(t, store, d)
	waitFor(t, 30*time.Second, func() bool { return len(d.Running()) >= n/3 }, "a third of the flows to start")
	stop() // the context cancels in-flight creates: the "kill"
	partial := len(d.Running())
	rep.Set("runningAtKill", partial)
	if partial >= n {
		t.Fatalf("controller finished before the kill; raise latency (running=%d)", partial)
	}
	d.Latency = 0
	_, stop = startController(t, store, d)
	defer stop()
	waitFor(t, 30*time.Second, func() bool { return converged(d, defs) }, "recovery after restart")
	successful := 0
	for _, entry := range d.History() {
		if strings.HasPrefix(entry, "create ") {
			successful++
		}
	}
	rep.Set("successfulCreates", successful)
	rep.Set("createAttempts", d.Verbs().Creates)
	if successful != n {
		t.Fatalf("expected exactly %d successful creates across both controller lives, got %d\n%s", n, successful, strings.Join(d.History(), "\n"))
	}
	if d.Verbs().Deletes != 0 {
		t.Fatalf("recovery deleted runtimes that matched the store: %s", verbsString(d.Verbs()))
	}
	checkInvariants(t, store, d, nil)
}

// TestConcurrentEditsDuringSync adds, removes, edits, stops, and starts
// flows through the controller while it converges a slow driver, then
// requires the store, the controller, and the runtimes to agree.
func TestConcurrentEditsDuringSync(t *testing.T) {
	rep := scenario(t, "integration")
	store, _ := fileStore(t)
	defs := flows.Many("edit", 60, 23000)
	seed(t, store, defs)
	d := fakedriver.New("fake")
	d.Latency = 2 * time.Millisecond
	ctrl, stop := startController(t, store, d)
	defer stop()

	var wg sync.WaitGroup
	errs := make(chan error, 200)
	wg.Add(1)
	go func() { // adds
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if err := ctrl.Add(flows.Listen(fmt.Sprintf("added-%02d", i), 23100+i)); err != nil {
				errs <- fmt.Errorf("add: %w", err)
			}
		}
	}()
	wg.Add(1)
	go func() { // removes
		defer wg.Done()
		for i := 0; i < 10; i++ {
			if err := ctrl.Remove(defs[i].Name); err != nil {
				errs <- fmt.Errorf("remove: %w", err)
			}
		}
	}()
	wg.Add(1)
	go func() { // edits (runtime-changing)
		defer wg.Done()
		for i := 10; i < 20; i++ {
			def := flows.Listen(defs[i].Name, defs[i].Source.Listen.Port, flows.Transform("edited", "edited", `"true"`))
			if err := ctrl.Add(def); err != nil {
				errs <- fmt.Errorf("edit: %w", err)
			}
		}
	}()
	wg.Add(1)
	go func() { // stop then start
		defer wg.Done()
		for i := 20; i < 25; i++ {
			if err := ctrl.SetState(defs[i].Name, spec.StateStopped); err != nil {
				errs <- fmt.Errorf("stop: %w", err)
			}
		}
		for i := 20; i < 23; i++ {
			if err := ctrl.SetState(defs[i].Name, spec.StateRunning); err != nil {
				errs <- fmt.Errorf("start: %w", err)
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	final, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(final) != 60-10+20 {
		t.Fatalf("store holds %d flows, want 70", len(final))
	}
	waitFor(t, 30*time.Second, func() bool { return converged(d, final) }, "convergence after concurrent edits")
	checkInvariants(t, store, d, nil)
	running := d.Running()
	for i := 10; i < 20; i++ {
		def := running[defs[i].Name]
		if len(def.Stages) != 1 || def.Stages[0].ID != "edited" {
			t.Fatalf("edited flow %s runs the old definition: %+v", defs[i].Name, def.Stages)
		}
	}
	for i := 23; i < 25; i++ {
		if _, ok := running[defs[i].Name]; ok {
			t.Fatalf("stopped flow %s still has a runtime", defs[i].Name)
		}
	}
	rep.Set("finalFlows", len(final))
	rep.Set("verbs", verbsString(d.Verbs()))
}

// TestStoreCorruptionKeepsLastGood corrupts the flow file while flows run.
// With an intact .bak the store recovers the last acknowledged save; with
// every copy corrupt the controller keeps its last good desired state and
// every runtime. Either way nothing converges toward garbage, and the
// controller applies the file again the moment it is valid (including a
// removal).
func TestStoreCorruptionKeepsLastGood(t *testing.T) {
	rep := scenario(t, "integration")
	store, _ := fileStore(t)
	defs := flows.Many("corrupt", 5, 24000)
	seed(t, store, defs)
	good, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	backup := store.Path + atomicfile.BackupSuffix
	previous := store.Path + atomicfile.PreviousSuffix
	// The backup is never behind an acknowledged save: after seeding it
	// equals the file, so a corrupted primary recovers every seeded flow.
	if bak, err := os.ReadFile(backup); err != nil || string(bak) != string(good) {
		t.Fatalf("backup differs from the last save: %v", err)
	}
	d := fakedriver.New("fake")
	ctrl, stop := startController(t, store, d)
	defer stop()
	waitFor(t, 10*time.Second, func() bool { return converged(d, defs) }, "initial convergence")

	garbage := map[string]string{"truncated": string(good[:len(good)/2]), "wrongKind": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n", "empty": ""}
	for _, withBackup := range []bool{true, false} {
		for name, content := range garbage {
			label := fmt.Sprintf("%s/backup=%v", name, withBackup)
			if withBackup {
				if bak, err := os.ReadFile(backup); err != nil || string(bak) != string(good) {
					t.Fatalf("%s: backup no longer holds the last save: %v", label, err)
				}
			} else {
				for _, copy := range []string{backup, previous} {
					if err := os.WriteFile(copy, []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := os.WriteFile(store.Path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			d.Crash(defs[0].Name) // a runtime dies while the store is unreadable
			// Restart triggers a sync (there is no public resync hook); the
			// controller must retain desired state and repair the runtime.
			if err := ctrl.Restart(context.Background(), defs[1].Name); err != nil {
				t.Fatalf("%s: restart: %v", label, err)
			}
			waitFor(t, 10*time.Second, func() bool { return converged(d, defs) }, label+": runtimes repaired from the last good state")
			if got := len(ctrl.Flows()); got != len(defs) {
				t.Fatalf("%s: controller forgot flows during store corruption: %d", label, got)
			}
			if _, ok := ctrl.Get(defs[0].Name); !ok {
				t.Fatalf("%s: lost flow", label)
			}
			// Saves are refused while nothing valid can be read, rather than
			// overwriting whatever is left with a partial flow set.
			if !withBackup {
				if err := ctrl.Add(flows.Listen("during-outage", 24900)); err == nil {
					t.Fatalf("%s: save succeeded on an unreadable store", label)
				}
			}
		}
	}
	// A valid file that drops one flow is applied on the next sync.
	if err := os.WriteFile(store.Path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), defs[4].Name); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Restart(context.Background(), defs[1].Name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool { return converged(d, defs[:4]) }, "removal applied after recovery")
	checkInvariants(t, store, d, nil)
	rep.Set("verbs", verbsString(d.Verbs()))
}

// TestInvalidFlowKeepsLastGoodVersion writes an invalid edit of a running
// flow straight into the store (as kubectl edit or a hand edit would): the
// old version keeps running, the flow is reported invalid, other flows
// are unaffected, and fixing the file applies the new version.
func TestInvalidFlowKeepsLastGoodVersion(t *testing.T) {
	store, _ := fileStore(t)
	defs := flows.Many("invalid", 3, 25000)
	seed(t, store, defs)
	d := fakedriver.New("fake")
	ctrl, stop := startController(t, store, d)
	defer stop()
	waitFor(t, 10*time.Second, func() bool { return converged(d, defs) }, "initial convergence")

	broken := defs[0]
	broken.Sinks = []spec.SinkCfg{{Type: "stdout", When: "not valid cel ((("}}
	if err := store.Save(context.Background(), broken); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Restart(context.Background(), defs[1].Name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		info, found, _ := ctrl.RuntimeStatus(context.Background(), defs[0].Name)
		return found && strings.Contains(info.Error, "invalid")
	}, "invalid flow reported")
	if def := d.Running()[defs[0].Name]; len(def.Sinks) != 1 || def.Sinks[0].When != "" {
		t.Fatalf("invalid edit reached the runtime: %+v", def.Sinks)
	}
	// Flows() excludes invalid flows; List includes them.
	if len(ctrl.Flows()) != 2 || len(ctrl.List()) != 3 {
		t.Fatalf("flow sets: valid=%d listed=%d", len(ctrl.Flows()), len(ctrl.List()))
	}
	fixed := defs[0]
	fixed.Sinks = []spec.SinkCfg{{Type: "stdout", When: `fields.ok == true`}}
	if err := ctrl.Add(fixed); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		def, ok := d.Running()[defs[0].Name]
		return ok && len(def.Sinks) == 1 && def.Sinks[0].When == `fields.ok == true`
	}, "fixed definition applied")
	checkInvariants(t, store, d, nil)
}

// TestOrphanRuntimeIsRemoved plants a runtime nobody declared (a container
// left over from an earlier deployment) and expects the controller to
// remove it on its next sync.
func TestOrphanRuntimeIsRemoved(t *testing.T) {
	store, _ := fileStore(t)
	defs := flows.Many("owned", 3, 26000)
	seed(t, store, defs)
	d := fakedriver.New("fake")
	d.Orphan(flows.Listen("leftover", 26999))
	_, stop := startController(t, store, d)
	defer stop()
	waitFor(t, 10*time.Second, func() bool { return converged(d, defs) }, "orphan removed and flows running")
	checkInvariants(t, store, d, nil)
	found := false
	for _, line := range d.History() {
		found = found || line == "delete leftover"
	}
	if !found {
		t.Fatalf("orphan was not deleted: %v", d.History())
	}
}

// TestHistoryRecordsAndRollsBack drives saves through the recorder (as the
// API does) and checks revision numbering, hashes, external-change
// detection, rollback, and the history invariants from the flow file.
func TestHistoryRecordsAndRollsBack(t *testing.T) {
	store, dir := fileStore(t)
	d := fakedriver.New("fake")
	ctrl, stop := startController(t, store, d)
	defer stop()
	backend := &history.File{Dir: filepath.Join(dir, "history")}
	rec := history.NewRecorder(backend, ctrl, history.Options{Limit: 5})
	ctx := context.Background()

	def := flows.Listen("versioned", 27000)
	apply := func(def spec.FlowDefinition, message string) history.Revision {
		rev, created, err := rec.Apply(ctx, def.Name, history.Meta{Source: history.SourcePortal, Author: "test", Message: message}, func() error { return ctrl.Add(def) })
		if err != nil {
			t.Fatalf("apply %s: %v", message, err)
		}
		if !created {
			t.Fatalf("apply %s: no revision created", message)
		}
		return rev
	}
	r1 := apply(def, "create")
	edited := flows.Listen("versioned", 27000, flows.Transform("t1", "a", `"1"`))
	r2 := apply(edited, "add transform")
	if r1.Number != 1 || r2.Number != 2 {
		t.Fatalf("revisions %d, %d", r1.Number, r2.Number)
	}
	// Saving the same content again records nothing.
	if _, created, _ := rec.Apply(ctx, def.Name, history.Meta{Source: history.SourcePortal}, func() error { return ctrl.Add(edited) }); created {
		t.Fatal("unchanged save created a revision")
	}
	// An external edit (hand-edited file) shows up on Sync as "external".
	external := flows.Listen("versioned", 27000, flows.Transform("t1", "a", `"2"`))
	if err := store.Save(ctx, external); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Restart(ctx, "versioned"); err != nil { // reload the store
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		got, _ := ctrl.Get("versioned")
		return len(got.Stages) == 1 && got.Stages[0].ValueExpr == `"2"`
	}, "external edit loaded")
	if err := rec.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	revs, err := backend.List(ctx, "versioned")
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 3 || revs[2].Source != history.SourceExternal {
		t.Fatalf("external change not recorded: %+v", summarize(revs))
	}
	// Rollback to revision 1 restores the original definition as revision 4.
	rev, created, err := rec.Rollback(ctx, "versioned", 1, history.Meta{Author: "test"}, func(d spec.FlowDefinition) error { return ctrl.Add(d) })
	if err != nil || !created || rev.Number != 4 || rev.Source != history.SourceRollback {
		t.Fatalf("rollback: %+v created=%v err=%v", rev, created, err)
	}
	got, _ := ctrl.Get("versioned")
	if len(got.Stages) != 0 {
		t.Fatalf("rollback did not restore revision 1: %+v", got.Stages)
	}
	waitFor(t, 10*time.Second, func() bool { return len(d.Running()["versioned"].Stages) == 0 }, "rolled-back definition running")
	// Retention: more saves than the limit prune the oldest.
	for i := 0; i < 5; i++ {
		apply(flows.Listen("versioned", 27000, flows.Transform("t", "n", fmt.Sprintf(`"%d"`, i))), "churn")
	}
	revs, _ = backend.List(ctx, "versioned")
	if len(revs) != 5 || revs[0].Number != 5 || revs[4].Number != 9 {
		t.Fatalf("retention: %+v", summarize(revs))
	}
	// Deleting records a tombstone; the history invariant accepts it.
	if _, _, err := rec.Apply(ctx, "versioned", history.Meta{Source: history.SourcePortal}, func() error { return ctrl.Remove("versioned") }); err != nil {
		t.Fatal(err)
	}
	w, err := invariants.ObserveFile(ctx, store.Path, backend.Dir)
	if err != nil {
		t.Fatal(err)
	}
	w.Runtimes = runtimesOf(d)
	waitFor(t, 10*time.Second, func() bool { return len(d.Running()) == 0 }, "runtime removed")
	w.Runtimes = runtimesOf(d)
	if v := invariants.Check(w, invariants.Options{RuntimesExpected: true, HistoryExpected: true}); len(v) > 0 {
		t.Fatalf("%s", invariants.Summary(v))
	}
	head := w.History["versioned"][len(w.History["versioned"])-1]
	if !head.Deleted {
		t.Fatalf("no tombstone: %+v", head)
	}
}

// TestFileStoreAtomicWrites checks the flow file is replaced atomically: a
// reader racing hundreds of saves never sees a partial document.
func TestFileStoreAtomicWrites(t *testing.T) {
	store, _ := fileStore(t)
	ctx := context.Background()
	seed(t, store, flows.Many("atomic", 20, 28000))
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var torn int
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := (&desired.File{Path: store.Path}).Load(ctx); err != nil && !os.IsNotExist(errors.Unwrap(err)) {
				torn++
			}
		}
	}()
	for i := 0; i < 300; i++ {
		def := flows.Listen(fmt.Sprintf("atomic-%04d", i%20), 28000+i%20, flows.Transform("t", "i", fmt.Sprintf(`"%d"`, i)))
		if err := store.Save(ctx, def); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if torn > 0 {
		t.Fatalf("reader saw %d torn or invalid snapshots", torn)
	}
	entries, _ := os.ReadDir(filepath.Dir(store.Path))
	for _, entry := range entries {
		if name := entry.Name(); name != "listeners.yaml" && name != "listeners.yaml"+atomicfile.BackupSuffix && name != "listeners.yaml"+atomicfile.PreviousSuffix {
			t.Fatalf("temp file left behind: %s", name)
		}
	}
	// The backup always equals the last acknowledged save.
	primary, _ := os.ReadFile(store.Path)
	if bak, _ := os.ReadFile(store.Path + atomicfile.BackupSuffix); string(bak) != string(primary) {
		t.Fatal("backup differs from the last save")
	}
}

func verbsString(v fakedriver.Verbs) string {
	return fmt.Sprintf("creates=%d deletes=%d lists=%d status=%d flowsets=%d", v.Creates, v.Deletes, v.Lists, v.Status, v.FlowSets)
}

func summarize(revs []history.Revision) []string {
	out := make([]string, 0, len(revs))
	for _, r := range revs {
		out = append(out, fmt.Sprintf("%d:%s:%v", r.Number, r.Source, r.Deleted))
	}
	return out
}
