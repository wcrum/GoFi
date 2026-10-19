package filter

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func BenchmarkDedup(b *testing.B) {
	for _, live := range []int{100, 10_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("live=%d", live), func(b *testing.B) {
			st, err := New(spec.StageSpec{ID: "d", Kind: "dedup", KeyExpr: `fields["id"]`, Window: "1h"})
			if err != nil {
				b.Fatal(err)
			}
			d := st.(*dedupStage)
			for i := 0; i < live; i++ {
				d.firstSeen("k" + strconv.Itoa(i))
			}
			m := message.New(nil, "bench", "r")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Fields["id"] = "n" + strconv.Itoa(i)
				st.Process(m)
			}
		})
	}
}

// Per-message cost must not grow with the number of live keys.
func TestDedupCostIndependentOfLiveKeys(t *testing.T) {
	timeFor := func(live int) time.Duration {
		st, _ := New(spec.StageSpec{ID: "d", Kind: "dedup", KeyExpr: `fields["id"]`, Window: "1h"})
		d := st.(*dedupStage)
		for i := 0; i < live; i++ {
			d.firstSeen("k" + strconv.Itoa(i))
		}
		const n = 20_000
		start := time.Now()
		for i := 0; i < n; i++ {
			d.firstSeen("n" + strconv.Itoa(i))
		}
		return time.Since(start) / n
	}
	small, large := timeFor(100), timeFor(200_000)
	// A linear sweep would be thousands of times slower here; allow for
	// cache effects and a noisy machine.
	if large > 20*small && large > 2*time.Microsecond {
		t.Fatalf("per-key cost grew from %v at 100 live keys to %v at 200k", small, large)
	}
}

func TestDedupExpiresInOrderAndCapsLiveKeys(t *testing.T) {
	st, _ := New(spec.StageSpec{ID: "d", Kind: "dedup", KeyExpr: `fields["id"]`, Window: "10s"})
	d := st.(*dedupStage)
	now := time.Unix(1_000_000, 0)
	d.now = func() time.Time { return now }
	d.maxKeys = 32 * dedupShards // 32 per shard

	for i := 0; i < 1000; i++ {
		if !d.firstSeen("k" + strconv.Itoa(i)) {
			t.Fatalf("fresh key %d reported as seen", i)
		}
	}
	if live := d.liveKeys(); live > d.maxKeys {
		t.Fatalf("%d live keys, cap %d", live, d.maxKeys)
	}
	// Recent keys are still deduplicated; the oldest were evicted.
	if d.firstSeen("k999") {
		t.Fatal("newest key forgotten under the cap")
	}
	if !d.firstSeen("k0") {
		t.Fatal("oldest key kept past the cap")
	}
	// Past the window everything expires, on the next lookup, in O(expired).
	now = now.Add(11 * time.Second)
	if !d.firstSeen("k999") {
		t.Fatal("key still live after the window")
	}
	if live := d.liveKeys(); live != 1 {
		t.Fatalf("%d live keys after expiry, want 1", live)
	}
	// A key re-inserted after expiring is not removed by its old queue entry.
	d.now = func() time.Time { return now }
	d.firstSeen("again")
	now = now.Add(11 * time.Second)
	d.firstSeen("again") // expires the old, inserts the new
	now = now.Add(5 * time.Second)
	if d.firstSeen("again") {
		t.Fatal("re-inserted key lost its window")
	}
}
