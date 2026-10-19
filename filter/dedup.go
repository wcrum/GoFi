package filter

import (
	"fmt"
	"hash/maphash"
	"sync"
	"time"

	"cel.dev/cel-go/cel"

	"github.com/wcrum/gofi/config"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

const defaultDedupWindow = 30 * time.Second

// Dedup bounds. Every stage keeps at most config.Limits.DedupMaxKeys live
// keys (GOFI_DEDUP_MAX_KEYS) across its shards: when a window is long and
// keys are many, the oldest keys are forgotten early rather than growing
// memory without limit. The bound is per stage and per replica (each runtime
// has its own table; see docs/flows.md "Running more than one replica").
const (
	// dedupShards splits the key space so concurrent connections rarely
	// contend on one lock.
	dedupShards = 16
)

// dedupMaxKeys reads the live-key cap once.
var dedupMaxKeys = sync.OnceValue(func() int {
	if n := config.Get().Limits.DedupMaxKeys; n > 0 {
		return n
	}
	return 100_000
})

// dedupStage drops messages whose KeyExpr value was already seen within the
// last Window. Like rateLimitStage, its table is listener-wide shared
// state — it relies on the Chain (and therefore this stage) being built
// once per listener and reused across every connection.
//
// Expiry is O(1) amortized: every key lives exactly Window, so insertion
// order is expiry order and each shard keeps a FIFO of (key, expiry) next to
// its map. A lookup pops expired entries from the front of that queue
// instead of sweeping the whole map per message.
type dedupStage struct {
	program cel.Program
	window  time.Duration
	maxKeys int
	seed    maphash.Seed
	shards  [dedupShards]dedupShard
	now     func() time.Time // for tests
}

type dedupShard struct {
	mu    sync.Mutex
	seen  map[string]int64 // key -> expiry (unix nanoseconds)
	queue []dedupEntry     // insertion (= expiry) order
}

type dedupEntry struct {
	key    string
	expiry int64
}

func newDedup(cfg spec.StageSpec) (Stage, error) {
	if cfg.KeyExpr == "" {
		return nil, fmt.Errorf("dedup: keyExpr is required")
	}
	prog, err := CompileCEL(cfg.KeyExpr)
	if err != nil {
		return nil, fmt.Errorf("dedup: compile keyExpr %q: %w", cfg.KeyExpr, err)
	}

	window := defaultDedupWindow
	if cfg.Window != "" {
		w, err := time.ParseDuration(cfg.Window)
		if err != nil {
			return nil, fmt.Errorf("dedup: invalid window %q: %w", cfg.Window, err)
		}
		window = w
	}
	d := &dedupStage{program: prog, window: window, maxKeys: dedupMaxKeys(), seed: maphash.MakeSeed(), now: time.Now}
	for i := range d.shards {
		d.shards[i].seen = make(map[string]int64)
	}
	return d, nil
}

func (d *dedupStage) Process(msg *message.Message) (bool, error) {
	val, err := EvalCEL(d.program, msg)
	if err != nil {
		// Fail open: pass the message through unchanged rather than dedup it.
		return true, fmt.Errorf("dedup: eval keyExpr: %w", err)
	}
	return d.firstSeen(valToString(val)), nil
}

// firstSeen records key and reports whether it was not live before.
func (d *dedupStage) firstSeen(key string) bool {
	now := d.now().UnixNano()
	shard := &d.shards[maphash.String(d.seed, key)%dedupShards]
	shardCap := d.maxKeys / dedupShards
	if shardCap < 1 {
		shardCap = 1
	}

	shard.mu.Lock()
	defer shard.mu.Unlock()

	shard.expire(now)
	if expiry, ok := shard.seen[key]; ok && expiry > now {
		return false
	}
	for len(shard.seen) >= shardCap && len(shard.queue) > 0 {
		shard.evictOldest()
	}
	expiry := now + d.window.Nanoseconds()
	shard.seen[key] = expiry
	shard.queue = append(shard.queue, dedupEntry{key: key, expiry: expiry})
	return true
}

// expire drops entries whose window passed. The queue front holds the
// oldest expiry, so this stops at the first live entry.
func (s *dedupShard) expire(now int64) {
	i := 0
	for i < len(s.queue) && s.queue[i].expiry <= now {
		e := s.queue[i]
		// A key re-inserted after expiring has a newer expiry in the map;
		// only delete the map entry this queue entry created.
		if s.seen[e.key] == e.expiry {
			delete(s.seen, e.key)
		}
		i++
	}
	s.dropFront(i)
}

func (s *dedupShard) evictOldest() {
	e := s.queue[0]
	if s.seen[e.key] == e.expiry {
		delete(s.seen, e.key)
	}
	s.dropFront(1)
}

// dropFront removes n queue entries, compacting the slice once the consumed
// prefix dominates so the backing array does not grow forever.
func (s *dedupShard) dropFront(n int) {
	if n == 0 {
		return
	}
	for i := 0; i < n; i++ {
		s.queue[i] = dedupEntry{}
	}
	s.queue = s.queue[n:]
	if len(s.queue) == 0 {
		s.queue = s.queue[:0:0]
		return
	}
	if cap(s.queue) > 1024 && len(s.queue) < cap(s.queue)/4 {
		s.queue = append(make([]dedupEntry, 0, len(s.queue)*2), s.queue...)
	}
}

// liveKeys counts unexpired keys (for tests). Expiry is lazy per shard, so
// each shard is expired first.
func (d *dedupStage) liveKeys() int {
	now := d.now().UnixNano()
	n := 0
	for i := range d.shards {
		d.shards[i].mu.Lock()
		d.shards[i].expire(now)
		n += len(d.shards[i].seen)
		d.shards[i].mu.Unlock()
	}
	return n
}

func valToString(val interface{}) string {
	if s, ok := val.(string); ok {
		return s
	}
	return fmt.Sprint(val)
}
