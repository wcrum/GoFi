# Verification record

Updated 2026-10-16. This records executed checks separately from remaining
deployment gates. The working tree contains the combined changes for this
session; image refreshes and package rebuilds can lag the source.

## Test suites

The checks below were run by hand; the repeatable suites live behind
`Taskfile.yml` targets:

| Target | What it runs |
| --- | --- |
| `task test:unit`, `task test:race` | `go test ./...`, with and without the race detector (`config` includes the `docs/configuration.md` sync check) |
| `task test:integration` | controller convergence against the flow file and an envtest kube-apiserver (`tests/integration`, tag `integration`) |
| `task test:e2e`, `task test:e2e:k3d` | chaos scenarios against the binary or Compose, or on a throwaway k3d cluster (`tests/e2e`, tag `e2e`) |
| `task test:chaos` | the binary chaos subset plus the edge profile under `docker --memory=128m`, with JSON reports under `testenv/reports/` |
| `task test:scale` | the 10→100→500 flow ladder against a running controller (`scripts/load/scale-ladder.sh`) |
| `task test:ui:e2e` | the Playwright portal smoke against the dev server |
| `task test:bench` | framing/codec/parser/filter/pipeline/sink benchmarks into `testenv/reports/bench.txt` |
| `scripts/chart-test.sh` | strict `helm lint`, every values overlay rendered, the `charts/gofi/tests` helm-unittest suites, `zarf dev lint` |

## Source and protocol checks

- `GOWORK=off GOTOOLCHAIN=auto GOCACHE=/private/tmp/gofi-go-cache go test -race ./...`
  passed after the Python tooling RPCs and binding-reconciliation fix. The
  sandbox initially denied local socket binds; the successful run used approved
  execution outside that sandbox.
- Controller regression checks preserve freshly created Python listeners when
  worker discovery changes, retain them during worker startup, and replace only
  listeners with stale private bindings. Docker/Kubernetes environment checks
  distinguish referenced credential rotation from unrelated library additions.
- Generated Go Connect tests exercise listener CRUD, streaming and cancellation,
  simulation, discovery, real TCP/UDP replay bytes, Python function chaining,
  and sink error paths.
- Python worker tests pass for registration, invocation, authentication,
  deadlines, formatting, and static validation. Formatting/validation sentinel
  checks prove the submitted source is not executed by those actions.
- Real Docker worker tests pass using generated Go clients: custom parsing,
  Ruff formatting, undefined-name diagnostics, and runtime isolation checks.
- UI model/topology tests and the production build passed with actual Grafana
  components. Official icon assets and the Monaco worker are bundled locally.
- Protobuf generation and `buf build` pass. Existing naming-convention findings
  mean `buf lint` is not reported as passing.
- `actionlint` and `git diff --check` pass.

## Real application environment

The persistent Compose environment exposes the portal on `localhost:19090`, an
explicit-request sender on `19080`, a receiver on `19081`, and Prometheus on
`19094`. Its real TCP listener uses port `17000`. No background traffic generator
is required or started.

The initial end-to-end check persisted event
`88fe0325-81a5-4546-b265-ade172448f71` with the expected `observedBy: gofi`
transformation and both Prometheus scrape targets up. A full Sandbox control-plane
check created a temporary library and Docker listener, discovered registered
functions, and persisted event `35d4d14e-d03d-471a-8de7-83a39578ae67` with the
expected custom parser/transformer output. Temporary test resources were removed.
The latter run preceded the binding-race fix. After the final image refresh,
baseline event `af5abe83-f7e3-4f8a-8da4-8f7e3b66e9b3` persisted with the expected
transformation and both Prometheus targets up. Python event
`d299dab8-cdc2-4e8f-be5d-401db896e129` persisted through the custom parser and
transformer without the former startup settling delay. Receiver total reached
four records; temporary test flow/library resources were removed.

The final generated-client outer tooling check passed FormatSource,
ValidateSource, source nonexecution, private worker visibility, RunFunction,
explicit rejection, kind mismatch, and cleanup. Current local arm64 image
indexes are main
`sha256:b17e3c1873b5132e00bbb71afe4b8c9e549e005876abec59076a8e7f3453e924`
and Python worker
`sha256:afe55d6ad831935ac99e0a0448388e3b6bbf02e8aa614c4459c617ffd91ee838`.

See [the test environment instructions](test-environment.md) for commands that
send explicit, uniquely identified events and inspect receiver-side evidence.

## Browser evidence

The Grafana component migration was checked in a browser for official icon
loading, full-width cards, 4px rectangular geometry, stable flow layout,
Edit/Save/Cancel, and retaining unsaved edits across inventory polling. The
dashboard/live-flow theme consolidation and new Python editor actions require
their final checks against the refreshed image.

## Package and deployment gates

Both Helm charts pass strict lint. Rendered Listener/Sandbox schemas and UDS
Package configuration were checked against the repository-local schemas; Zarf
lint passes with image-pinning warnings. An initial arm64 Zarf package and test
bundle were created successfully. Final source/worker image changes require a
new package and bundle build.

Mandatory UDS MCP `validate-package` remains blocked by the server's skill
integrity guard, which requests `uds-mcp update` followed by reconnecting the MCP
server. Helm/schema/Zarf checks do not replace that gate. No authenticated UDS
deployment at `gofi.admin.uds.dev`, Kubernetes worker isolation, or live
Kubernetes networking qualification is claimed. The available local Kubernetes
context was unreachable during this session.

Security scans are not all green. The dependency findings, affected scope, and
remaining work are recorded in [the security scan assessment](security-scan.md).

## Resilience, scale, and restart harness (2026-10-17)

The test pyramid in [docs/testing.md](testing.md) was added and exercised on
the development host (Linux x86_64, Docker 29.8, no binfmt for arm64). Ran
and passed, with numbers from `testenv/reports/*.json`:

- `go test -tags integration ./tests/integration/` (flow file store): 10/100/500
  flows converged with exactly N creates and 0 deletes; a restarted controller
  over a converged world issued 0 driver writes; a controller cancelled after a
  third of 120 slow creates and restarted produced exactly 120 successful
  creates and 0 deletes; 60 flows with concurrent add/remove/edit/stop/start
  converged to 70 with no stale definitions; truncated/wrong-kind/empty flow
  files with and without an intact `.bak` kept every runtime, refused saves
  while unreadable, and applied a removal once valid; an invalid edit written
  to the store kept the last good version running; an orphan runtime was
  deleted; history numbering/dedup/external-change/rollback/retention/
  tombstone checks passed; 300 saves raced by a reader produced no torn read.
- envtest (`kube-apiserver` 1.37 + `etcd` from `setup-envtest`, started by
  `internal/testkit/envtest` with the chart CRDs and a stand-in `uds.dev`
  Package CRD): 10 and 100 Flow CRs (ClusterIP exposure) converged in 22 ms
  and 39 ms with 2 API-server requests and 0 writes; the second controller
  life made 2 requests, 0 API writes, 0 driver writes; the chart CRD schema
  rejected a Flow without a source and one with two sources while a
  schema-valid flow with bad CEL was isolated; FlowRevision history with limit
  3 retained revisions 3,4,5 after a rollback; the generated UDS allow set
  followed an edit and a removal and a planted stale allow was flagged. Flow
  file seeding of 500 flows through `File.Save` took 23 s (each save rewrites
  and fsyncs the whole file: about 47 ms per save at that size).
- `go test -tags e2e ./tests/e2e/` (binary shape, in-process runtimes):
  controller restart at 600 msg/s — graceful: 1528 sent, 1526 delivered, ready
  in 102 ms, delivery gap 108 ms; SIGKILL: 1535 sent, 1533 delivered, ready in
  104 ms, gap 108 ms; slow sink (200 ms per request at 2000 msg/s): 19,994
  frames accepted under backpressure with peak RSS 57 MiB and 100% delivered
  after the delay cleared; resetting sink: 500 of 500 sends counted as
  `gofi_sink_dispatch_total{outcome="error"}`; blackholed sink: 58 of 58 hung
  sends ended as 30 s timeouts and delivery resumed; 1000 idle TCP connections
  cost 50 MiB (about 51 KiB each) with delivery unaffected; UDP bursts of
  2000 datagrams delivered 100% at 500, 2000, 10000 and unlimited msg/s on
  loopback; a definition edit mid-stream applied in 161 ms with a 111 ms gap
  and zero stale deliveries; stop/start/restart, rollback, 5000 messages past
  a never-reading WatchTraffic client (1562 tap events dropped, 5000 delivered),
  SIGKILL during a save storm (20 flows, 0 acknowledged saves lost), and 50
  flows through a controller restart all held the invariants.
- `node ui/e2e/run.mjs` with an owned backend and Vite: 23/23 steps passed —
  every page in Grafana dark and light without console or page errors, the
  create/edit/stop/start/restart/rollback/delete lifecycle through the portal,
  and a live traffic row for a sent message. The live view did not resume
  after a backend restart without a page reload (recorded as a warning; strict
  mode fails it).

- Docker shape (`GOFI_E2E_MODE=compose GOFI_E2E_IMAGE=gofi:e2e`, image built
  from this tree; the published `ghcr.io/wcrum/gofi:0.2.0` predates
  OperationsService and fails the harness with 405): controller container
  restart at 600 msg/s — graceful and SIGKILL both 0 missing (listener
  containers outlive the controller), ready in 350-370 ms, delivery gap 5-6 ms;
  `docker kill` of a listener container recreated and delivering after 23.1 s
  (one resync interval); edit mid-stream applied in 350 ms, gap 322 ms, 0
  stale; stop/start/restart, rollback, and 50 flows through a restart held the
  invariants (Docker observer: no orphan or duplicate containers).
- Edge profile (`docker run --memory=128m --cpus=0.5`, `GOMEMLIMIT=64MiB`,
  `GOFI_PROFILE=edge`, non-root, read-only root, linux/amd64): started in 0.44
  s; 20 flows at 21 MiB; a 10k-message burst over 8 connections finished in 3.8
  s with 517 delivered and 9,483 counted in `gofi_sink_queue_dropped_total`
  (the edge preset's 512-deep drop-oldest sink queue shedding load as
  designed), memory 28.7 MiB, not OOM-killed; SIGKILL during a save storm
  followed by restart loaded all 20 flows, invariants held, 17.8 MiB.

Written but not run here: the Kubernetes shape (`task test:e2e:k3d`, 3-agent
k3d), the KWOK scale ladder, the linux/arm64 edge profile (no binfmt on this
host), and the nightly workflow. The shared `uds` k3d cluster was not used.
