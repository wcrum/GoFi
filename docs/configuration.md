# Configuration

GoFi is configured entirely through environment variables; there is no
configuration file for the process itself (flows, sandboxes and CEL
expressions live in their own stores). This page covers the runtime tunables
owned by the `config` package. Deployment-specific variables (`GOFI_DRIVER`,
`GOFI_IMAGE`, `GOFI_CONFIG_FILE`, `GOFI_HISTORY_DIR`, ...) are described in
[deployment.md](deployment.md).

## Profiles

`GOFI_PROFILE` selects a preset for every tunable below. Individual variables
override the preset. Pick the profile from the host class, then tune only
what the workload needs:

| Profile | Host | Shape |
|---|---|---|
| `edge` | 1-2 cores, 256 MB-1 GB RAM, flash storage, intermittent uplink (ARM SBCs, vehicles, sensor gateways) | small frames and buffers, one worker per stage, `drop-oldest` sinks so a dead uplink never stalls ingest, long retry cap, small history, GOMEMLIMIT at 70% of the budget |
| `default` | laptop, VM, small server | historical GoFi behaviour |
| `large` | 8+ cores, tens of GB | deep queues, 16 workers, 16 MiB frames, large tap buffers, GOMEMLIMIT at 90% |

Values accept the forms shown in the table: byte sizes as `64Mi`, `512k`,
`1G` or a plain number (binary multiples, like `GOMEMLIMIT`); durations as
Go durations (`30s`, `5m`) or bare seconds; booleans as `true/false/on/off`.
Invalid values abort startup with a message naming the variable, so a bad
unit file is caught by `systemctl status`, not hours later under load.

The process-wide values are reachable in Go as `config.Get().Limits`,
`config.Get().Observability` and `config.Get().Memory`.

## Memory and CPU sizing

On start GoFi applies:

- **GOMEMLIMIT**: if the `GOMEMLIMIT` environment variable is unset, the
  memory budget is `GOFI_MEMORY_LIMIT` or, failing that, the cgroup limit
  (`memory.max` on cgroup v2, `memory.limit_in_bytes` on v1, which is what
  `docker run --memory`, Kubernetes `resources.limits.memory` and systemd
  `MemoryMax=` all set). `GOFI_GOMEMLIMIT_FRACTION` of that budget becomes
  the Go soft limit so the GC runs harder before the kernel OOM-kills the
  process; the remainder is headroom for Python workers, stacks and the page
  cache. Without any limit GOMEMLIMIT is left untouched.
- **GOMAXPROCS**: `GOFI_GOMAXPROCS` if set; otherwise the Go 1.25+ runtime
  already derives it from the cgroup CPU quota. On older runtimes GoFi reads
  `cpu.max` / `cpu.cfs_quota_us` itself and lowers GOMAXPROCS to the quota.

The startup log line `runtime limits applied` records what was chosen.

## Variables

| Variable | Default | Edge | Large | Effect |
|---|---|---|---|---|
| `GOFI_PROFILE` | `default` | `edge` | `large` | Preset selecting every default below: edge (1-2 cores, <=1 GB), default, large. |
| `GOFI_MAX_FRAME_BYTES` | `1Mi` | `256Ki` | `16Mi` | Largest frame any framer accepts; oversized frames close the connection. Accepts 1Mi, 256k, 1048576. |
| `GOFI_FRAMER_INITIAL_BUFFER` | `64Ki` | `4Ki` | `256Ki` | Initial per-connection read buffer; grows on demand up to GOFI_MAX_FRAME_BYTES. Memory per idle connection is roughly this value. |
| `GOFI_MAX_DECODED_BYTES_PER_MESSAGE` | `8Mi` | `2Mi` | `64Mi` | Total decoder output (decode step plus every parse stage) allowed for one message; past it the message is dropped, so a tiny compressed frame cannot expand without bound. |
| `GOFI_MAX_CONNECTIONS` | `1024` | `512` | `65536` | Concurrent TCP connections per listen source; further accepts are closed at once (gofi_listener_connections_refused_total). |
| `GOFI_IDLE_TIMEOUT` | `0s` | `2m0s` | `15m0s` | Close a TCP connection that sent nothing for this long (0 disables). Frees buffers held by dead peers on lossy links. |
| `GOFI_UDP_READ_BUFFER` | `4Mi` | `256Ki` | `16Mi` | Datagram read buffer and SO_RCVBUF request per UDP flow. |
| `GOFI_UDP_WORKERS` | `1` | `1` | `16` | Goroutines reading and processing datagrams per UDP flow; more than one processes out of order. |
| `GOFI_SOURCE_WORKERS` | `1` | `1` | `8` | Goroutines processing pulled messages for non-acknowledging sources (NATS, MQTT, WebSocket, SSE, Redis Pub/Sub, fetch). |
| `GOFI_PIPELINE_WORKERS` | `4` | `1` | `16` | Goroutines running filter/transform stages per flow. |
| `GOFI_SINK_QUEUE_DEPTH` | `4096` | `512` | `65536` | Messages buffered in memory per sink before the drop policy applies. |
| `GOFI_SINK_BATCH_MAX` | `256` | `64` | `2048` | Largest batch a sink flushes in one write/request. |
| `GOFI_SINK_BATCH_BYTES` | `1Mi` | `256Ki` | `4Mi` | Payload bytes per batched request (Loki, HTTP) before the batch is flushed early. |
| `GOFI_SINK_BATCH_INTERVAL` | `1s` | `2s` | `500ms` | How long a partial sink batch waits for more messages before it is sent. |
| `GOFI_SINK_DROP_POLICY` | `block` | `drop-oldest` | `block` | Behaviour of a full sink queue: block (backpressure to the source), drop-newest, drop-oldest. |
| `GOFI_SINK_RETRIES` | `3` | `5` | `3` | Retries of a retryable sink delivery failure (network error, HTTP 429/5xx) before the batch is dropped. |
| `GOFI_SINK_RETRY_INITIAL_BACKOFF` | `200ms` | `200ms` | `200ms` | First delay after a sink delivery failure; doubles each retry. |
| `GOFI_SINK_RETRY_MAX_BACKOFF` | `30s` | `2m0s` | `30s` | Cap on the exponential backoff between sink retries during an outage. |
| `GOFI_SINK_BREAKER_FAILURES` | `5` | `5` | `10` | Consecutive failed deliveries that open a sink's circuit breaker (sends fail fast without touching the destination); 0 disables it. |
| `GOFI_SINK_BREAKER_COOLDOWN` | `10s` | `30s` | `10s` | How long an open sink breaker fails fast before one delivery probes the destination again. |
| `GOFI_HTTP_MAX_IDLE_CONNS` | `64` | `8` | `256` | Idle HTTP connections kept per destination host by HTTP-based sinks (HTTP, Loki, Zenoh REST). |
| `GOFI_SINK_SPOOL_DIR` | `""` | `""` | `""` | Directory for the on-disk sink spill buffer (segment-file queue). Empty disables spooling; messages that overflow the memory queue are then subject to the drop policy. |
| `GOFI_SINK_SPOOL_MAX_BYTES` | `256Mi` | `32Mi` | `4Gi` | Upper bound of on-disk spool per sink; oldest segments are dropped past it. |
| `GOFI_TAP_BUFFER_PER_SUBSCRIBER` | `1024` | `128` | `8192` | Tap events buffered per live-traffic subscriber (UI/CLI) before that subscriber starts missing events. |
| `GOFI_TAP_RAW_TRUNCATE` | `4Ki` | `512` | `64Ki` | Raw payload bytes carried per tap event (0 = none). Bounds memory and link usage of the UI stream. |
| `GOFI_DEDUP_MAX_KEYS` | `100000` | `10000` | `5000000` | Keys remembered per dedup stage; oldest are evicted first. |
| `GOFI_RATELIMIT_MAX_KEYS` | `100000` | `10000` | `5000000` | Keys tracked per rate-limit stage; oldest are evicted first. |
| `GOFI_PYTHON_WORKER_POOL` | `2` | `1` | `8` | Python worker processes per flow using Python stages/framing. Each worker is ~30-60 MB RSS. |
| `GOFI_HISTORY_LIMIT` | `50` | `10` | `500` | Revisions kept per resource in the history store. |
| `GOFI_HISTORY_DELETED_RETENTION` | `168h0m0s` | `24h0m0s` | `720h0m0s` | How long history of deleted resources is kept (0 = forever). |
| `GOFI_LOG_LEVEL` | `info` | `info` | `info` | Process log level: debug, info, warn, error. |
| `GOFI_LOG_FORMAT` | `json` | `json` | `json` | Log encoding: json (machine ingestion) or text (journald/console). |
| `GOFI_METRICS_ENABLED` | `true` | `true` | `true` | Serve metrics and aggregate per-flow counters. |
| `GOFI_METRICS_INTERVAL` | `5s` | `15s` | `5s` | Aggregation/push interval for per-flow metrics. Longer on intermittent links. |
| `GOFI_TAP_ENABLED` | `true` | `true` | `true` | Allow live traffic tap subscriptions (UI message stream). |
| `GOFI_WATCH_MAX_STREAMS` | `256` | `32` | `1024` | Open portal live streams (traffic + stats) one controller serves; further streams get resource_exhausted. |
| `GOFI_WATCH_MAX_STREAMS_PER_CLIENT` | `16` | `8` | `32` | Open portal live streams per client IP (first X-Forwarded-For hop behind an ingress). |
| `GOFI_WATCH_QUEUE` | `256` | `64` | `1024` | Events queued per browser stream; a browser further behind loses its own events (reported in the stream's dropped field). |
| `GOFI_HUB_IDLE_GRACE` | `10s` | `10s` | `10s` | How long the controller keeps a flow's runtime stream open after its last viewer leaves, so a page change reuses it. |
| `GOFI_HUB_RESYNC` | `15s` | `15s` | `15s` | How often a watched flow's runtime address is re-resolved when no Pod watcher pushes changes. |
| `GOFI_SAMPLE_BUFFER` | `100` | `20` | `500` | Recent messages retained per flow for UI sampling. |
| `GOFI_BULLETIN_RETENTION` | `100` | `25` | `500` | Bulletins (warnings) retained per flow. |
| `GOFI_MEMORY_LIMIT` | `0` | `0` | `0` | Memory budget for the process (e.g. 128Mi). 0 = read the cgroup limit; if neither is set no GOMEMLIMIT is applied. Ignored when GOMEMLIMIT is already set. |
| `GOFI_GOMEMLIMIT_FRACTION` | `0.8` | `0.7` | `0.9` | Share of the memory budget given to the Go GC as GOMEMLIMIT; the remainder is headroom for Python workers and the kernel. |
| `GOFI_GOMAXPROCS` | `0` | `0` | `0` | Override GOMAXPROCS (0 = runtime default, which honours the cgroup CPU quota on Go 1.25+; older runtimes get the quota applied by GoFi). |

`GOFI_HISTORY_LIMIT` and `GOFI_HISTORY_DELETED_RETENTION` also keep their
previous meaning for the history recorder (see [versioning.md](versioning.md));
the edge preset shrinks them so flash sees fewer revision files.

The table is rendered from `config.MarkdownTable()`; `go test ./config/`
fails when this file drifts from the code.

# UDS package configuration

The application chart is `charts/gofi`. The separate `chart` directory contains
the UDS `Package` and scoped NodePort `Exemption`. `common/zarf.yaml` composes
these charts; `zarf.yaml` selects the upstream flavor and pins the GoFi and
Python worker image versions. `bundle/uds-bundle.yaml` packages GoFi for an
existing UDS Core installation with identity, authorization, and monitoring
layers enabled.

## Portal and SSO

Default UDS configuration places the controller behind the admin tenant gateway
at `https://gofi.admin.uds.dev`. The Package's SSO client is **GoFi Login**, with
Authservice enabled on the controller selector and a `/login` return route. The
portal's SPA serves that route after authentication. All GoFi and Sandbox RPCs
and live streams use the same protected origin.

These values configure the UDS chart:

```yaml
domain: uds.dev
gateway: admin
sso:
  enabled: true
  secretConfig:
    name: gofi-sso
```

`gateway: tenant` uses `https://gofi.uds.dev`. Change the domain and gateway
together with the platform's DNS, certificates, and gateway routing. Keep the
Authservice selector aligned with the actual controller labels. Disabling SSO
removes this package's authentication declaration; it does not create an
alternative application login service.

The controller Service remains ClusterIP. Flow `listen` data sockets are separate
application endpoints and do not inherit browser login semantics.

## Durable resources

`Flow` and `Sandbox` are namespaced resources in
`gofi.wcrum.dev/v1alpha1`. GoFi reads only `kind: Flow` resources. Portal changes persist through the Kubernetes API,
and declarative changes reload during reconciliation. No application Postgres,
Valkey, or object-store dependency is required: Kubernetes stores desired
configuration, and live traffic state remains in process. This is not a durable
message queue or historical traffic database.

One controller reconciles at a time. `replicaCount > 1` requires
`leaderElection.enabled` (a `coordination.k8s.io` Lease; the bundle exposes
both as the `REPLICA_COUNT` and `LEADER_ELECTION` variables), and standbys
serve the portal while the Lease holder reconciles. Flow runtime Pods have no
service-account token; their Kubernetes selector targets are represented by
controller-managed Services, so the data path does not require Kubernetes API
credentials.

## Images and pull policy

The package pins `ghcr.io/wcrum/gofi:0.2.0` and `ghcr.io/wcrum/gofi-python:0.2.0`
and installs them with `imagePullPolicy: IfNotPresent` (`values/upstream-values.yaml`),
which also applies to the listener and worker Pods the controller creates. A
dev cluster that rebuilds the same tag sets the bundle variable
`IMAGE_PULL_POLICY: Always` in `uds-config.yaml`; do not ship that to
production, where a registry outage would keep drained flows from starting.
Pin by digest with `image.digest`/`sandboxImage.digest` where the release
process produces one. `python/Dockerfile` has a slim `worker` target and a
`tooling` target (adds ruff); `toolingImage` points the private format/validate
worker at the latter so libraries run the slim image. Build both images for
amd64 and arm64 with `docker buildx build --platform linux/amd64,linux/arm64`;
the Go stage cross-compiles from `TARGETOS`/`TARGETARCH`.

## Several GoFi namespaces

UDS Core allows one Package per namespace, and GoFi's flows are namespaced, so
scale out by installing one release per namespace (each with its own
controller, Flows, and Package). Give each namespace its own NodePort
Exemption name (`nodePort.exemptionName` in the config chart, default
`gofi-listener-nodeports`), create the PriorityClasses from one release only
(`priorityClasses.create`), and give each namespace its own Gateway when flows
use Gateway exposure (about 64 listeners per Gateway, `gateway.maxListeners`).
[Sizing and scaling](deployment.md#sizing-and-scaling) has the footprint table.

## NodePort and data networking

The UDS config chart can enable the `DisallowNodePortServices` exemption only for
Services named `gofi-listener-*` (flow runtime Services) in the GoFi namespace. It covers explicitly configured flow `listen` ingress. It does not exempt the controller Service or
Services in other namespaces. Set `nodePort.enabled: false` when NodePort is not
needed.

An exemption permits Service creation; it does not open a data path. Declare
site-specific ingress from approved source CIDRs and egress to actual receiver
ports through `additionalNetworkAllow`. For example, replace this documentation
CIDR with your approved sender range:

```yaml
additionalNetworkAllow:
  - description: Approved telemetry senders
    direction: Ingress
    selector:
      gofi.io/listener: telemetry
    remoteCidr: 192.0.2.0/24
    remoteProtocol: TCP
    port: 7000
```

Use the flow runtime Pod's `listen` port (`7000` here), not the allocated NodePort
(`31080`). For UDP use a separate entry with `remoteProtocol: UDP`. Cross-namespace
or external destinations also need explicit rules; selector destinations
currently remain in GoFi's namespace. For an external HTTP receiver, declare its
actual host and application port according to the installed UDS Core schema.
The [UDS network access guide](https://docs.defenseunicorns.com/core/v1-14/how-to-guides/networking/define-network-access/)
and [Package CR reference](https://docs.defenseunicorns.com/core/v1-14/reference/operator--crds/packages-v1alpha1-cr/)
describe these fields.

The package declares controller access to the Kubernetes API and intra-namespace
runtime communication. Python worker isolation also depends on avoiding broad
allow policies that select those workers: Kubernetes NetworkPolicy permissions
are additive. Review generated UDS policies alongside worker policies. Test
NodePort traffic against your installed mesh and admission configuration before
treating a rendered manifest as operational evidence.

Aster application traffic stays inside the flow runtime's dedicated loopback
namespace; do not expose the agent with a Service or NodePort. Agent mesh
connectivity and provisioning remain deployment-owner responsibilities.

## Build and integration gates

Review the pinned images and make them available before creating air-gap
artifacts. From the repository root:

```sh
helm lint charts/gofi chart
helm template gofi charts/gofi --namespace gofi
helm template uds-gofi-config chart --namespace gofi
# After validate-package succeeds, build final artifacts:
uds zarf package create --flavor upstream --skip-sbom --confirm
uds run create:test-bundle
```

The test bundle expects an existing compatible UDS Core cluster with identity,
authorization, and monitoring layers. Run the required `validate-package` gate
before the final build and deployment; resolve its findings in source, then
rebuild the package and bundle. Only after that validation gate is clear should
the workflow reuse a healthy cluster and deploy the rebuilt artifacts, check Pod
readiness and restarts, and exercise SSO and application traffic. Configuration
fixes belong in source and rebuilt artifacts rather than live-cluster patches.
The blocked MCP validation gate leaves this workflow blocked before live UDS
deployment.

UDS MCP operations in this session were blocked by the server's skill-integrity
guard. Package source, schema rendering, and application tests do not satisfy
those integration or validation gates. No successful UDS deployment, protected
login, or zero-finding package validation is claimed. The final measured checks
and remaining gates are recorded in [verification evidence](verification.md).
