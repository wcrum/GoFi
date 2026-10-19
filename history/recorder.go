package history

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/wcrum/gofi/spec"
)

// Flows is the view of current desired state the Recorder observes; the
// controller implements it. Get and List return stored definitions,
// including ones that failed validation, because those were saved too.
type Flows interface {
	Get(name string) (spec.FlowDefinition, bool)
	List() []spec.FlowDefinition
}

// Meta attributes a change. An empty Source means "observed": the first
// revision of a flow is SourceImport, later ones SourceExternal.
type Meta struct {
	Source  Source
	Author  string
	Message string
}

// Options tune a Recorder; zero values use the defaults.
type Options struct {
	// Limit is how many revisions each flow keeps (default DefaultLimit).
	Limit int
	// DeletedRetention is how long a deleted flow's history is kept after
	// its deletion (default DefaultDeletedRetention).
	DeletedRetention time.Duration
	// Now is the clock (tests).
	Now func() time.Time
}

const (
	DefaultLimit            = 50
	DefaultDeletedRetention = 30 * 24 * time.Hour
	purgeInterval           = time.Hour
)

// Recorder turns flow changes into revisions. Saves made through GoFi run
// inside Apply, which attributes the resulting change; Sync (run
// periodically) records anything else that changed the stored flows.
// Holding one lock across both means a portal save is never mistaken for an
// external edit.
type Recorder struct {
	store     Store
	flows     Flows
	limit     int
	retention time.Duration
	now       func() time.Time

	mu        sync.Mutex
	heads     map[string]head // latest revision per flow; number 0 = no history
	scanned   bool            // heads holds every flow with stored history
	lastPurge time.Time
	lastErr   string
}

type head struct {
	number  int64
	hash    string
	deleted bool
	at      time.Time
}

func NewRecorder(store Store, flows Flows, opts Options) *Recorder {
	r := &Recorder{store: store, flows: flows, limit: opts.Limit, retention: opts.DeletedRetention, now: opts.Now, heads: map[string]head{}}
	if r.limit <= 0 {
		r.limit = DefaultLimit
	}
	if r.retention <= 0 {
		r.retention = DefaultDeletedRetention
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r
}

// Store returns the backend (for the API's description of it).
func (r *Recorder) Store() Store { return r.store }

// Limit is how many revisions each flow keeps.
func (r *Recorder) Limit() int { return r.limit }

// Apply runs change (a save or delete through the controller) and records
// the flow's resulting state with meta. It returns change's error unchanged;
// a failure to record history after a successful change is logged, not
// returned, because the change itself took effect. created is false when
// the stored definition did not change (same content hash).
func (r *Recorder) Apply(ctx context.Context, flow string, meta Meta, change func() error) (rev Revision, created bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err = change(); err != nil {
		return Revision{}, false, err
	}
	rev, created, recErr := r.observeLocked(ctx, flow, meta)
	if recErr != nil {
		slog.Warn("history: revision not recorded", "flow", flow, "error", recErr)
		return Revision{}, false, nil
	}
	return rev, created, nil
}

// Rollback restores revision number of flow by saving its definition
// through apply (the controller's normal save path, so validation applies)
// and records the result as a SourceRollback revision. Restoring a deleted
// flow re-creates it.
func (r *Recorder) Rollback(ctx context.Context, flow string, number int64, meta Meta, apply func(spec.FlowDefinition) error) (Revision, bool, error) {
	target, err := r.store.Get(ctx, flow, number)
	if err != nil {
		return Revision{}, false, err
	}
	if target.Definition == nil {
		return Revision{}, false, fmt.Errorf("revision %d records the flow's deletion; restore an earlier revision", number)
	}
	def := *target.Definition
	def.Name = flow
	// Revisions carry no run state; a restored flow keeps its current one.
	if current, ok := r.flows.Get(flow); ok {
		def.State = current.State
	}
	meta.Source = SourceRollback
	if meta.Message == "" {
		meta.Message = fmt.Sprintf("Restore revision %d", number)
	}
	return r.Apply(ctx, flow, meta, func() error { return apply(def) })
}

// List returns up to limit (0 = all) revision summaries of flow, newest first.
func (r *Recorder) List(ctx context.Context, flow string, limit int) ([]Revision, error) {
	if err := validFlowName(flow); err != nil {
		return nil, err
	}
	revs, err := r.store.List(ctx, flow)
	if err != nil {
		return nil, err
	}
	out := make([]Revision, 0, len(revs))
	for i := len(revs) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, revs[i].Summary())
	}
	return out, nil
}

// Get returns one revision with its definition.
func (r *Recorder) Get(ctx context.Context, flow string, number int64) (Revision, error) {
	if err := validFlowName(flow); err != nil {
		return Revision{}, err
	}
	return r.store.Get(ctx, flow, number)
}

// Run calls Sync every interval until ctx ends.
func (r *Recorder) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.Sync(ctx); err != nil {
			if err.Error() != r.lastErr {
				slog.Warn("history: sync failed", "error", err)
			}
			r.lastErr = err.Error()
		} else {
			r.lastErr = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sync records every stored flow whose content changed since its latest
// revision (SourceImport for a flow without history, otherwise
// SourceExternal), a tombstone for every flow that disappeared, and purges
// deleted flows' history after the retention window.
func (r *Recorder) Sync(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var problems []error
	if !r.scanned {
		names, err := r.store.Flows(ctx)
		if err != nil {
			return err
		}
		for _, name := range names {
			if _, _, err := r.headLocked(ctx, name); err != nil {
				problems = append(problems, err)
			}
		}
		r.scanned = len(problems) == 0
	}
	current := map[string]bool{}
	for _, def := range r.flows.List() {
		if validFlowName(def.Name) != nil {
			continue
		}
		current[def.Name] = true
		if _, _, err := r.observeLocked(ctx, def.Name, Meta{}); err != nil {
			problems = append(problems, fmt.Errorf("flow %q: %w", def.Name, err))
		}
	}
	names := make([]string, 0, len(r.heads))
	for name := range r.heads {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if h := r.heads[name]; current[name] || h.number == 0 || h.deleted {
			continue
		}
		if _, _, err := r.observeLocked(ctx, name, Meta{}); err != nil {
			problems = append(problems, fmt.Errorf("flow %q: %w", name, err))
		}
	}
	if r.now().Sub(r.lastPurge) >= purgeInterval {
		if err := r.purgeLocked(ctx, current); err != nil {
			problems = append(problems, err)
		} else {
			r.lastPurge = r.now()
		}
	}
	return errors.Join(problems...)
}

// purgeLocked removes the whole history of flows deleted longer than the
// retention window ago.
func (r *Recorder) purgeLocked(ctx context.Context, current map[string]bool) error {
	for name, h := range r.heads {
		if current[name] || !h.deleted || r.now().Sub(h.at) < r.retention {
			continue
		}
		revs, err := r.store.List(ctx, name)
		if err != nil {
			return err
		}
		numbers := make([]int64, len(revs))
		for i, rev := range revs {
			numbers[i] = rev.Number
		}
		if err = r.store.Delete(ctx, name, numbers...); err != nil {
			return err
		}
		delete(r.heads, name)
		slog.Info("history: purged deleted flow", "flow", name, "revisions", len(numbers))
	}
	return nil
}

func (r *Recorder) headLocked(ctx context.Context, flow string) (head, bool, error) {
	if h, ok := r.heads[flow]; ok {
		return h, h.number > 0, nil
	}
	revs, err := r.store.List(ctx, flow)
	if err != nil {
		return head{}, false, err
	}
	h := head{}
	if n := len(revs); n > 0 {
		last := revs[n-1]
		h = head{number: last.Number, hash: last.Hash, deleted: last.Deleted, at: last.CreatedAt}
		// Recompute from the stored definition: a revision recorded in an
		// older shape (parser.decoders) hashes differently from the same
		// definition in the current shape, which must not look like a change.
		if last.Definition != nil && !last.Deleted {
			h.hash = Hash(*last.Definition)
		}
	}
	r.heads[flow] = h
	return h, h.number > 0, nil
}

// observeLocked records flow's current stored state if it differs from its
// latest revision. When nothing changed it returns the latest revision.
func (r *Recorder) observeLocked(ctx context.Context, flow string, meta Meta) (Revision, bool, error) {
	if err := validFlowName(flow); err != nil {
		return Revision{}, false, err
	}
	h, has, err := r.headLocked(ctx, flow)
	if err != nil {
		return Revision{}, false, err
	}
	def, exists := r.flows.Get(flow)
	rev := Revision{Flow: flow, Author: meta.Author, Message: CleanMessage(meta.Message), Source: meta.Source}
	if rev.Author == "" {
		rev.Author = UnknownAuthor
	}
	switch {
	case !exists && (!has || h.deleted):
		return r.latestLocked(ctx, flow, h)
	case !exists:
		rev.Deleted = true
		if rev.Source == "" {
			rev.Source = SourceExternal
		}
	default:
		norm := Normalize(def)
		norm.Name = flow
		rev.Hash = Hash(norm)
		rev.Definition = &norm
		if has && !h.deleted && h.hash == rev.Hash {
			return r.latestLocked(ctx, flow, h)
		}
		if rev.Source == "" {
			rev.Source = SourceExternal
			if !has {
				rev.Source = SourceImport
			}
		}
	}
	return r.appendLocked(ctx, rev, h)
}

func (r *Recorder) latestLocked(ctx context.Context, flow string, h head) (Revision, bool, error) {
	if h.number == 0 {
		return Revision{}, false, nil
	}
	rev, err := r.store.Get(ctx, flow, h.number)
	if err != nil {
		return Revision{}, false, err
	}
	return rev.Summary(), false, nil
}

func (r *Recorder) appendLocked(ctx context.Context, rev Revision, h head) (Revision, bool, error) {
	rev.CreatedAt = r.now().UTC()
	for attempt := 0; ; attempt++ {
		rev.Number = h.number + 1
		err := r.store.Append(ctx, rev)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrExists) || attempt == 2 {
			return Revision{}, false, err
		}
		// Another writer took the number: re-read the head and renumber.
		delete(r.heads, rev.Flow)
		if h, _, err = r.headLocked(ctx, rev.Flow); err != nil {
			return Revision{}, false, err
		}
	}
	r.heads[rev.Flow] = head{number: rev.Number, hash: rev.Hash, deleted: rev.Deleted, at: rev.CreatedAt}
	if rev.Number > int64(r.limit) {
		if err := r.pruneLocked(ctx, rev.Flow, rev.Number-int64(r.limit)); err != nil {
			slog.Warn("history: retention prune failed", "flow", rev.Flow, "error", err)
		}
	}
	return rev.Summary(), true, nil
}

// pruneLocked deletes every revision of flow numbered at or below upTo.
func (r *Recorder) pruneLocked(ctx context.Context, flow string, upTo int64) error {
	revs, err := r.store.List(ctx, flow)
	if err != nil {
		return err
	}
	var old []int64
	for _, rev := range revs {
		if rev.Number <= upTo {
			old = append(old, rev.Number)
		}
	}
	if len(old) == 0 {
		return nil
	}
	return r.store.Delete(ctx, flow, old...)
}
