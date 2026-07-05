package filter

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestNewDedup_RequiresKeyExpr(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "d1", Kind: "dedup"})
	if err == nil {
		t.Fatal("expected error for missing keyExpr, got nil")
	}
}

func TestNewDedup_InvalidKeyExpr(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "d1", Kind: "dedup", KeyExpr: "not valid ["})
	if err == nil {
		t.Fatal("expected error for invalid keyExpr, got nil")
	}
}

func TestNewDedup_InvalidWindow(t *testing.T) {
	_, err := New(spec.StageSpec{ID: "d1", Kind: "dedup", KeyExpr: `fields["id"]`, Window: "not-a-duration"})
	if err == nil {
		t.Fatal("expected error for invalid window, got nil")
	}
}

func TestNewDedup_DefaultWindow(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "d1", Kind: "dedup", KeyExpr: `fields["id"]`})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	d := st.(*dedupStage)
	if d.window != defaultDedupWindow {
		t.Fatalf("window = %v, want default %v", d.window, defaultDedupWindow)
	}
}

func TestDedup_FirstSeenPassesRepeatDrops(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "d1", Kind: "dedup", KeyExpr: `fields["id"]`, Window: "1s"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	msg1 := message.New(nil, "l", "r")
	msg1.Fields["id"] = "abc"

	pass, err := st.Process(msg1)
	if err != nil || !pass {
		t.Fatalf("first message: pass=%v err=%v, want true, nil", pass, err)
	}

	msg2 := message.New(nil, "l", "r")
	msg2.Fields["id"] = "abc"
	pass, err = st.Process(msg2)
	if err != nil {
		t.Fatalf("second message err = %v", err)
	}
	if pass {
		t.Fatal("repeat within window should have been dropped")
	}
}

func TestDedup_PassesAgainAfterWindow(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "d1", Kind: "dedup", KeyExpr: `fields["id"]`, Window: "500ms"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	msg := message.New(nil, "l", "r")
	msg.Fields["id"] = "abc"

	pass, _ := st.Process(msg)
	if !pass {
		t.Fatal("first message should pass")
	}

	time.Sleep(600 * time.Millisecond)

	pass, err = st.Process(msg)
	if err != nil || !pass {
		t.Fatalf("after window: pass=%v err=%v, want true, nil", pass, err)
	}
}

func TestDedup_DifferentKeysBothPass(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "d1", Kind: "dedup", KeyExpr: `fields["id"]`, Window: "1s"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	msgA := message.New(nil, "l", "r")
	msgA.Fields["id"] = "a"
	msgB := message.New(nil, "l", "r")
	msgB.Fields["id"] = "b"

	passA, _ := st.Process(msgA)
	passB, _ := st.Process(msgB)
	if !passA || !passB {
		t.Fatalf("distinct keys should both pass, got passA=%v passB=%v", passA, passB)
	}
}

func TestDedup_NonStringKeyIsFormatted(t *testing.T) {
	st, err := New(spec.StageSpec{ID: "d1", Kind: "dedup", KeyExpr: `fields["n"]`, Window: "1s"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	msg1 := message.New(nil, "l", "r")
	msg1.Fields["n"] = 42.0
	msg2 := message.New(nil, "l", "r")
	msg2.Fields["n"] = 42.0

	pass1, err := st.Process(msg1)
	if err != nil || !pass1 {
		t.Fatalf("first: pass=%v err=%v", pass1, err)
	}
	pass2, err := st.Process(msg2)
	if err != nil {
		t.Fatalf("second err = %v", err)
	}
	if pass2 {
		t.Fatal("same numeric key formatted identically should dedup")
	}
}

// TestDedup_ConcurrentAccess fires many goroutines with a mix of distinct
// and repeated keys at a single shared dedupStage (as it would be shared
// listener-wide across connections, per the doc comment on dedupStage) and
// confirms, under -race, that there's no data race on the mutex-guarded
// `seen` map, and that for each distinct key exactly one of the concurrent
// callers observed it as "first seen" (pass=true) — i.e. dedup counting is
// exact even under concurrent access, not just race-free.
func TestDedup_ConcurrentAccess(t *testing.T) {
	const numKeys = 20
	const repeatsPerKey = 10

	st, err := New(spec.StageSpec{ID: "d1", Kind: "dedup", KeyExpr: `fields["id"]`, Window: "10s"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	var passedPerKey [numKeys]int64
	var wg sync.WaitGroup

	for k := 0; k < numKeys; k++ {
		key := strconv.Itoa(k)
		for r := 0; r < repeatsPerKey; r++ {
			wg.Add(1)
			go func(k int, key string) {
				defer wg.Done()
				msg := message.New(nil, "l", "r")
				msg.Fields["id"] = key
				pass, err := st.Process(msg)
				if err != nil {
					t.Errorf("Process error = %v, want nil", err)
				}
				if pass {
					atomic.AddInt64(&passedPerKey[k], 1)
				}
			}(k, key)
		}
	}
	wg.Wait()

	for k := 0; k < numKeys; k++ {
		if passedPerKey[k] != 1 {
			t.Fatalf("key %d: passed count = %d, want exactly 1 (first-seen) out of %d concurrent duplicates", k, passedPerKey[k], repeatsPerKey)
		}
	}
}
