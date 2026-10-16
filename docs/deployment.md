# Deploy GoFi

GoFi runs one controller and a runtime per Listener. Binary deployments use
in-process listeners, Docker deployments manage sibling containers, and
Kubernetes manages Pods and Services. The portal and ConnectRPC services share
the controller's HTTP listener.

| Setting | Purpose |
| --- | --- |
| `INGRESS_API_ADDR` | Controller bind address, default `:9090` |
| `GOFI_DRIVER` | `inprocess`, `docker`, or `kubernetes` |
| `GOFI_DEPLOY_MODE` | Explicit environment: `binary`, `compose`, or `kubernetes` |
| `GOFI_CONFIG_FILE` | Listener YAML path; defaults to `gofi/listeners.yaml` under `os.UserConfigDir()` |
| `GOFI_SANDBOX_CONFIG_FILE` | Python Sandbox YAML path; defaults to `sandboxes.yaml` beside the Listener file |
| `GOFI_IMAGE` | Image for managed listener containers or Pods |
| `GOFI_DOCKER_NETWORK` | Shared Docker listener/application network |
| `GOFI_K8S_NAMESPACE` | Managed namespace; defaults to the projected service-account namespace |
| `GOFI_SANDBOX_IMAGE` | Administrator-selected Python worker image |
| `GOFI_SANDBOX_NETWORK` | Internal Docker worker network, default `gofi-sandbox` |
| `GOFI_LISTENER_POD_TEMPLATE` | Additional listener sidecars, volumes, mounts, and environment |
| `GOFI_ASTER_TOKEN_FILE` | Default Aster token mount inside the listener runtime |

Use one active controller; leader election is not implemented. Local files and
Kubernetes resources persist desired configuration, not live traffic, dedup
windows, rate counters, or sink delivery receipts.

## Binary

```sh
npm --prefix ui ci
npm --prefix ui run build
GOWORK=off go build -o bin/gofi .
GOFI_DEPLOY_MODE=binary GOFI_DRIVER=inprocess ./bin/gofi
```

Open `http://localhost:9090`. Every Listener binds its configured TCP/UDP port on
this host. Kubernetes `source.service` and selector destinations are rejected in
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

`LISTENER_SPEC` selects an internal single-listener process role. Its private
ConnectRPC tap relay binds `TAP_RELAY_ADDR`, default `:9091`. This role does not
start the portal; managed Docker/Kubernetes runtimes use it internally.

## Docker Compose

```sh
docker compose up --build -d
```

Open `http://localhost:9091`, or set `GOFI_PORT` to choose another published port.
The Compose file mounts the Docker socket and a named data volume. Listener
configuration is stored at `/data/gofi.yaml`; Python libraries default to
`/data/sandboxes.yaml`. The socket lets GoFi manage listener and sandbox
containers.

Listeners publish their source TCP/UDP port one-to-one on the Docker host.
Use receiver container DNS names on the configured shared network, such as
`http://receiver:8080/events` or `tcp://receiver:9000`. Set `GOFI_IMAGE` and
`GOFI_DOCKER_NETWORK` explicitly if automatic inspection cannot identify them.
Runtime reconciliation is scoped to that application network. GoFi refuses to
replace an unmanaged container or a listener belonging to another network when
its generated container name conflicts.

Python workers join a separate internal Docker network. The supervisor attaches
only the controller and listeners that need worker RPC access. A binary outside
Docker uses a loopback-only control relay into this worker network. See
[Python isolation](python.md).

For file bind mounts, mount the directory instead of one file because writes use
atomic rename. For Aster, use a supported shared network namespace arrangement
from [the Aster guide](aster.md); sharing only a bridge network does not share
loopback.

## Kubernetes

The chart is `charts/gofi`. Use fixed application/worker image versions available
to your cluster:

```sh
helm lint charts/gofi
helm template gofi charts/gofi --namespace gofi
helm upgrade --install gofi charts/gofi --namespace gofi --create-namespace
```

A plain Helm installation supplies no identity provider. For local access:

```sh
kubectl -n gofi port-forward svc/gofi 9090:9090
```

The chart installs namespaced Listener and Sandbox CRDs. The controller's
namespace-scoped permissions manage their resources and owned runtime objects.
Listener Pods run without a Kubernetes service-account token. Configuration
reloads every 30 seconds, with immediate reconciliation after portal changes.
The controller repairs missing runtimes and source/target Service drift.

`source.service.type` is `ClusterIP` or `NodePort`. Its port defaults to
`source.port`; optional `nodePort` can be allocated by Kubernetes. TCP and UDP
retain their transport. This Service exposes application data, separately from
the portal and private live-tap relay.

The Endpoints inventory shows Services, assigned external/load balancer
addresses, NodePorts, and Pod labels/readiness. Selector destinations are scoped
to this namespace and use managed ClusterIP Services. Explicit application URLs
are also supported. Network policy must permit the actual data path.

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
| `ListListeners`, `GetListener` | Read durable desired definitions |
| `UpsertListener`, `DeleteListener` | Validate and persist changes |
| `GetRuntimeStatus` | Inspect the backing process/container/Pod |
| `ListEndpoints`, `ResolveTargets` | Discover endpoints and preview matching ready Pods |
| `Replay` | Send exact TCP/UDP bytes with per-destination outcomes |
| `Simulate` | Evaluate a draft without real sink delivery |
| `GetConfiguration`, `GetMode` | Read store identity and driver capabilities |
| `WatchTraffic` | One server stream for all listeners or a selected listener |

For an unauthenticated local binary, Connect JSON unary requests can be inspected
with ordinary HTTP tools:

```sh
curl http://localhost:9090/gofi.v1.GoFiService/ListListeners \
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
and timing, plus Go/process metrics. Each listener process has its own counters;
scrape managed listener relays as well as the controller where supported by the
platform configuration. A successful sink call is not a durable delivery proof.

A Loki destination can send selected traffic records to a reachable Loki push
endpoint with static labels and HTTP authentication. Keep labels low-cardinality
and keep secrets out of message fields; arbitrary field values are not promoted
to labels automatically.
