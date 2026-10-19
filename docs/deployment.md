# Deploy GoFi

GoFi runs one controller and a runtime per flow. Binary deployments run flows
in-process, Docker deployments manage sibling containers, and
Kubernetes manages Pods and Services. The portal and ConnectRPC services share
the controller's HTTP listener.

| Setting | Purpose |
| --- | --- |
| `INGRESS_API_ADDR` | Controller bind address, default `:9090` |
| `GOFI_DRIVER` | `inprocess`, `docker`, or `kubernetes` |
| `GOFI_DEPLOY_MODE` | Explicit environment: `binary`, `compose`, or `kubernetes` |
| `GOFI_CONFIG_FILE` | Flow YAML path; defaults to `gofi/listeners.yaml` under `os.UserConfigDir()` |
| `GOFI_SANDBOX_CONFIG_FILE` | Python Sandbox YAML path; defaults to `sandboxes.yaml` beside the flow file |
| `GOFI_IMAGE` | Image for managed flow runtime containers or Pods |
| `GOFI_DOCKER_NETWORK` | Shared Docker flow-runtime/application network |
| `GOFI_K8S_NAMESPACE` | Managed namespace; defaults to the projected service-account namespace |
| `GOFI_SANDBOX_IMAGE` | Administrator-selected Python worker image |
| `GOFI_SANDBOX_NETWORK` | Internal Docker worker network, default `gofi-sandbox` |
| `GOFI_LISTENER_POD_TEMPLATE` | Additional flow-runtime sidecars, volumes, mounts, and environment |
| `GOFI_ASTER_TOKEN_FILE` | Default Aster token mount inside the flow runtime |
| `GOFI_HISTORY` | `off` disables flow revision history ([versioning.md](versioning.md)) |
| `GOFI_HISTORY_LIMIT` | Revisions kept per flow, default `50` |
| `GOFI_HISTORY_DELETED_RETENTION` | How long a deleted flow's history is kept, default `720h` |
| `GOFI_HISTORY_DIR` | Revision directory (binary/Docker); defaults to `history/` beside the flow file |
| `GOFI_ASSISTANT_CONFIG_FILE` | GoFiBot configuration path (binary/Docker); defaults to `assistant.yaml` beside the flow file, mode `0600` ([assistant.md](assistant.md)). Kubernetes uses the Secret `gofi-assistant` instead |
| `GOFI_ASSISTANT_API_KEY` | GoFiBot model API key; overrides a stored key and locks the portal field |
| `GOFI_ASSISTANT_API_KEY_FILE` | File whose trimmed contents are the GoFiBot API key (a secrets mount); read when `GOFI_ASSISTANT_API_KEY` is empty; overrides a stored key |
| `GOFI_ASSISTANT_BASE_URL` | Pins the GoFiBot endpoint so portal users cannot change it |
| `GOFI_ASSISTANT_ALLOW_FUNCTIONS` | `1`/`0` pins whether Python libraries may use the GoFiBot endpoint through `gofi.llm` (the "Functions may use the AI endpoint" switch); the portal shows it locked |
| `GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT` | `1` lets GoFiBot call an endpoint on a private, loopback, or link-local address (an in-cluster gateway) |
| `GOFI_LLM_PROXY_ADDR` | Listen address of the AI proxy that Python libraries call while functions may use the AI endpoint ([python.md](python.md#ai-and-langchain-integrations)), default `:9091`; its own listener, never the API server. With Docker workers only the port is used: the proxy binds the controller's address on the sandbox network (chart: `llmProxy.port`) |
| `GOFI_LLM_MAX_OUTPUT_TOKENS` | Cap the AI proxy puts on `max_tokens` / `max_completion_tokens` of every forwarded request, default `8192` (chart: `llmProxy.maxOutputTokens`) |
| `GOFI_LLM_PROXY_HOST` | Kubernetes: `host:port` workers use for the AI proxy, when the Service that selects the controller is not the one to use; default: discovered from that Service's port targeting the proxy |
| `GOFI_LEADER_ELECTION` | `true` enables Lease leader election (Kubernetes); with `GOFI_LEADER_ELECTION_LEASE`, `_LEASE_DURATION`, `_RENEW_DEADLINE`, `_RETRY_PERIOD`, and `GOFI_POD_NAME` as the holder identity |
| `GOFI_KUBE_QPS`, `GOFI_KUBE_BURST` | client-go rate limits for the controller's Kubernetes client (chart: `kubeClient`) |
| `GOFI_LISTENER_RESOURCES`, `GOFI_LISTENER_SCHEDULING`, `GOFI_LISTENER_PROBES`, `GOFI_LISTENER_ENV` | JSON defaults for flow runtime Pods (chart: `listenerPods`, `listenerPods.extraEnv`); see [Pod defaults](#pod-defaults-from-the-chart) |
| `GOFI_WORKER_RESOURCES`, `GOFI_WORKER_SCHEDULING`, `GOFI_WORKER_PROBES`, `GOFI_WORKER_REPLICAS`, `GOFI_WORKER_ENV` | The same for Python worker Pods (chart: `workers`, `workers.extraEnv`) |
| `GOFI_PROFILE` | `edge`, `default`, or `large`: presets every data-plane tunable in [configuration.md](configuration.md) (chart: `profile`, applied to the controller and, through `GOFI_LISTENER_ENV`, every listener Pod) |
| `GOFI_TOOLING_IMAGE` | Image for the private format/validate worker; defaults to `GOFI_SANDBOX_IMAGE` |
| `GOFI_LB_ALLOCATE_NODE_PORTS` | `false` (chart default) sets `allocateLoadBalancerNodePorts: false` on flow LoadBalancer Services |
| `GOFI_LB_ANNOTATIONS` | JSON map of annotations added to every flow LoadBalancer Service (a flow's own annotations win) |
| `GOFI_GATEWAY_MAX_LISTENERS` | Listeners GoFi may hold on one Gateway (default 64); further flows report a Problem |
| `GOFI_WATCH_MAX_STREAMS`, `GOFI_WATCH_MAX_STREAMS_PER_CLIENT` | Open portal live streams per controller (default 256) and per client IP (default 16); preset by `GOFI_PROFILE` ([configuration.md](configuration.md)) |
| `GOFI_WATCH_QUEUE` | Events queued per browser stream before it drops (default 256; preset by `GOFI_PROFILE`) |
| `GOFI_WATCH_WRITE_TIMEOUT` | Per-write deadline on browser streams (default `10s`) |
| `GOFI_HUB_IDLE_GRACE`, `GOFI_HUB_RESYNC` | How long a flow's upstream stays open after its last viewer (default `10s`); how often targets are re-resolved without a Pod watcher (default `15s`) |
| `GOFI_TAP_MAX_BYTES` | Flow runtime: per-event string limit before streaming (default 4096, `0` unlimited); see [observability.md](observability.md#live-traffic-and-stats-streams) |

Local files and Kubernetes resources persist desired configuration, not live
traffic, dedup windows, rate counters, or sink delivery receipts. Binary and
Docker deployments run one controller. On Kubernetes, `replicaCount > 1` with
`leaderElection.enabled` runs standbys that serve the portal and API and take
over reconciliation when the Lease holder goes away (see
[Controller availability](#controller-availability)).

## Binary

```sh
npm --prefix ui ci
npm --prefix ui run build
GOWORK=off go build -o bin/gofi .
GOFI_DEPLOY_MODE=binary GOFI_DRIVER=inprocess ./bin/gofi
```

Open `http://localhost:9090`. Every flow with a `listen` source binds its
configured TCP/UDP port on this host. Kubernetes `source.listen.service` and
selector destinations are rejected in
this mode; use reachable explicit sink URLs.

The operating system selects the default configuration root. On Linux it is
normally `$XDG_CONFIG_HOME` or `~/.config`; on macOS it is normally
`~/Library/Application Support`. Override the complete path with
`GOFI_CONFIG_FILE`. Mount or back up the containing directory when needed.
A missing initial file starts empty. A loaded file disappearing is treated as a
storage error and does not erase the last good state.

A normal binary needs no Docker daemon for built-in processing. Python function
libraries use isolated Docker workers and require Docker access plus the worker
image. The controller never imports submitted Python into its own process.

`FLOW_SPEC` selects an internal single-flow runtime process role. Its private
ConnectRPC tap relay binds `TAP_RELAY_ADDR`, default `:9091`. This role does not
start the portal; managed Docker/Kubernetes runtimes use it internally.

## Docker Compose

```sh
docker compose up --build -d
```

Open `http://localhost:9091`, or set `GOFI_PORT` to choose another published port.
The Compose file mounts the Docker socket and a named data volume. Flow
configuration is stored at `/data/gofi.yaml`; Python libraries default to
`/data/sandboxes.yaml`. The socket lets GoFi manage flow-runtime and sandbox
containers.

Flows with a `listen` source publish its TCP/UDP port one-to-one on the Docker host;
a `quic` listener publishes that port as UDP, since QUIC runs over UDP. Use receiver container DNS names on the configured shared network, such as
`http://receiver:8080/events` or `tcp://receiver:9000`. Set `GOFI_IMAGE` and
`GOFI_DOCKER_NETWORK` explicitly if automatic inspection cannot identify them.
Runtime reconciliation is scoped to that application network. GoFi refuses to
replace an unmanaged container or a flow runtime belonging to another network when
its generated container name conflicts.

Python workers join a separate internal Docker network. The supervisor attaches
only the controller and flow runtimes that need worker RPC access. A binary outside
Docker uses a loopback-only control relay into this worker network. See
[Python isolation](python.md).

For file bind mounts, mount the directory instead of one file because writes use
atomic rename, and the directory also holds the backups GoFi keeps beside each
state file (`listeners.yaml.bak`, the last acknowledged save, and
`listeners.yaml.bak.1`, the one before it; likewise for `sandboxes.yaml` and the
CEL library file). Every save is durable when the API acknowledges it: the new
content is written to a temp file and fsynced, renamed over the file, the
backups are refreshed, and the directory is fsynced. A file corrupted after
the fact (media error, in-place edit, torn write) is served from `.bak`, which
never lags an acknowledged save, and healed on the next save. Saves stay cheap
with hundreds of flows because the store caches the file it last wrote and
re-encodes only the flow that changed (about 5 ms per save at 500 flows on a
local SSD, fsync-bound). For Aster, use a supported shared network namespace arrangement
from [the Aster guide](aster.md); sharing only a bridge network does not share
loopback.

## Kubernetes

The chart is `charts/gofi`. Use fixed application/worker image versions available
to your cluster (`image.digest` / `sandboxImage.digest` pin by digest):

```sh
helm lint charts/gofi
helm template gofi charts/gofi --namespace gofi
helm upgrade --install gofi charts/gofi --namespace gofi --create-namespace \
  -f values/production-values.yaml   # HA controller, PriorityClasses, NetworkPolicies, alerts
```

`values/production-values.yaml` is the multi-node baseline for a plain cluster,
`values/upstream-values.yaml` the UDS package's, and `values/dev-values.yaml`
the only overlay with `imagePullPolicy: Always` (dev clusters that rebuild one
tag). Production keeps `IfNotPresent`: with `Always`, a registry outage or rate
limit stops every listener Pod from starting after a node drain.

A plain Helm installation supplies no identity provider. For local access:

```sh
kubectl -n gofi port-forward svc/gofi 9090:9090
```

The chart installs namespaced Flow and Sandbox CRDs. GoFi reads only
`kind: Flow` resources. The controller's namespace-scoped
permissions manage their resources and owned runtime objects. Flow runtime Pods
run without a Kubernetes service-account token. Configuration
reloads every 30 seconds, with immediate reconciliation after portal changes.
The controller repairs missing runtimes and source/target Service drift.

`source.listen.service.type` is `ClusterIP`, `NodePort`, or `LoadBalancer`. The
Service listens on `source.listen.port`, which is also the flow Pod's container
port, so the portal asks for one port (`service.port` remains available in YAML
to map a different Service port). `nodePort` applies to NodePort and
LoadBalancer and is allocated by Kubernetes when unset. A LoadBalancer needs a
load balancer implementation in the cluster (k3d ships one) and, on UDS, a
matching network allowance. TCP and UDP
retain their transport. This Service exposes application data, separately from
the portal and private live-tap relay.

QUIC flows are UDP wherever a port is published: a `network: quic` listener gets
a Service port with `protocol: UDP`, a UDS allow with `remoteProtocol: UDP`, and
a Gateway `UDPRoute` (Docker publishes the host port as UDP). `quic` sinks
(`quic://host:port` or a Pod selector) get UDP egress allows and UDP target
Service ports. The TLS handshake and streams stay inside the flow; only the
transport is mapped.

The Endpoints inventory shows Services, assigned external/load balancer
addresses, NodePorts, and Pod labels/readiness. Selector destinations are scoped
to this namespace and use managed ClusterIP Services. Explicit application URLs
are also supported. Network policy must permit the actual data path.

## Controller availability

The controller is the only writer of flow runtimes; flows keep running without
it (listener Pods and workers are independent Pods), but changes, repairs after
a crash, and the portal are unavailable. For a highly available controller:

```yaml
replicaCount: 2
leaderElection:
  enabled: true        # coordination.k8s.io Lease in the release namespace
podAntiAffinity: soft  # or hard
priorityClasses:
  create: true         # gofi-control-plane / gofi-dataplane, once per cluster
```

With leader election the Deployment rolls one replica at a time (surge 1,
unavailable 0), a PodDisruptionBudget keeps one replica through drains, and the
Lease holder is the only replica that reconciles; the others serve reads and
redirect writes to the holder (or accept them: desired state lives in the
Kubernetes API, so any replica may persist a Flow and the leader reconciles it).

Probes: `/healthz` answers 200 while the process serves HTTP (liveness).
`/readyz` answers 200 once the Flow/Sandbox/Pod/Service informer caches have
synced, the flow store has loaded, and the controller has completed its first
reconcile pass; 503 before that, when a watch has been failing for longer than
the resync period, and while the runtime driver does not answer; standby
replicas are ready too (they serve reads). The `startupProbe` on `/readyz`
gives a cold start up to `probes.startup.periodSeconds × failureThreshold`
(120 s by default) before liveness applies. `GOMEMLIMIT` and `GOMAXPROCS`
follow the container limits.

The same two endpoints exist in binary and Docker mode: `/healthz` is
liveness only, and `/readyz` is 503 until the flow file has loaded and the
first reconcile pass has completed (every declared flow started or its failure
reported), and again whenever the Docker daemon stopped answering the
controller's last listing. Bind failures are handled per role: the controller
binds its API port before it starts any flow or container, so a port conflict
exits with nothing started; a flow runtime (Docker container, Pod) binds its
relay port and its flow port before it runs, and exits if either is taken; the
in-process controller (binary mode) keeps running the flows that bound and
reports the one that could not (runtime status `failed`, a bulletin), retrying
it with backoff.

## Pod defaults from the chart

The chart hands the controller JSON defaults for every Pod it creates;
Kubernetes-mode operators tune them in `values.yaml`, never per flow:

| Env | JSON shape | Applied to |
| --- | --- | --- |
| `GOFI_LISTENER_RESOURCES` / `GOFI_WORKER_RESOURCES` | `corev1.ResourceRequirements` (`requests`/`limits`: `cpu`, `memory`, `ephemeral-storage`) | listener container / worker container (worker `memory` comes from the Sandbox's `memoryMib` and overrides the JSON) |
| `GOFI_LISTENER_SCHEDULING` / `GOFI_WORKER_SCHEDULING` | `{priorityClassName, nodeSelector, tolerations, affinity, topologySpreadConstraints, terminationGracePeriodSeconds}` (all optional) | Pod spec; `terminationGracePeriodSeconds` overrides the derived preStop + drain + margin |
| `GOFI_LISTENER_PROBES` / `GOFI_WORKER_PROBES` | `{startup, readiness, liveness}` each `{enabled, periodSeconds, timeoutSeconds, failureThreshold, initialDelaySeconds}` | HTTP `GET /healthz` on the listener relay port (9091) or the worker RPC port (8080) |
| `GOFI_WORKER_REPLICAS` | integer | `> 1` runs each library's workers as a Deployment behind its Service (bare Pods otherwise) so HPAs and PDBs apply |
| `GOFI_LISTENER_ENV` / `GOFI_WORKER_ENV` | `[]corev1.EnvVar` (`name` plus `value` or `valueFrom`) | appended to the listener / worker container; names the driver sets itself (`FLOW_SPEC`, `GOFI_K8S_NAMESPACE`, `GOFI_PYTHON_SOURCE`, `GOFI_SANDBOX_TOKEN`, `GOMEMLIMIT`, ...) keep their values. The chart builds `GOFI_LISTENER_ENV` from `profile` (as `GOFI_PROFILE`) plus `listenerPods.extraEnv`, and `GOFI_WORKER_ENV` from `workers.extraEnv`. This is how the data-plane tunables of [configuration.md](configuration.md) (`GOFI_MAX_CONNECTIONS`, `GOFI_SINK_QUEUE_DEPTH`, `GOFI_SINK_SPOOL_DIR`, `GOFI_MAX_DECODED_BYTES_PER_MESSAGE`, ...) and `GOFI_TAP_MAX_BYTES` reach listener Pods |

Defaults (chart `listenerPods` / `workers`): listener requests 50m CPU, 64Mi
memory, 32Mi ephemeral storage, limits 1 CPU, 512Mi, 128Mi; worker requests
50m CPU, limits 1 CPU, memory from `memoryMib`; readiness every 5 s, liveness
every 10 s, startup every 2 s (worker 1 s) up to 30 (60) failures. The runtime
sets its own `GOMEMLIMIT` from the memory limit it receives.

## Sizing and scaling

Each flow is one listener Pod plus one Service (and a TCPRoute/UDPRoute or
target Services as configured); each Python library is one worker Pod (or
Deployment) plus a Service, Secret, and NetworkPolicy. Requests below are the
chart defaults; the controller row assumes 250m/256Mi at 1000 flows.

| Flows (and 10 libraries) | Pods | CPU requests | Memory requests | Nodes (max-pods 110, ~20 system Pods) | Services | NodePorts if all NodePort |
| --- | --- | --- | --- | --- | --- | --- |
| 10 | 10 + 10 + 1–2 | ~1.1 cores | ~2.0 Gi | 1 (2 for an HA controller) | 21 | 10 of 2768 |
| 100 | 100 + 10 + 2 | ~5.7 cores | ~8.0 Gi | 2 | 111 | 100 |
| 1000 | 1000 + 10 + 2 | ~51 cores | ~66 Gi | 12 (10 at the cap, plus headroom for drains) | 1011 | 1000 (36 % of the range) |

Limits are 1 CPU / 512Mi per listener, so burst capacity is 20× the requests;
raise `listenerPods.resources.requests` for flows that sustain traffic
(request what a flow uses at p95 so the scheduler packs honestly). Memory
beyond `limits.memory` is an OOM kill and a restart; sinks that buffer (HTTP
with retries, Zenoh, Loki) are the usual growth path.

Knobs, in the order they matter:

- `profile` (`edge`/`default`/`large`): one `GOFI_PROFILE` for the controller
  and every listener Pod that presets queue depths, worker counts, frame and
  decode budgets, and the portal stream caps ([configuration.md](configuration.md));
  `listenerPods.extraEnv` and `env` override single variables.
- `listenerPods.resources`, `workers.resources`: the footprint per flow/library.
- `history.limit` (50) and `history.deletedRetention`: FlowRevision objects per
  flow. At 1000 flows the default is up to 50 000 etcd objects; use 10 or less.
  A Flow spec is also inlined into its Pod's `FLOW_SPEC` env, so keep specs
  (large CEL, Python config) under ~100 KiB; etcd rejects objects over 1.5 MiB.
- `kubeClient.qps`/`burst` (20/50): a reconcile pass lists the namespace's Pods
  and Services and patches per flow; raise them when
  `GoFiControllerAPIThrottled` fires. The controller uses informers, so steady
  state is watch traffic, not polling.
- `drainTimeout` (20s): bounds a flow restart's unavailability together with
  the 3 s preStop delay; the Service has no second endpoint during it.
- `nodePortRange`: expand `--service-node-port-range` above 1000 NodePort
  flows, or prefer LoadBalancer with a shared address or Gateway exposure.

Sharding plan. Flows and their Pods are namespaced, and on UDS Core each
namespace has exactly one Package with one allow list, so the unit of
sharding is the namespace:

1. Install one release per namespace (`helm install gofi-a charts/gofi -n
   gofi-a`, `gofi-b` …); each gets its own controller, Role, Flows, and (on
   UDS) Package. Cluster-scoped objects are per release (`<fullname>-discovery`,
   `<fullname>-gateway-listeners` ClusterRoles) except the PriorityClasses
   (`priorityClasses.create` in one release only) and the UDS NodePort
   Exemption (`nodePort.exemptionName` per namespace in the config chart).
2. Keep a namespace at or below a few hundred flows: that bounds one
   controller's reconcile pass, etcd watch fan-out, and the blast radius of a
   bad Flow.
3. Gateway exposure adds a listener per flow to a Gateway, and implementations
   cap a Gateway at about 64 listeners (`gateway.maxListeners`); beyond it GoFi
   reports a Problem on the flow instead of breaking the Gateway. Give each
   namespace (or each 64 flows) its own Gateway, or expose with a
   LoadBalancer Service.
4. LoadBalancer exposure allocates no NodePort by default
   (`exposure.loadBalancer.allocateNodePorts: false`), and
   `exposure.loadBalancer.annotations` can make MetalLB share one address
   across flows (`metallb.io/allow-shared-ip: gofi`; flows then need distinct
   ports). Cloud load balancers are one per Service: budget for them or use a
   Gateway.

## Multi-node checklist

- Controller: `replicaCount: 2`, `leaderElection.enabled`, `podAntiAffinity`,
  `topologySpreadConstraints` across zones, `priorityClasses.create: true`.
- Disruption budgets: the controller PDB renders with `replicaCount > 1`;
  `workers.podDisruptionBudget` with `workers.replicas > 1`. Listener Pods are
  bare Pods on purpose (one flow, one ingress endpoint): a PDB cannot keep a
  flow up through a drain, so plan a flow restart per drained node instead.
- Drains: `kubectl drain` refuses bare Pods without `--force`. Drain GoFi
  nodes with `--force --pod-selector 'gofi.io/managed-by=true'` (or evict
  through the API); the controller recreates each evicted flow on another node
  within its resync period (30 s) plus image pull. Set
  `listenerPods.nodeSelector`/`tolerations` to keep flows on data-plane nodes
  and the controller elsewhere.
- Registry: `imagePullPolicy: IfNotPresent` and digest pins in production;
  `Always` only in `values/dev-values.yaml`. Mirror the images into the
  cluster's registry (Zarf does) so a drain does not depend on the internet.
- Resource requests: request p95 usage per flow; add `ephemeral-storage`
  requests (set by default) so log-heavy Pods are evicted predictably instead
  of filling a node.
- etcd: `history.limit` ≤ 10 past a few hundred flows; keep Flow specs small.
- Network: `networkPolicy.enabled` on an enforcing CNI (metadata service
  blocked, relay port restricted); on UDS rely on the Package instead.
- Alerting: `alerting.enabled` with `alerting.controller` and kube-state-metrics
  present for `GoFiPodRestarts`; route `runbook_url` to this document.

## Runbooks

Every alert's `runbook_url` points at the matching heading here.

### GoFiFlowDown

A flow declared running has no runtime. Open the flow's Problems panel; check
`kubectl -n <ns> get pod -l gofi.io/listener=<flow>` for Pending (resources,
PriorityClass, nodeSelector), ImagePullBackOff (registry, pull policy), or a
start error in the controller log. The controller retries every resync.

### GoFiFlowCrashLooping

The runtime exits repeatedly. Read the previous container's log
(`kubectl logs --previous`); OOMKilled means a sink is buffering (raise
`listenerPods.resources.limits.memory` or fix the destination).

### GoFiSourceErrors

A pulled source (fetch, stream) fails or reconnects. Check the destination
from inside the namespace, credentials, and network policy or UDS allows.

### GoFiSinkFailures

Sends fail. Check the receiver, DNS, and policy; a dead-letter sink keeps the
records meanwhile.

### GoFiParseErrors

Input does not match framing/parser; compare a live-traffic sample against
the flow's parser settings.

### GoFiTapEventsDropped

Live-traffic viewers are slower than the flow; close viewers or filter the
tap. Delivery is unaffected.

### GoFiFlowNoTraffic

An opted-in flow saw no frames: the sender, exposure (NodePort, LoadBalancer
address, Gateway listener), or a policy change.

### GoFiControllerNoLeader

No replica holds the Lease. `kubectl -n <ns> get lease <release>`; if the
holder Pod is gone, a standby should acquire within `leaseDuration`. Check
replicas are Ready, RBAC on `coordination.k8s.io/leases`, and API server
latency.

### GoFiControllerLeaderFlapping

Leadership changes repeatedly: Lease renewals time out. Look for CPU
throttling (raise `resources.limits.cpu`), restarts (`GoFiPodRestarts`), or
API latency (`GoFiControllerAPIThrottled`); lengthen `leaderElection.leaseDuration`
and `renewDeadline` together as a last resort.

### GoFiControllerWatchErrors

Informer watches fail: RBAC removed, CRD changed, API server unreachable, or
a NetworkPolicy/UDS allow missing to `KubeAPI`. The controller works from
stale caches until watches recover.

### GoFiControllerReconcileStale

The leader has not completed a pass in `reconcileStaleSeconds`. Check its log
for a hung driver call (a Pod delete waiting on a stuck finalizer, a Service
patch conflict) and API throttling; restarting the leader hands over to a
standby.

### GoFiControllerAPIThrottled

client-go waits on its rate limiter. Raise `kubeClient.qps`/`burst`, or shard
flows across namespaces so each controller handles fewer objects.

### GoFiPodRestarts

A controller or worker container restarts. `kubectl describe pod` for
OOMKilled (controller `resources.limits.memory`; worker Sandbox `memoryMib`),
probe failures (slow start: raise the startup probe threshold), or crashes
(previous log).

### GoFiLiveViewersDropping

Browsers watching live traffic fall behind (`gofi_tap_dropped_total{layer}`).
`controller` means a browser queue overflowed: narrow the Live traffic view
to one flow, raise `GOFI_WATCH_QUEUE` (`env`), or check the browser host's
load. `runtime` means the controller itself reads a runtime too slowly
(controller CPU throttling, `GoFiRelayReconnectStorm`). `pipeline` means the
runtime's own tap queue overflows: raise `GOFI_TAP_BUFFER_PER_SUBSCRIBER` on
the listener Pods (`listenerPods.extraEnv`). Flow delivery is unaffected in
every case.

### GoFiStreamCapReached

The controller refused browser streams (`gofi_watch_streams_rejected_total`).
`reason="client"` is one address opening too many tabs or a reconnect loop
(a stale tab, a broken ingress keepalive): find it in the controller log
(`too many open live streams`) or raise `GOFI_WATCH_MAX_STREAMS_PER_CLIENT`.
`reason="global"` means the controller is at `GOFI_WATCH_MAX_STREAMS`: raise
it (each stream costs one goroutine and a `GOFI_WATCH_QUEUE` buffer) or add a
replica, since every replica serves its own browsers.

### GoFiRelayReconnectStorm

Controller-to-runtime traffic or stats streams keep dropping
(`gofi_relay_reconnects_total{kind}`). Check listener Pod restarts
(`GoFiFlowCrashLooping`), the NetworkPolicy or UDS allow between the controller
and the listeners' `:9091` relay (`networkPolicy.relay`), and controller CPU
throttling (a starved controller misses HTTP/2 PINGs). Backoff is 1-15 s with
jitter, so a steady rate means the connection fails as soon as it is made.

## UDS gateway and authentication

The UDS package adds the admin tenant gateway, Authservice login, and networking
declarations. Its default portal is `https://gofi.admin.uds.dev`. The tenant
alternative is `https://gofi.uds.dev`. See [UDS configuration](configuration.md)
for overrides, NodePort policy, packaging commands, and the remaining validation
gates. Helm rendering and local application tests do not establish a successful
UDS login or cluster deployment.

## ConnectRPC surface

All application operations are typed RPCs on `gofi.v1.GoFiService` and
`gofi.v1.SandboxService`; generated sources are checked in under `gen` and
`ui/src/gen`. Python worker bindings are under `python/gofi/v1`.

| GoFiService method | Behavior |
| --- | --- |
| `ListFlows`, `GetFlow` | Read durable desired definitions |
| `UpsertFlow`, `DeleteFlow` | Validate and persist changes |
| `GetRuntimeStatus` | Inspect the backing process/container/Pod |
| `ListEndpoints`, `ResolveTargets` | Discover endpoints and preview matching ready Pods |
| `Replay` | Send exact TCP/UDP bytes with per-destination outcomes |
| `Simulate` | Evaluate a draft without real sink delivery |
| `GetConfiguration`, `GetMode` | Read store identity and driver capabilities |
| `WatchTraffic` | One server stream for all flows or a selected flow |

For an unauthenticated local binary, Connect JSON unary requests can be inspected
with ordinary HTTP tools:

```sh
curl http://localhost:9090/gofi.v1.GoFiService/ListFlows \
  -H 'Content-Type: application/json' \
  -H 'Connect-Protocol-Version: 1' \
  -d '{}'
```

In UDS, use the authenticated portal origin and session. The former `/api/*`
REST/SSE routes return 404. `/healthz`, `/readyz`, `/metrics`, and static portal
assets remain ordinary HTTP endpoints.

## Logging and metrics

Application and request diagnostics use structured JSON logs on stderr. The
stdout sink emits its traffic records separately. The Prometheus endpoint
exports frame/byte counts, parser errors, rejections, and sink dispatch outcomes
and timing, plus Go/process metrics. Each flow runtime has its own counters
(the `listener` metric label holds the flow name); scrape managed flow-runtime relays as well as
the controller where supported by the
platform configuration. A successful sink call is not a durable delivery proof.

A Loki destination can send selected traffic records to a reachable Loki push
endpoint with static labels and HTTP authentication. Keep labels low-cardinality
and keep secrets out of message fields; arbitrary field values are not promoted
to labels automatically.

## UDS network allows for flows

On UDS Core, a namespace's Package generates default-deny NetworkPolicies and
Istio AuthorizationPolicies, so a flow exposed outside the namespace (NodePort,
LoadBalancer, Gateway, or ClusterIP from other namespaces) or sending to
another namespace or host needs an allow entry. With `uds.manageAllows: true`
(gofi chart) and `gofiManagesPackage: true` (uds-gofi-config chart; set both
together), GoFi generates those entries:

- The config chart renders the Package's base (SSO, expose, monitors, static
  allows) into the `gofi-uds-package` ConfigMap instead of the Package itself.
- The controller server-side applies the `gofi` Package (field manager `gofi`)
  as the base plus one entry per flow need, each described `GoFi flow …`. The
  Package is owned by the ConfigMap, so it is removed with the chart.

UDS allows one Package per namespace and treats its allow list as one field,
so the Package must have a single owner; Helm owns only the ConfigMap.
Upgrading from a release that rendered the Package directly deletes and
recreates it once (a brief SSO and policy reconcile; users sign in again).

## Edge and embedded

The same static binary that runs the controller in Kubernetes runs alone on a
1-2 core, 256 MB-1 GB host with flash storage and an intermittent uplink
(ARM SBCs, vehicle gateways, sensor hubs). Nothing else is required: no
Docker, no database, no Python unless flows use Python stages. Files for this
mode live in [`deploy/edge/`](../deploy/edge/README.md); the tunables are in
[configuration.md](configuration.md).

### Install

1. Build or download the slim binary for the target:
   `task build:cross` produces `bin/gofi-edge-linux-{amd64,arm64,armv7}`
   (`CGO_ENABLED=0 -trimpath -ldflags="-s -w"`, tags
   `gofi_nosql,gofi_nokafka,gofi_nos3`). The tags drop the Postgres/MySQL/SQL
   Server, Kafka and S3 sources, which return "not available in this build"
   if a flow asks for them. Measured sizes (Go 1.27, UI embedded, 6.7 MiB of
   it is the SPA):

   | Target | full | edge tags |
   |---|---|---|
   | linux/amd64 | 57.9 MiB | 48.8 MiB |
   | linux/arm64 | 54.4 MiB | 45.9 MiB |
   | linux/arm/v7 | 55.5 MiB | 47.3 MiB |

   Most of the remainder is the Kubernetes and Docker client libraries the
   controller needs when it is *not* on the edge; splitting those behind a
   tag is the next size win (see TODO).
2. `deploy/edge/gofi.service` + `gofi.env.example` for systemd, or
   `gofi.openrc` for Alpine/busybox. Both run as an unprivileged `gofi`
   user with state in `/var/lib/gofi`, `Restart=always`, and
   `ProtectSystem=strict`-level hardening. The README in that directory has
   the exact commands.
3. Set `GOFI_PROFILE=edge`. That single variable sizes every buffer, worker
   count and retention for the host class; override individual values only
   when a workload needs it.

### Sizing

Measured with `scripts/edge-footprint.sh` (`task edge:footprint`): the edge
binary in Docker with `--memory=128m --cpus=0.5`, `GOFI_PROFILE=edge`,
5 TCP line/JSON flows into a stdout sink, 500 idle TCP connections held
open, then a 10,000-message burst. The kernel did not OOM-kill the
container in either run.

| | linux/amd64 (native) | linux/arm64 (qemu user-mode) |
|---|---|---|
| Startup to `/healthz` / all flow ports accepting | 312 ms / 319 ms | 1.29 s / 1.29 s |
| RSS idle (process) | 38.8 MiB | 85.1 MiB |
| RSS with 500 idle connections | 45.3 MiB (+13 KiB per connection) | 92.3 MiB |
| RSS after the 10k burst / peak | 53.9 / 53.1 MiB | 102.8 / 102.8 MiB |
| cgroup `memory.peak` | 37.5 MiB | 91.2 MiB |
| 10k messages: wall / CPU time | 336 ms / 147 ms | 3.07 s / 1.50 s |

The arm64 column is an emulation artefact: qemu's translation cache is
counted in the process RSS and the CPU time is the translated code's. On
real Cortex-A53/A72 hardware expect figures close to the amd64 column; treat
them as an upper bound and re-run the script on the device.

Rules of thumb for the edge profile: ~40 MiB RSS baseline, ~13 KiB per idle
TCP connection (`GOFI_FRAMER_INITIAL_BUFFER=4Ki` plus kernel buffers), ~1 MiB
per flow, 30-60 MiB per Python sandbox worker (`GOFI_PYTHON_WORKER_POOL=1`
on edge). A 256 MB host comfortably runs a handful of flows without Python;
with Python budget 128 MB for GoFi plus one worker per library. Set
`MemoryMax=` (systemd) or `--memory` and GoFi derives `GOMEMLIMIT` from it
at 70%; the startup log line `runtime limits applied` shows the result.

### Offline and intermittent links

- Sinks never give up and never crash the flow on DNS or link loss: every
  delivery error is retried with capped exponential backoff
  (`GOFI_SINK_RETRY_INITIAL_BACKOFF`, `GOFI_SINK_RETRY_MAX_BACKOFF`; edge caps
  at 2 min with jitter). Pulled sources (`fetch`, `subscribe`) reconnect the
  same way and resume where the protocol allows (SSE `Last-Event-ID`, Kafka
  and JetStream acks).
- `GOFI_SINK_SPOOL_DIR` turns on the on-disk spill buffer: messages that
  overflow the in-memory sink queue during an outage are appended to a
  bounded segment-file queue (`GOFI_SINK_SPOOL_MAX_BYTES`, 32 MiB on edge)
  with CRC-checked records and a durable ack cursor, and drain back in order
  when the destination answers. A crash between delivery and ack redelivers
  (at-least-once); a torn record from a power cut is truncated on start.
  Past the byte bound the oldest segment is dropped (edge) or the memory
  queue's drop policy applies.
- `GOFI_SINK_DROP_POLICY=drop-oldest` (edge default) means a dead uplink
  never applies backpressure to local senders; use `block` where loss is
  worse than a stalled sensor.
- Metrics over an intermittent link: scrape `/metrics` from the gateway
  side with a long interval (`GOFI_METRICS_INTERVAL=15s` on edge) and let
  the scraper tolerate gaps; counters are monotonic so missed scrapes lose
  resolution, not totals. Alternatively a `prometheus` remote-write or
  `loki` sink on the same host buffers through the spool like any other sink.

### Durable state on flash

`listeners.yaml`, `sandboxes.yaml`, `expressions.yaml` and each history
revision are written atomically (temp file, fsync, rename, fsync of the
directory) and the previous good copy is kept as `<file>.bak`. A corrupted
file is detected on load and the backup is used with a warning; the next save
heals the primary. Identical content is never rewritten, so idle
reconciliation does not wear flash. The edge profile keeps 10 revisions per
flow and 24 h of deleted-flow history (`GOFI_HISTORY_LIMIT`,
`GOFI_HISTORY_DELETED_RETENTION`); set `GOFI_HISTORY=off` to disable history
writes entirely on very small devices.

### Upgrades

The binary is a single file: copy the new one next to the old, `mv` it over
`/usr/local/bin/gofi` (atomic on the same filesystem) and
`systemctl restart gofi`. SIGTERM drains in-flight frames and flushes sinks
within `GOFI_DRAIN_TIMEOUT` (20 s default; keep systemd `TimeoutStopSec`
larger). State files are forward-compatible; keep the previous binary for a
rollback. For fleets, ship the binary and `gofi.env` through the existing
OTA channel; no schema migration step exists.

### Log rotation

Prefer `GOFI_LOG_FORMAT=text` into journald (systemd) or busybox syslog
(OpenRC) and let those rotate: `journald.conf` `SystemMaxUse=64M`, or
syslogd `-s 1024 -b 3`. GoFi does not write log files itself. Use
`GOFI_LOG_LEVEL=warn` on devices where storage is the constraint; per-flow
bulletins and metrics stay available over the API regardless of the log
level.

## Release artifacts and verification

`.github/workflows/release.yaml` runs on a `vX.Y.Z` tag (or by hand as a dry
run) and produces, for one version, all of:

| Artifact | Where | Signed / attested |
|---|---|---|
| Static binaries `gofi-{linux-amd64,linux-arm64,linux-armv7,darwin-*}` and the slim `gofi-edge-linux-*` variants, `SHA256SUMS`, the `deploy/edge` unit files | GitHub release assets | Sigstore keyless bundle per file (`<file>.sigstore.json`), SLSA build provenance via GitHub artifact attestations |
| Images `ghcr.io/wcrum/gofi:{V,V-edge}` (amd64, arm64, arm/v7), `gofi-python:V`, `gofi-python-tooling:V` (amd64, arm64) | GHCR | BuildKit provenance `mode=max` + SBOM, `cosign sign --recursive` on the index and each platform manifest, GitHub provenance and SPDX SBOM attestations pushed to the registry |
| Helm charts `oci://ghcr.io/wcrum/charts/gofi:V`, `.../uds-gofi-config` | GHCR + release `.tgz` | cosign on the OCI digest, Sigstore bundle + provenance on the `.tgz` |
| Zarf package `oci://ghcr.io/wcrum/packages/gofi:V-{amd64,arm64}` | GHCR + release `.tar.zst` | cosign on the OCI digest, Sigstore bundle + provenance on the archive (Zarf's own SBOMs are inside the package) |

`scripts/release-version.sh V` stamps the version into the charts, values,
Zarf package and image labels so every artifact of a release agrees; run it
locally before tagging. Verify a download before installing it:

```sh
ISSUER=https://token.actions.githubusercontent.com
ID='^https://github.com/wcrum/GoFi/.github/workflows/release.yaml@refs/tags/v'
cosign verify-blob --bundle gofi-edge-linux-arm64.sigstore.json \
  --certificate-identity-regexp "$ID" --certificate-oidc-issuer "$ISSUER" gofi-edge-linux-arm64
gh attestation verify gofi-edge-linux-arm64 --repo wcrum/GoFi
cosign verify ghcr.io/wcrum/gofi:0.3.0 --certificate-identity-regexp "$ID" --certificate-oidc-issuer "$ISSUER"
gh attestation verify oci://ghcr.io/wcrum/gofi:0.3.0 --repo wcrum/GoFi
cosign verify ghcr.io/wcrum/packages/gofi:0.3.0-arm64 --certificate-identity-regexp "$ID" --certificate-oidc-issuer "$ISSUER"
```

Kubernetes admission (Kyverno, Sigstore policy-controller, UDS Core's image
policies) can enforce the same identity on every GoFi image. Pin images by
digest in production values (`image.digest`), not by tag.
