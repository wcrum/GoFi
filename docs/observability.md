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
