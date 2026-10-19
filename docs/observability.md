# Logs and metrics

GoFi writes application and HTTP request logs as one JSON object per line on
stdout. Log collectors such as Grafana Alloy can ship container output to
Loki without a multiline parser. Request records include method, URL path,
status, duration in milliseconds, and response size. They omit request bodies,
headers, query strings, and authentication credentials. The `stdout` traffic
sink also emits one JSON line per message with `fields` and `meta`.

Kubernetes operators can collect pod logs with their existing UDS logging
stack. Binary deployments can collect stdout with their service supervisor.
Loki collection is independent of application traffic routing: configuring a
Loki sink sends selected pipeline messages to Loki; collecting stdout ships
GoFi's own operational logs.

## Prometheus

The controller exposes `/metrics` on its API port (default `9090`). Each
managed flow runtime exposes `/metrics` on its tap/RPC relay port (`9091`, named
`metrics` in Kubernetes Pods). Binary mode hosts flow pipelines in the
controller, so scrape its API endpoint. Docker/Kubernetes deployments run
separate flow runtime processes; scrape their relay endpoints to collect their
pipeline counters as well as the controller's process metrics.

The `listener` label on each metric holds the flow name.

| Metric | Meaning |
| --- | --- |
| `gofi_frames_received_total{listener}` | Frames read before decoding |
| `gofi_source_bytes_total{listener}` | Original framed bytes, excluding framing delimiters |
| `gofi_parser_errors_total{listener}` | Parser failures under both continue and drop policies |
| `gofi_messages_rejected_total{listener,stage}` | Messages rejected by a stage, listener hook, or parser |
| `gofi_sink_dispatch_total{listener,sink_type,outcome}` | Sink calls that returned success or error |
| `gofi_sink_dispatch_duration_seconds{listener,sink_type}` | Histogram of sink call duration |

Default Go runtime and process collectors are included. Labels use configured
listener names, stage IDs, and sink types. Message content, remote addresses,
URLs, credentials, and exception text never become metric labels.

A successful sink dispatch means the sink's `Send` call succeeded. It does
not establish durable end-to-end delivery or deduplicate a retry. Conditional
sinks skipped by `when` expressions do not count as dispatches. Dead-letter
sink calls count under their configured sink type.

Flow operations add these metrics:

| Metric | Meaning |
| --- | --- |
| `gofi_flow_desired_running{listener}` | Controller: 1 when the declared state is running, 0 when stopped |
| `gofi_flow_runtime_up{listener}` | Controller: 1 when the driver reports a live runtime |
| `gofi_flow_restarts_total{listener}` | Controller: runtimes restarted after they exited on their own |
| `gofi_bulletins_total{listener,severity}` | Problems reported to the flow's bulletin board |
| `gofi_tap_events_dropped_total{listener}` | Live traffic events a slow viewer missed (pipeline delivery unaffected) |

Capacity and back-pressure ([data plane](data-plane.md)):

| Metric | Meaning |
| --- | --- |
| `gofi_listener_connections{listener}` | Open TCP connections on a listen source |
| `gofi_listener_connections_refused_total{listener}` | Connections closed at accept because the listener was at `GOFI_MAX_CONNECTIONS` |
| `gofi_listener_connections_idle_closed_total{listener}` | Connections closed by `GOFI_IDLE_TIMEOUT` |
| `gofi_udp_kernel_drops_total{listener}` | Datagrams the kernel dropped on the UDP socket (receive buffer full); never seen by the process |
| `gofi_udp_read_buffer_bytes{listener}` | Effective kernel receive buffer of the UDP socket |
| `gofi_sink_queue_depth{listener,sink_type}` | Messages waiting in a sink's async queue |
| `gofi_sink_queue_dropped_total{listener,sink_type}` | Messages dropped by a full queue (drop policies) or abandoned at close |
| `gofi_sink_queue_blocked_total{listener,sink_type}` | Sends that waited for room (policy block) |
| `gofi_sink_spooled_total{listener,sink_type}`, `gofi_sink_spool_depth{listener,sink_type}` | Overflow written to and waiting in the on-disk spool |
| `gofi_sink_retries_total{listener,sink_type}` | Retried deliveries |
| `gofi_sink_breaker_open{listener,sink_type}`, `gofi_sink_breaker_rejected_total{listener,sink_type}` | Circuit breaker state and deliveries it failed fast |
| `gofi_sink_batch_size{listener,sink_type}` | Messages per batched request (Loki) |
| `gofi_source_slow_consumer_total{listener,source}` | Messages a pulled source's client dropped because the pipeline fell behind |
| `gofi_decode_budget_exceeded_total{listener}` | Messages dropped over the per-message decoded-bytes budget |

For queued sinks (`http`, `loki`, `aster`, `zenoh`) `gofi_sink_dispatch_total`
and route health are recorded when the worker delivers, not when the pipeline
enqueues.

The live streaming path (portal traffic and stats views) adds these:

| Metric | Meaning |
| --- | --- |
| `gofi_watch_streams{kind}` | Open browser streams on the controller: `traffic` (WatchTraffic), `stats` (WatchFlowStats) |
| `gofi_watch_streams_rejected_total{reason}` | Streams refused by the `global` or per-`client` cap |
| `gofi_watch_write_timeouts_total` | Streams closed because a browser stopped reading for the write deadline |
| `gofi_hub_upstreams{kind}` | Controller-to-runtime streams: at most one per flow and kind, however many browsers watch |
| `gofi_relay_reconnects_total{kind}` | Controller-to-runtime stream reconnects (runtime restarts, network) |
| `gofi_tap_subscriptions{layer}` | Open live-traffic subscriptions per fan-out layer: `runtime` (relay viewers, including the controller), `controller` (browser queues) |
| `gofi_tap_dropped_total{layer}` | Events a slow subscriber missed per layer: `pipeline` (the pipeline's own tap queue), `runtime`, `controller`. Pipeline delivery is never affected |
| `rest_client_rate_limiter_duration_seconds{verb,host}` | Time Kubernetes API calls waited in client-go's client-side rate limiter (raise `GOFI_KUBE_QPS` when this grows). Standard client-go name, so kube-controller-manager dashboards apply |
| `rest_client_request_duration_seconds{verb,host}`, `rest_client_requests_total{code,method,host}` | Kubernetes API latency and request counts as client-go sees them |

Example queries for Grafana's Prometheus data source:

```promql
sum by (listener) (rate(gofi_frames_received_total[5m]))
sum by (listener, stage) (rate(gofi_messages_rejected_total[5m]))
sum by (listener, sink_type) (rate(gofi_sink_dispatch_total{outcome="error"}[5m]))
histogram_quantile(0.95, sum by (le, listener, sink_type) (rate(gofi_sink_dispatch_duration_seconds_bucket[5m])))
```

The importable [Grafana dashboard](grafana-dashboard.json) includes traffic,
parser errors, rejected messages, sink failures, latency, and Loki logs.
Select your Prometheus and Loki data sources after import.

## Native Loki sink

```yaml
sinks:
  - type: loki
    url: http://loki-gateway.monitoring.svc.cluster.local
    labels:
      environment: development
      application: telemetry
    headers:
      X-Scope-OrgID: tenant-a
```

A base URL receives `/loki/api/v1/push`; a URL already ending with that path
is used directly. The default static labels are `app="gofi"` and the configured
listener name. Explicit labels may override those defaults. Loki label names
must be identifiers, cannot start with `__`, and are limited to 30 configured
labels. Fields are stored in the JSON log line instead of becoming labels.
Use `bodyMode: raw` to store the original frame as the log line.

Wrapped lines contain `id`, `fields`, and `meta`. The message timestamp in
`meta.ts` becomes a nanosecond timestamp string in Loki's Push API. Bearer,
basic, and API-key authentication use the same `auth` configuration as HTTP
sinks; static headers support tenant routing. Each Push call has a ten-second
timeout. Non-2xx replies produce sink errors and increment failed dispatches.

Example LogQL for pipeline traffic sent through the native sink:

```logql
{app="gofi", listener="telemetry"} | json
```

Push API format follows [Grafana's Loki HTTP API](https://grafana.com/docs/loki/latest/reference/loki-http-api/).

## Problems (bulletins)

Each flow keeps a bulletin board of recent runtime problems, shown on the
flow's Problems tab, as a count in the Flows list, and as a badge in the top
bar. A bulletin aggregates every occurrence of one message from one component
with a severity, count, and first and last seen times:

| Component | Reported when |
| --- | --- |
| `source` | a pulled source poll or connection fails, a listener cannot accept or read |
| `framer` | a framer error drops a connection |
| `parser` | a frame fails to parse (warning) |
| `stage:<id>` | a stage fails to evaluate (warning) |
| `sink:<index>`, `deadLetter` | a destination send, encoding, or `when` condition fails |
| `runtime` | the runtime could not start, or exited and was restarted |

Each runtime process keeps at most 200 distinct bulletins per flow (the least
recently seen are dropped). Bulletins live in memory: a runtime's board starts
empty when it restarts, while crash and start-failure bulletins are kept by
the controller. In binary mode flows run in the controller, so there is one
board. Under Docker and Kubernetes the controller reads each running flow's
board from its relay port (`9091`, OperationsService `ListBulletins`) at most
every 5 seconds and serves every browser from that cache; browsers never
contact flow runtimes.

Acknowledging a bulletin keeps it listed but stops counting it; clearing hides
it. Either lasts until the problem happens again. Acknowledgements are kept in
the controller's memory and reset when it restarts.

## Graceful drain

Stopping, restarting, deleting, or updating a flow, and stopping a runtime
container or Pod, drains it: the runtime stops accepting connections,
datagrams, and pulled messages, finishes the frames it already received
through the pipeline, flushes buffering destinations (NATS), and exits.
Frames still unread on an open TCP connection are not waited for; senders
reconnect to the replacement runtime. `GOFI_DRAIN_TIMEOUT` (chart value
`drainTimeout`, default `20s`) bounds the drain; past it the runtime stops
hard. A second SIGTERM/SIGINT also stops it hard.

| Mode | Behavior |
| --- | --- |
| Binary | Flows drain in the controller process on delete/update/stop and when the controller shuts down |
| Docker | Containers get a stop timeout of the drain timeout plus 5s |
| Kubernetes | Listener Pods get a 3s `preStop` sleep (so Service endpoints are removed before intake stops) and `terminationGracePeriodSeconds` of preStop + drain + 7s (30s by default) |

## Live traffic and stats streams

The portal's live views are served by a hub in the controller, not by one
relay stream per browser:

- **One upstream per flow.** The first viewer of a flow opens one
  `WatchTraffic` stream from the controller to that flow's runtime (or one
  in-process subscription in binary mode); every further browser shares it.
  The upstream closes `GOFI_HUB_IDLE_GRACE` (default `10s`) after the last
  viewer leaves, so a page change reuses it. Dashboards (home, topology, flow
  overview, flows table) use `WatchFlowStats` instead: one small message per
  second with every flow's cumulative counters, again one upstream per flow
  shared by all viewers. Only the Live traffic and inspector views receive
  events.
- **Convert once.** A runtime converts each observed message to its RPC form
  exactly once and shares the immutable result with every subscriber. Strings
  longer than `GOFI_TAP_MAX_BYTES` (default 4096; `0` disables) are cut on the
  runtime before streaming and the event carries `truncated: true`. The full
  payload is not retrievable afterwards; raise the limit on the flow runtime
  (listener Pod environment) when the inspector needs more.
- **Bounded queues, visible drops.** Each browser has a queue of
  `GOFI_WATCH_QUEUE` events (default 256). A browser that cannot keep up loses
  its own events; the next message on its stream reports how many in
  `dropped`, and the Live traffic view shows "N events dropped". No layer ever
  blocks the pipeline or another viewer (`gofi_tap_dropped_total{layer}`).
- **Deadlines and caps.** Every write to a browser stream has a
  `GOFI_WATCH_WRITE_TIMEOUT` (default `10s`) deadline, so a stalled tab is
  closed instead of pinning memory. The controller serves at most
  `GOFI_WATCH_MAX_STREAMS` streams (default 256) and
  `GOFI_WATCH_MAX_STREAMS_PER_CLIENT` per client IP (default 16; the first
  `X-Forwarded-For` hop when an ingress sets it); further streams get
  `resource_exhausted` and `gofi_watch_streams_rejected_total` counts them.
- **Shared connection to each runtime.** The controller reaches runtimes over
  cleartext HTTP/2 (every stream to one Pod multiplexes on one connection, with
  PING-based dead-peer detection), falling back to HTTP/1.1 for an older
  runtime image. Reconnects use exponential backoff with ±50% jitter (1 s to
  15 s). Targets are re-resolved on stream failure, every `GOFI_HUB_RESYNC`
  (default `15s`), and immediately when the Kubernetes driver reports a Pod
  change.
- **Inventory polls.** `ListFlows` carries a `revision`; the portal polls with
  `if_revision` and gets an `unchanged` reply (no definitions) while nothing
  changed. `ListFunctions` is answered from a per-worker cache that refreshes
  when a worker's binding changes (restart or new digest) or after 60 s.

### More than one controller replica

Each replica serves its own browsers: it opens its own upstreams to the
runtimes it is asked about, so a runtime sees at most one traffic and one stats
stream per replica (`gofi_tap_subscriptions{layer="runtime"}`), not per
browser. Counters come from the runtimes themselves, so every replica reports
the same numbers; `gofi_watch_streams` and `gofi_hub_upstreams` are per
replica, and `ListFlows` revisions are per replica and must not be compared
across them (a browser that moves between replicas simply fetches the full
list once). Nothing in the streaming path needs leader election.

## Alerting

Set `alerting.enabled: true` to install a `PrometheusRule` (Prometheus
Operator CRDs required). Scrape both the controller's metrics and the listener
Pods' `metrics` port. Rules, with their thresholds in `values.yaml`:

| Alert | Fires when |
| --- | --- |
| `GoFiFlowDown` | a flow declared running has no runtime for `alerting.for` |
| `GoFiFlowCrashLooping` | a runtime restarted `restartsPerHour` times in an hour |
| `GoFiSourceErrors` | pulled-source errors and reconnects exceed `sourceErrorsPerSecond` |
| `GoFiSinkFailures` | the share of failed sends exceeds `sinkFailureRatio` |
| `GoFiParseErrors` | the share of frames failing to parse exceeds `parseErrorRatio` |
| `GoFiTapEventsDropped` | live traffic viewers miss more than `tapDropsPerSecond` events |
| `GoFiFlowNoTraffic` | a flow listed in `alerting.noTrafficFlows` received no frame for `noTrafficMinutes` (opt in per flow) |

`alerting.controller` (default on) adds the controller group:
`GoFiControllerNoLeader`, `GoFiControllerLeaderFlapping`,
`GoFiControllerWatchErrors`, `GoFiControllerReconcileStale`,
`GoFiControllerAPIThrottled` (p99 wait in client-go's rate limiter from
`rest_client_rate_limiter_duration_seconds`; raise `kubeClient.qps`/`burst`),
and `GoFiPodRestarts`. `alerting.streaming` (default on) adds the live-stream
hub group:

| Alert | Fires when |
| --- | --- |
| `GoFiLiveViewersDropping` | a fan-out layer drops more than `streaming.viewerDropsPerSecond` events/s for 10 minutes (`gofi_tap_dropped_total`); lower the view to one flow, raise `GOFI_WATCH_QUEUE`, or check browser load |
| `GoFiStreamCapReached` | a browser stream was refused in the last 15 minutes (`gofi_watch_streams_rejected_total`); raise `GOFI_WATCH_MAX_STREAMS` or the per-client cap, or find the tab storm |
| `GoFiRelayReconnectStorm` | controller-to-runtime streams reconnect more than `streaming.relayReconnectsPerSecond` times/s for 10 minutes (`gofi_relay_reconnects_total`); check runtime restarts and network policy |

Every alert's `runbook_url` points at [deployment.md "Runbooks"](deployment.md#runbooks).

```yaml
alerting:
  enabled: true
  ruleLabels: { release: kube-prometheus-stack }
  labels: { team: integrations }
  noTrafficFlows: [radar-feed]
```
