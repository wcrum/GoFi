// Package history keeps an immutable revision log of every flow definition.
//
// Each saved change to a flow (a portal save, a rollback, or a change made
// outside GoFi such as kubectl edit or a hand-edited flow file) becomes a
// numbered Revision holding the full definition and its content hash. The
// Recorder decides when a revision is due (hash dedup, numbering, retention);
// a Store only persists revisions. Stores exist for local files (binary and
// Docker deployments) and Kubernetes FlowRevision resources; the interface is
// shaped so a Git backend (one commit per revision) can implement it too. See
// docs/versioning.md.
package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/wcrum/gofi/spec"
)

// Source says where a revision came from.
type Source string

const (
	// SourcePortal is a save through the API (the portal or any RPC client).
	SourcePortal Source = "portal"
	// SourceExternal is a change GoFi observed in its desired-state store
	// that it did not make: kubectl edit, GitOps, a hand-edited flow file.
	SourceExternal Source = "external"
	// SourceRollback restores an earlier revision through the normal save path.
	SourceRollback Source = "rollback"
	// SourceImport is the first revision of a flow that existed before GoFi
	// started recording its history.
	SourceImport Source = "import"
)

// UnknownAuthor attributes changes without an authenticated identity.
const UnknownAuthor = "unknown"

// Revision is one immutable version of a flow. A deletion is recorded as a
// tombstone revision (Deleted, no Definition) so the history outlives the
// flow for the deleted-flow retention window and a deleted flow can be
// restored from its last revision.
type Revision struct {
	Flow      string    `json:"flow"`
	Number    int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	Author    string    `json:"author,omitempty"`
	Message   string    `json:"message,omitempty"`
	Source    Source    `json:"source"`
	// Hash is the SHA-256 of the canonical definition (see Hash); empty for
	// a tombstone.
	Hash    string `json:"hash,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
	// Definition is the full flow snapshot. Summaries (Recorder.List) omit it.
	Definition *spec.FlowDefinition `json:"definition,omitempty"`
}

// Summary returns the revision without its definition.
func (r Revision) Summary() Revision {
	r.Definition = nil
	return r
}

var (
	// ErrNotFound reports a missing flow history or revision.
	ErrNotFound = errors.New("revision not found")
	// ErrExists reports that a revision number is already taken (another
	// writer appended first); the Recorder re-reads and retries.
	ErrExists = errors.New("revision already exists")
)

// Store persists revisions. Implementations do not interpret them: the
// Recorder owns numbering, dedup, and retention. Every method must be safe
// for concurrent use.
//
// A Git backend maps Append to writing <flow>/<number>.yaml and committing
// (author, message, and source in the commit), List/Get to reading the
// branch head, Delete to a no-op or a pruning commit, and ErrExists to a
// rejected (non-fast-forward) push.
type Store interface {
	// Append persists rev. It returns ErrExists if rev.Flow already has a
	// revision rev.Number.
	Append(ctx context.Context, rev Revision) error
	// List returns every stored revision of flow, oldest first, with
	// definitions. A flow without history returns an empty list.
	List(ctx context.Context, flow string) ([]Revision, error)
	// Get returns one revision with its definition, or ErrNotFound.
	Get(ctx context.Context, flow string, number int64) (Revision, error)
	// Delete removes the given revisions of flow (retention). Missing
	// revisions are not an error.
	Delete(ctx context.Context, flow string, numbers ...int64) error
	// Flows names every flow with stored history.
	Flows(ctx context.Context) ([]string, error)
	// Describe identifies the backend for logs and the API ("file", "kubernetes").
	Describe() string
}

// Normalize returns the form of a definition that history stores and hashes:
// an independent copy in the current shape (spec.FlowDefinition.Normalize),
// without the Python library names GoFi derives from inline source on load
// and without the run state.
func Normalize(def spec.FlowDefinition) spec.FlowDefinition {
	data, _ := json.Marshal(def)
	var out spec.FlowDefinition
	_ = json.Unmarshal(data, &out)
	// Revisions recorded before the decode step existed carry
	// parser.decoders; read them in the current shape. A conflicting
	// definition (never accepted by the controller) is hashed as is.
	_ = out.Normalize()
	out.StripDerivedPython()
	// The run state (running/stopped) is operational, not part of the
	// definition: starting or stopping a flow is never a revision, and a
	// rollback keeps the flow's current state.
	out.State = ""
	return out
}

// Hash returns the hex SHA-256 of a definition's canonical JSON. Zero values
// (null, "", false, 0, [], {}) are dropped first: a field that is absent and
// one that is explicitly empty decode to the same definition, so a store
// round trip (YAML file, Kubernetes resource) never looks like a change.
func Hash(def spec.FlowDefinition) string {
	data, err := json.Marshal(Normalize(def))
	if err != nil {
		return ""
	}
	var value any
	if err = json.Unmarshal(data, &value); err != nil {
		return ""
	}
	canonical, _ := json.Marshal(prune(value)) // map keys marshal sorted
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func prune(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			if item = prune(item); item != nil {
				out[key] = item
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		if len(v) == 0 {
			return nil
		}
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = prune(item)
		}
		return out
	case string:
		if v == "" {
			return nil
		}
	case bool:
		if !v {
			return nil
		}
	case float64:
		if v == 0 {
			return nil
		}
	}
	return value
}
