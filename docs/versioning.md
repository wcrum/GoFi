# Flow versioning

Every change to a flow is kept as an immutable, numbered **revision**. The
portal's **Resource** tab shows the flow's resource beside its revision
timeline; picking a revision (`#/flows/<name>/resource?rev=N`; the old
`/history` path still resolves) diffs it against the current definition or the
revision before it, either visually (both versions as one graph with steps
marked added, removed, or changed; click one for its field-level changes) or
as YAML, and restores it. The flow page header shows the latest one
("Revision 7 · saved by ada, 5 minutes ago") and links there.

The visual diff matches steps by stable ids: the Source/Frame/Decode/Parse
steps by their fixed ids, stages by their `id`, the dead-letter sink by its
fixed id. Sinks have no id, so they match by type and destination (URL,
subject, Kubernetes selector, Python function), then leftovers of the same
type pair in order, which reads an edited URL as one changed sink. A stage
deleted and re-added under a new id reads as removed plus added, and a
reorder shows as re-routed edges (ui/src/lib/flow-diff.ts).

This document describes the local and Kubernetes implementation and the plan
for a Git backend (GitHub/GitLab), which the design is built for but which is
not implemented yet.

## Revisions

| Field | Meaning |
| --- | --- |
| `revision` | Monotonic per flow, starting at 1. Never reused, even after retention prunes older ones. |
| `createdAt` | When GoFi recorded the change. |
| `author` | The authenticated identity behind the change, or `unknown` (see [Authors](#authors)). |
| `message` | Optional note: `UpsertFlowRequest.message`, or the rollback message (default "Restore revision N"). |
| `source` | `portal` (saved through the API), `external` (changed outside GoFi: kubectl, GitOps, a hand-edited flow file), `rollback` (an earlier revision restored), or `import` (the flow as GoFi first found it). |
| `hash` | Hex SHA-256 of the canonical definition (below). Empty for a deletion. |
| `deleted` | A tombstone: the flow was deleted at this revision. |
| `definition` | The full flow spec at this revision (absent for a tombstone). |

**When a revision is recorded.** A save that leaves the content hash unchanged
records nothing, so re-saving, a store round trip, or a resync never adds
noise. The hash covers the definition with the Python library names GoFi
derives from inline source removed, serialized as JSON with every zero value
(`null`, `""`, `false`, `0`, `[]`, `{}`) dropped and keys sorted: an absent
field and an explicitly empty one decode to the same definition, so they hash
the same.

**How changes are observed.** `history.Recorder` (package `history/`) has two
inputs, serialized by one lock:

- `Recorder.Apply` wraps every save the API makes (`UpsertFlow`,
  `DeleteFlow`, `RollbackFlow`): it runs the controller change, then records
  the flow's resulting state attributed to the request. A failed change
  records nothing; a failure to *record* after a successful change is logged
  and does not fail the save.
- The run state (`spec.state`: running/stopped) is operational, not part of
  the versioned definition: `Normalize` strips it, so `StartFlow`/`StopFlow`
  (and a GitOps edit of `state` alone) record no revision, and a rollback keeps
  the flow's current state.
- `Recorder.Sync` runs every 5 seconds (and once at startup, before the API
  serves) and compares the controller's stored flows with each flow's latest
  revision. A flow without history is recorded as `import`; any other
  difference as `external`; a flow that disappeared gets an `external`
  tombstone. The controller reloads its desired-state store every 30 seconds,
  so an outside edit appears in history within about 35 seconds.

Because both paths hold the same lock, a portal save is never mistaken for an
external edit. Flows that fail validation are recorded too: they were saved,
and their history explains what changed.

**Deletion and retention.** Deleting a flow appends a tombstone; its history
stays readable (`ListFlowRevisions`) and restorable (`RollbackFlow` re-creates
the flow) for `GOFI_HISTORY_DELETED_RETENTION` (default `720h`, 30 days),
after which the whole history is purged (checked hourly). Each flow keeps its
last `GOFI_HISTORY_LIMIT` revisions (default 50); older ones are deleted after
each append.

**Rollback** (`RollbackFlow(flow, revision, message)`) reads the revision and
saves its definition through `Controller.Add`, the same path as any save, so
validation, inline Python checks, and platform admission (Pepr) all apply. It
is recorded as a new `rollback` revision; history is never rewritten.

## Authors

`history.AuthorFromHeader` names the author, first match wins:

1. Identity headers set by an authenticating proxy:
   `X-Auth-Request-Preferred-Username`, `X-Forwarded-Preferred-Username`,
   `X-Auth-Request-User`, `X-Forwarded-User`, `X-Auth-Request-Email`,
   `X-Forwarded-Email` (oauth2-proxy, Pomerium, and similar).
2. `Authorization: Bearer <JWT>`: the `preferred_username`, `email`, `name`,
   or `sub` claim. Under UDS Core, Authservice authenticates the portal session
   and forwards the Keycloak ID token in this header.
3. Otherwise `unknown`.

The token's signature is not verified: the author only labels a revision and
grants nothing. It is trustworthy only when an authenticating proxy owns these
headers and replaces any client-supplied value (verify this for your
Authservice configuration); a binary or Docker deployment without one accepts
whatever a client sends. External
changes are attributed to `unknown`; see [Follow-ups](#follow-ups).

## Storage

All backends store a revision as the same resource, so files, Kubernetes
objects, and (later) files in a Git repository are interchangeable:

```yaml
apiVersion: gofi.wcrum.dev/v1alpha1
kind: FlowRevision
metadata:
  name: orders.r7                 # <flow>.r<revision>
  labels:
    gofi.wcrum.dev/flow: orders
    gofi.wcrum.dev/revision: "7"
    app.kubernetes.io/managed-by: gofi
spec:
  flow: orders
  revision: 7
  createdAt: "2026-10-17T12:00:00Z"
  author: ada
  message: Raise the rate limit
  source: portal
  hash: 3f1c...
  definition: { source: ..., stages: [...], sinks: [...] }   # the Flow spec
```

The `history.Store` interface is deliberately dumb: `Append` (fails with
`ErrExists` if the number is taken: revisions are never overwritten), `List`,
`Get`, `Delete` (retention), `Flows`, `Describe`. The Recorder owns
numbering, dedup, retention, and attribution, so a new backend only persists.

### Binary and Docker: files

`<dir of GOFI_CONFIG_FILE>/history/<flow>/<revision>.yaml` (override with
`GOFI_HISTORY_DIR`). In Docker the flow file lives in a mounted directory, so
history is persisted and backed up with it. Each file is written to a
temporary file and hard-linked into place, which is atomic and refuses to
replace an existing revision.

### Kubernetes: `FlowRevision` resources

One namespaced `FlowRevision` object per revision in GoFi's namespace
(`charts/gofi/crds/flowrevisions.yaml`; `kubectl get flowrevisions -l
gofi.wcrum.dev/flow=orders`). Chosen over ConfigMaps because:

- **Object size.** One object per revision is never larger than the Flow it
  snapshots, which already fits etcd's ~1.5 MiB object limit. A ConfigMap
  holding a flow's whole history would grow toward that limit and need its
  own cap or sharding.
- **Least privilege.** The Role grants `get, list, create, delete` on
  `flowrevisions` only (no `update`: revisions are immutable, and the CRD
  also enforces `self == oldSelf` on `spec`). Writing ConfigMaps would need
  namespace-wide ConfigMap access, which also exposes unrelated config.
- **Atomic numbering.** `Create` fails with AlreadyExists if the number is
  taken; the Recorder re-reads and renumbers.
- **Tooling.** Printer columns (flow, revision, source, author, age), label
  selection, and the same shape as the file format.

Revisions have no owner reference, so they outlive a deleted Flow until the
deleted-flow retention purges them. A GitOps tool that manages Flows should
ignore `FlowRevision` objects (they are labeled `app.kubernetes.io/managed-by:
gofi`).

Helm installs CRDs from `crds/` only on first install: on an upgrade, apply
`charts/gofi/crds/flowrevisions.yaml` before rolling out. Until the CRD
exists, saves still work and history logs a warning.

## Configuration

| Setting | Default | Purpose |
| --- | --- | --- |
| `GOFI_HISTORY` | on | `off` disables recording and the History API |
| `GOFI_HISTORY_LIMIT` | `50` | Revisions kept per flow |
| `GOFI_HISTORY_DELETED_RETENTION` | `720h` | How long a deleted flow's history is kept |
| `GOFI_HISTORY_DIR` | `<config dir>/history` | File backend directory (binary/Docker) |

Helm: `history.enabled`, `history.limit`, `history.deletedRetention`.

## API

`FlowHistoryService` (`proto/gofi/v1/history.proto`):

| RPC | Purpose |
| --- | --- |
| `ListFlowRevisions(flow, limit)` | Summaries newest first, plus the retention limit and backend. |
| `GetFlowRevision(flow, revision)` | One revision with its full definition. |
| `RollbackFlow(flow, revision, message)` | Saves that revision's definition through the normal save path; returns the new revision (`created=false` if the flow already matched). |

`UpsertFlowRequest.message` sets the revision message for a save. Diffs are
computed in the portal (Monaco's diff editor over the Flow resource YAML), so
there is no diff RPC.

## Git integration plan (not implemented)

Goal: flows live in a Git repository (GitHub or GitLab) as the system of
record, with review and promotion through pull/merge requests.

### Backend: `history.Git`

A `Store` implementation over a clone of one repository and branch:

- **Layout.** `<path>/<flow>.yaml` holds the Flow resource (what `kubectl
  apply` takes); each revision is a commit touching it. Revision numbers are
  kept in a commit trailer (`GoFi-Revision: 7`, with `GoFi-Source:` and
  `GoFi-Hash:`) so `List`/`Get` read `git log -- <flow>.yaml` and `git show
  <commit>:<flow>.yaml`. A tombstone is a commit deleting the file.
- **Append** writes the file, commits with the author as the commit author
  (`ada <ada@...>` when the identity has an email; the GoFi bot as
  committer), and pushes. A rejected push (non-fast-forward) maps to
  `ErrExists`: the Recorder re-reads and retries, the same contract the
  Kubernetes backend already honors.
- **Delete** (retention) is a no-op: Git keeps all history; `List` caps at the
  limit.
- **Mirroring.** Git can run alongside the local or Kubernetes store
  (`history.Tee`: local store authoritative for the UI, Git written
  asynchronously with a retry queue) so a Git outage never blocks a save.

### Authentication

- GitHub: a GitHub App installation token (preferred: scoped to one repo,
  short-lived, refreshed by GoFi) or a fine-grained PAT; SSH deploy key with
  write access as the alternative.
- GitLab: a project access token or deploy key with `write_repository`.
- Credentials come from a Kubernetes Secret (or a file for binary/Docker)
  named by `GOFI_GIT_*` settings: repo URL, branch, path, and secret
  reference. Tokens are never logged or returned by the API. HTTPS and SSH
  host keys are pinned (`known_hosts` in the Secret). Egress to the Git host
  must be allowed (UDS Package egress allow); route the change through the
  security review process before enabling it.

### Sync model

1. **Push-only (first step).** GoFi is the writer: every revision becomes a
   commit. Edits made directly in Git are not read back; GoFi overwrites
   them on its next commit to that flow. Simple and safe; Git is an audit
   log and backup.
2. **Two-way (GitOps).** Git is the source of truth: GoFi polls (or receives
   a webhook for) the branch, and a new commit that GoFi did not author is
   applied through `Controller.Add` and recorded as `external` with the
   commit's author and message. Portal saves commit first and apply only
   after the push succeeds, so the cluster never runs a definition Git lacks.
   In Kubernetes this competes with Argo CD/Flux managing Flow CRs: pick one
   writer per flow (GoFi's Git sync, or a GitOps tool applying CRs that GoFi
   then records as `external`), never both.
3. **PR-based promotion.** Portal saves go to a branch per change
   (`gofi/<flow>/<revision>`) and open a pull/merge request through the
   GitHub/GitLab API instead of applying; merging to the tracked branch
   applies it via (2). The Resource tab's history would show pending PRs next to
   revisions, and promotion between environments becomes a PR from one
   environment's path or branch to another's.

### Conflict handling

- Push race (two writers): rejected push, fetch, rebase the single-file
  commit; if the same flow changed upstream, stop and record a conflict
  instead of overwriting: the save fails with `Aborted` and the portal offers
  the upstream version (a three-way diff in the Resource tab's history).
- In two-way mode a portal save names the revision (hash) it was based on;
  if the branch head for that flow has moved, the save is rejected the same
  way (optimistic concurrency, like Kubernetes `resourceVersion`).
- Invalid definitions committed directly to Git are recorded and reported
  like an invalid Flow CR today: the last valid version keeps running.

## Follow-ups

- A message field in the portal's save dialogs (the API already takes it).
- Attribute Kubernetes `external` changes from the Flow's `managedFields`
  (manager name, e.g. `kubectl-edit` or `argocd-controller`).
- Browse and restore deleted flows from the Flows list (the API supports it;
  the flow page only exists for current flows).
