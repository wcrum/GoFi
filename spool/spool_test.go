package spool

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

func open(t *testing.T, dir string, o Options) *Queue {
	t.Helper()
	q, err := Open(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	return q
}

func drain(t *testing.T, q *Queue) []string {
	t.Helper()
	var out []string
	for {
		recs, err := q.Peek(10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) == 0 {
			return out
		}
		for _, r := range recs {
			out = append(out, string(r.Data))
		}
		if err := q.Ack(recs[len(recs)-1]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFIFOAndAck(t *testing.T) {
	q := open(t, t.TempDir(), Options{})
	for i := 0; i < 25; i++ {
		if err := q.Push([]byte(fmt.Sprintf("m%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if q.Pending() != 25 {
		t.Fatalf("pending = %d", q.Pending())
	}
	recs, _ := q.Peek(5, 0)
	again, _ := q.Peek(5, 0)
	if len(recs) != 5 || string(recs[0].Data) != "m00" || string(again[4].Data) != "m04" {
		t.Fatalf("peek is not idempotent: %v %v", recs, again)
	}
	if err := q.Ack(recs[2]); err != nil {
		t.Fatal(err)
	}
	if q.Pending() != 22 {
		t.Fatalf("pending after partial ack = %d", q.Pending())
	}
	got := drain(t, q)
	if len(got) != 22 || got[0] != "m03" || got[21] != "m24" {
		t.Fatalf("drain = %v", got)
	}
	if q.Pending() != 0 {
		t.Fatalf("pending = %d after drain", q.Pending())
	}
}

func TestPeekLimits(t *testing.T) {
	q := open(t, t.TempDir(), Options{})
	for i := 0; i < 10; i++ {
		_ = q.Push(bytes.Repeat([]byte{'x'}, 100))
	}
	recs, _ := q.Peek(100, 250)
	if len(recs) != 2 {
		t.Fatalf("byte-bounded peek returned %d", len(recs))
	}
	recs, _ = q.Peek(100, 10) // smaller than one record: still returns one
	if len(recs) != 1 {
		t.Fatalf("peek must return at least one record, got %d", len(recs))
	}
}

func TestSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir, Options{SegmentBytes: 256, MaxBytes: 1 << 20})
	for i := 0; i < 40; i++ {
		if err := q.Push([]byte(fmt.Sprintf("rec-%03d", i))); err != nil {
			t.Fatal(err)
		}
	}
	recs, _ := q.Peek(7, 0)
	if err := q.Ack(recs[6]); err != nil {
		t.Fatal(err)
	}
	// Peek but do not ack: these must be redelivered.
	if more, _ := q.Peek(5, 0); len(more) != 5 {
		t.Fatal("expected 5 more")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}

	q2 := open(t, dir, Options{SegmentBytes: 256, MaxBytes: 1 << 20})
	if q2.Pending() != 33 {
		t.Fatalf("pending after reopen = %d, want 33", q2.Pending())
	}
	got := drain(t, q2)
	if len(got) != 33 || got[0] != "rec-007" || got[32] != "rec-039" {
		t.Fatalf("drain after reopen: %d %v", len(got), got[:3])
	}
	// Push after reopen continues in the active segment.
	if err := q2.Push([]byte("after")); err != nil {
		t.Fatal(err)
	}
	if got := drain(t, q2); len(got) != 1 || got[0] != "after" {
		t.Fatalf("after reopen push: %v", got)
	}
}

func TestTornTailIsTruncated(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir, Options{})
	for i := 0; i < 3; i++ {
		_ = q.Push([]byte(fmt.Sprintf("ok%d", i)))
	}
	_ = q.Close()

	// Simulate power loss mid-append: a partial header + payload.
	segs, _ := filepath.Glob(filepath.Join(dir, "seg-*.log"))
	f, err := os.OpenFile(segs[0], os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0x05, 0x00, 0x00, 0x00, 0xde, 0xad}) // 6 of 8 header bytes
	f.Close()
	before, _ := os.Stat(segs[0])

	q2 := open(t, dir, Options{})
	after, _ := os.Stat(segs[0])
	if after.Size() != before.Size()-6 {
		t.Fatalf("torn tail not truncated: %d -> %d", before.Size(), after.Size())
	}
	if got := drain(t, q2); len(got) != 3 || got[2] != "ok2" {
		t.Fatalf("records after truncation: %v", got)
	}
	// New pushes land cleanly after the truncated tail.
	if err := q2.Push([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if got := drain(t, q2); len(got) != 1 || got[0] != "new" {
		t.Fatalf("push after truncation: %v", got)
	}
}

func TestCorruptPayloadStopsAtLastGood(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir, Options{})
	_ = q.Push([]byte("good"))
	_ = q.Push([]byte("flipped"))
	_ = q.Close()
	segs, _ := filepath.Glob(filepath.Join(dir, "seg-*.log"))
	data, _ := os.ReadFile(segs[0])
	data[len(data)-1] ^= 0xff // corrupt last byte of the second payload
	if err := os.WriteFile(segs[0], data, 0o600); err != nil {
		t.Fatal(err)
	}
	q2 := open(t, dir, Options{})
	if got := drain(t, q2); len(got) != 1 || got[0] != "good" {
		t.Fatalf("got %v", got)
	}
}

func TestBoundedFullAndDropOldest(t *testing.T) {
	// Refusing mode.
	q := open(t, t.TempDir(), Options{MaxBytes: 4096, SegmentBytes: 1024})
	rec := bytes.Repeat([]byte{'a'}, 200)
	var pushed int
	for ; pushed < 1000; pushed++ {
		if err := q.Push(rec); err != nil {
			if !errors.Is(err, ErrFull) {
				t.Fatal(err)
			}
			break
		}
	}
	if pushed == 1000 || q.Bytes() > 4096 {
		t.Fatalf("spool exceeded bound: pushed=%d bytes=%d", pushed, q.Bytes())
	}
	if err := q.Push(bytes.Repeat([]byte{'z'}, 2000)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized record: %v", err)
	}
	// Acking frees space.
	recs, _ := q.Peek(100, 0)
	if err := q.Ack(recs[len(recs)-1]); err != nil {
		t.Fatal(err)
	}
	if err := q.Push(rec); err != nil {
		t.Fatalf("push after ack: %v", err)
	}

	// Drop-oldest mode keeps the newest records and counts the drops.
	d := open(t, t.TempDir(), Options{MaxBytes: 4096, SegmentBytes: 1024, DropOldest: true})
	for i := 0; i < 100; i++ {
		if err := d.Push([]byte(fmt.Sprintf("%03d-%s", i, strings.Repeat("b", 150)))); err != nil {
			t.Fatal(err)
		}
	}
	if d.Bytes() > 4096 {
		t.Fatalf("drop-oldest exceeded bound: %d", d.Bytes())
	}
	if d.Dropped == 0 {
		t.Fatal("expected drops")
	}
	got := drain(t, d)
	if len(got) == 0 || !strings.HasPrefix(got[len(got)-1], "099-") {
		t.Fatalf("newest record lost: %v", got)
	}
	sort.Strings(got)
	if !sort.StringsAreSorted(got) || uint64(len(got))+d.Dropped != 100 {
		t.Fatalf("accounting: kept=%d dropped=%d", len(got), d.Dropped)
	}
}

func TestSegmentsAreReclaimed(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir, Options{MaxBytes: 1 << 20, SegmentBytes: 512})
	for i := 0; i < 200; i++ {
		_ = q.Push(bytes.Repeat([]byte{'s'}, 60))
	}
	segs, _ := filepath.Glob(filepath.Join(dir, "seg-*.log"))
	if len(segs) < 10 {
		t.Fatalf("expected many segments, got %d", len(segs))
	}
	drain(t, q)
	segs, _ = filepath.Glob(filepath.Join(dir, "seg-*.log"))
	if len(segs) != 1 {
		t.Fatalf("consumed segments not deleted: %d remain", len(segs))
	}
	if q.Bytes() <= 0 {
		// The active segment keeps acked bytes until it rolls; that is fine,
		// but Bytes must stay consistent with the file on disk.
		t.Fatalf("bytes = %d", q.Bytes())
	}
	info, _ := os.Stat(segs[0])
	if info.Size() != q.Bytes() {
		t.Fatalf("bytes accounting drift: file=%d tracked=%d", info.Size(), q.Bytes())
	}
}

func TestConcurrentProducerConsumer(t *testing.T) {
	q := open(t, t.TempDir(), Options{MaxBytes: 8 << 20, SegmentBytes: 64 << 10, SyncInterval: 1e9})
	const n = 2000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := q.Push([]byte(fmt.Sprintf("%06d", i))); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	seen := 0
	last := -1
	for seen < n {
		recs, err := q.Peek(50, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) == 0 {
			continue
		}
		for _, r := range recs {
			var v int
			fmt.Sscanf(string(r.Data), "%d", &v)
			if v != last+1 {
				t.Fatalf("out of order: %d after %d", v, last)
			}
			last = v
		}
		seen += len(recs)
		if err := q.Ack(recs[len(recs)-1]); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if q.Pending() != 0 {
		t.Fatalf("pending = %d", q.Pending())
	}
}

func TestClosedQueue(t *testing.T) {
	q := open(t, t.TempDir(), Options{})
	_ = q.Close()
	if err := q.Push([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := q.Peek(1, 0); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
