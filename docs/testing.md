# Testing GoFi: the pyramid, the invariants, and how to read the evidence

GoFi's reliability claim is one sentence: **the system converges to the
declared Flow set and keeps delivering**, under failure, at scale, and across
restarts, in all three deployment shapes (single binary including constrained
hosts, Docker/Compose, multi-node Kubernetes). Every layer below checks the
same invariants with the same library; the layers differ only in how real the
world is.

| Layer | Build tag | What is real | Runs in | Command |
| --- | --- | --- | --- | --- |
| Unit | none | one package | seconds, every `go test ./...` | `task test` / `task test:race` |
| Integration | `integration` | the controller, the desired-state store (flow file or Flow CRDs on a real kube-apiserver + etcd), the history recorder; the runtime driver is an in-memory fake that counts verbs | about a minute | `task test:integration` |
| End to end | `e2e` | the whole binary or image, real sockets, real sinks, process/container kills | minutes | `task test:e2e`, `task test:chaos`, `GOFI_E2E_MODE=compose task test:e2e`, `task test:e2e:k3d` |
| Load | none (a `main`) | a running controller of any shape | minutes to an hour | `task test:scale` |
| Portal | none (Node) | the React portal in headless Chromium against a real controller | about two minutes | `task test:ui:e2e` |

CI runs unit + race + vet in `verify`, the integration job (envtest), a
benchmark job (benchstat against the PR base, failing on >20% allocs/op
regression in the data-plane packages), and the portal smoke on every PR;
`.github/workflows/nightly-e2e.yaml` runs the chaos suite (binary and Docker
shapes), the Kubernetes scenarios, and the scale ladder on a k3d cluster each
night and on demand (`workflow_dispatch` with a `-run` pattern).

## Invariants

`internal/testkit/invariants` is the contract. An observer fills a `World`
(declared flows, runtimes, Services, Secrets, bindings, revisions, UDS allows,
probes); `Check(world, opts)` returns violations. It is pure, so a unit test,
an envtest run, and a k3d run all assert the same thing.

| Rule | Statement |
| --- | --- |
| `one-runtime` | every running flow has exactly one healthy runtime (Running; Pending counts only where no kubelet exists, `PendingIsHealthy`), and that runtime runs the desired definition |
| `stopped-no-runtime` | a flow with `state: stopped` has no runtime |
| `no-orphan` | no runtime, flow Service, worker Pod/container, Secret, or NetworkPolicy exists without a desired owner |
| `service-shape` | a flow Service's type, port (service.port or listen port), targetPort (listen port), protocol (TCP/UDP from the network), nodePort (when fixed) and selector match the flow; a flow with exposure has exactly one Service |
| `history` | revision numbers are strictly increasing with gaps only at the start (retention); every revision's hash equals `history.Hash` of its definition; the head revision matches the stored flow, or is a tombstone when the flow is gone |
| `bindings` | each Python library token a runtime received equals the library's worker Secret |
| `uds-allows` | the UDS Package's generated allows equal `driver.UDSAllows(desired)`, with no stale `GoFi flow ...` entries |
| `metrics` | every metrics endpoint answers |
| `readyz` | `/readyz` is truthful: 200 implies the API answers, and not-200 while the API lists flows is a violation |

Observers:

- `ObserveFile(configPath, historyDir)`: the flow file and revision directory (binary, Docker). Sees no runtimes.
- `ObserveAPI(client)`: what the portal sees (ListFlows, per-flow runtime status, revisions, `/readyz`, `/metrics`). One runtime per flow at most, so pair it with a platform observer.
- `DockerObserver`: every container with GoFi's labels on the deployment network, in any state (so exited leftovers and duplicates count), bindings from `GOFI_PYTHON_BINDINGS`.
- `KubeObserver`: Flow and Sandbox resources, Pods, Services, Secrets, NetworkPolicies, FlowRevisions, and the UDS Package in a namespace.

In integration tests the driver is `internal/testkit/fakedriver`: an in-memory
driver with Docker/Kubernetes semantics (duplicate create refused, delete of
missing is fine), latency and failure injection, `Crash(name)` (a dead
container), `Orphan(def)` (a leftover nobody declared), and verb counters.
"Idempotent" is stated as a number: a restarted controller over a converged
world issues **zero** writes to the driver and zero writes to the API server
(the envtest `rest.Config` is wrapped in a verb-counting transport).

## Integration tests (`tests/integration`, tag `integration`)

Flow file store:

- convergence from 10, 100, and 500 stored flows with exactly N creates and 0 deletes;
- second sync writes nothing (controller restarted over converged runtimes, including a stopped flow);
- restart recovery mid-sync (slow driver, controller cancelled after a third of the creates, restarted over the surviving runtimes: exactly N successful creates in total, no deletes);
- concurrent Add/Remove/edit/stop/start while converging;
- store corruption (truncated, wrong kind, empty) with and without an intact `.bak`: desired state retained, runtimes repaired, saves refused while unreadable, removal applied once the file is valid again;
- an invalid edit written straight to the store keeps the last good version running and is reported;
- orphan runtime removed;
- history: numbering, hash dedup, external-change detection, rollback, retention, tombstones, all re-checked by `ObserveFile` + `Check`;
- atomic writes: a reader racing 300 saves never sees a torn file, and
  `listeners.yaml.bak` always equals the last acknowledged save (the
  `atomicfile` unit tests add a simulated crash after every step of a write
  and a torn primary after an acknowledged save: recovery never yields an
  older generation).

envtest (`kube-apiserver` + `etcd` from `setup-envtest`; `task test:integration`
downloads them into `.envtest/`, or set `KUBEBUILDER_ASSETS`/`GOFI_ENVTEST_DIR`).
`internal/testkit/envtest` starts the control plane without controller-runtime:
it generates the CA/serving/client certificates, a static token file (so a
controller *container* can mount `TokenDir()` as its service account and reach
the API server), installs `charts/gofi/crds` plus a stand-in `uds.dev` Package
CRD, and counts requests by verb and resource.

- convergence from 10 and 100 Flow CRs (ClusterIP exposure) with zero API-server writes during convergence and zero driver/API writes in the second controller life;
- the chart's CRD schema rejects malformed Flows (no source, two sources); a schema-valid but semantically invalid Flow is isolated by the controller;
- FlowRevision history on the API server: numbering, retention (limit 3), rollback, checked by `KubeObserver`;
- UDS allow set follows the flow set through an edit and a removal; a planted stale allow is detected.

Without the binaries the envtest tests skip locally and fail in CI (`CI=true`).

## End-to-end scenarios (`tests/e2e`, tag `e2e`)

The harness picks a deployment shape from `GOFI_E2E_MODE`:

- `binary` (default): builds the repository to `bin/gofi-e2e` (or uses `GOFI_BINARY`) and runs it as a child process with in-process runtimes; data ports on 127.0.0.1.
- `compose`: the controller runs in a container (`GOFI_E2E_IMAGE`, default `ghcr.io/wcrum/gofi:0.2.0`, must exist locally) with the Docker socket and drives sibling listener containers on a private network, like `docker-compose.yml`; listener ports are published 1:1 on the host, sinks reach the host through the network gateway.
- `kubernetes`: a cluster prepared by `scripts/load/k3d-up.sh` (3 agents, GoFi from `charts/gofi`, controller NodePort and flow NodePorts published through the k3d load balancer, `host.k3d.internal` for sinks); `source testenv/reports/k3d-gofi-e2e.env` first.

Every scenario drives identified messages (`{"id": "<prefix>-<n>", ...}`) from
`internal/testkit/traffic` (TCP, UDP, NATS generators; `Reconnect` for
sender-side retry) into a flow whose HTTP sink is the test's receiver, which
counts ids (unique, duplicates, first-seen time) and can be slowed, made to
drop connections, or blackholed. Each scenario ends with `assertInvariants`
and writes `testenv/reports/<shape>-<Test>.json`.

| Scenario | Failure injected | Measured | Asserted |
| --- | --- | --- | --- |
| `TestControllerRestartUnderLoad` | SIGTERM, then SIGKILL, at 600 msg/s | time to ready, delivery gap, sent/delivered/missing/duplicates | delivery resumes; graceful loses <2% of acknowledged sends; gap <45s |
| `TestRuntimeKillRecovers` (compose, k8s) | `docker kill` / Pod force-delete of the runtime | recovery time | recreated and delivering within 90s (two resync intervals) |
| `TestSlowAndBlackholedSink` | 200ms sink delay at 2000 msg/s; sink resetting connections; sink hanging past the 30s send timeout | peak RSS, delivered during/after, dispatch success/error counters | RSS growth <256 MiB; no TCP frame lost to backpressure (<1%); every dropped send counted as a sink error; hung sends time out and delivery resumes |
| `TestIdleConnectionsBounded` | 1000 idle TCP connections | RSS delta, KiB/connection | >900 accepted; <200 MiB; delivery continues |
| `TestUDPBurstLossCurve` | 2000-datagram bursts at 500, 2000, 10000, unlimited msg/s | delivered fraction per rate | >=95% at 500 msg/s; curve recorded |
| `TestFlowEditDuringTraffic` | definition edit mid-stream | edit-to-first-new-message, gap, stale deliveries | no message processed by the old runtime after the new one delivers |
| `TestStopStartRestart` | Stop/Start/Restart operations | start and restart latency | stopped flow refuses connections; invariants at every step |
| `TestRollbackRestoresDefinition` | two edits then rollback | revision count, head source | runtime runs the restored definition; history invariant with `HistoryExpected` |
| `TestSlowStreamClientDoesNotStallProducer` | WatchTraffic client that never reads | tap events dropped | all 5000 messages delivered to the sink |
| `TestKillDuringSavesKeepsConfigIntact` | SIGKILL during a save storm | acknowledged saves behind stored | file loads; every acknowledged save persisted; all flows running |
| `TestManyFlowsConverge` | controller restart with 50 flows | create-to-all-running, restart-to-all-running, RSS | same set, no duplicates |
| `TestEdgeProfile` | `docker run --memory=128m --cpus=0.5`, `GOMEMLIMIT=64MiB`, `GOFI_PROFILE=edge`, 20 flows, 10k-message burst, SIGKILL mid-save | start time, memory after flows/burst/restart, delivered vs `gofi_sink_queue_dropped_total`, OOMKilled | starts; every burst message delivered or counted as dropped by the bounded sink queue (edge preset sheds load, drop-oldest); not OOM-killed; configuration intact after SIGKILL; invariants (`GOFI_EDGE_ARM64=1` adds linux/arm64 when the daemon has binfmt) |

Python worker crash/adoption and node drain are not yet scenarios (see
"Gaps" below).

## Load and capacity (`scripts/load`)

`scripts/load/scale-ladder.sh 10,100,500` runs `scripts/load/scaleladder`
against `GOFI_SERVER`: for each rung it creates flows through the API, waits
for all-running, and records create time, convergence time, controller RSS and
CPU (from `/metrics`), optionally API server request deltas and QPS
(`--apiserver-metrics URL --kubeconfig`), and summed per-Pod RSS
(`--pod-metrics` file from `scripts/load/pod-rss.sh`). Output is a Markdown
table (`testenv/reports/scale-ladder.md`) and `testenv/reports/load-scale-ladder.json`.

Kubernetes at 500 to 1000 flows uses KWOK fake nodes so the Pods schedule
without running: `scripts/load/k3d-up.sh`, then `scripts/load/kwok-setup.sh 30`
(installs the KWOK controller and tainted fake nodes; either remove the taint or
add a toleration through `GOFI_LISTENER_POD_TEMPLATE`), then
`scripts/load/scale-ladder.sh 10,100,500,1000 --nodeport --apiserver-metrics ... --kubeconfig ...`.

## Portal smoke (`ui/e2e/run.mjs`)

Playwright (`playwright-core`, headless Chromium) against the Vite dev server
and a controller: every page renders in dark and light with no console errors,
page errors, failed requests, or 5xx responses (RPC-level 4xx such as
NotFound for a stopped flow's runtime are application semantics and ignored);
a flow goes through create, edit (YAML editor), stop, start, restart, rollback
(Resource tab, "Restore this version"), and delete entirely through the UI;
the Live traffic tab shows a sent message; and, when the runner owns the
backend (`GOFI_UI_E2E_OWN_BACKEND=1` spawns `bin/gofi-e2e` and Vite), the
live view is checked after a backend restart. That last check is a warning
unless `GOFI_UI_E2E_STRICT=1`: today the live view does not resume without a
page reload. Screenshots land in `testenv/reports/ui/`, results in
`testenv/reports/ui-smoke.json`. `npx playwright-core install chromium` once;
`GOFI_PW_MODULES` points at another `node_modules` holding playwright-core.

## Reading the reports

`testenv/reports/<deployment>-<Scenario>.json`:

```json
{
  "name": "TestControllerRestartUnderLoad", "deployment": "binary",
  "passed": true, "duration": "9.37s",
  "metrics": { "graceful.sent": 1528, "graceful.deliveredUnique": 1526, "graceful.missing": 2, "graceful.readyAfterSeconds": 0.102, "graceful.deliveryGapSeconds": 0.108, "...": "..." },
  "invariants": [],
  "steps": [ { "at": "...", "note": "..." } ]
}
```

`invariants` is empty when the final check converged; otherwise each entry is
`{rule, subject, detail}`. `metrics` keys are scenario-specific and listed in
the table above. The integration tests write the same shape under
`integration-*.json` and `envtest-*.json` (convergence seconds, verb counts,
API-server request counts). `GOFI_REPORT_DIR` redirects the directory.

## Adding a scenario

1. Pick the layer: a control-plane property belongs in `tests/integration`
   (fake driver, real store); anything involving sockets, processes, or
   containers belongs in `tests/e2e`.
2. In `tests/e2e`, start with `rep := scenario(t)`, `rx := receiver(t)`,
   `def := deploy(t, listenFlow(name, ports(n), flows.HTTPSink(deployment.SinkURL(rx))))`.
   Drive traffic with `traffic.Generator`, inject the failure through the
   `Deployment` interface (`Restart`, `Kill`, `KillRuntime`) or the receiver
   (`SetDelay`, `SetDrop`, `SetBlackhole`), measure, `rep.Set(...)`, and end
   with `assertInvariants(t, rep, defaultOptions())`.
3. Shapes that cannot inject a failure return `errNotSupported`; skip on it.
4. Add the scenario to the table above and to `task test:chaos` if it should
   run nightly.

## Gaps

- Python worker crash/adoption and Kubernetes node drain scenarios are not
  written; the `kubeDeployment` has `drainNode`/`uncordon` helpers ready.
- The Kubernetes shape is implemented but was not run on the authoring host
  (see docs/verification.md for what ran).
- A worker-protocol conformance suite (any language implementing
  `proto/gofi/v1/sandbox.proto`) would let Go/TypeScript/Python workers be
  tested identically.
- The file store's `.bak` recovery is one save behind: when the primary is
  corrupt and the backup is read, the most recent save is reverted rather than
  retained. The corruption test pins today's behaviour; the gap is tracked.
