# Flows and sources (design)

Status: approved direction, 2026-10-16. R1 (the Flow rename), R2 (`fetch.http`,
`subscribe.nats`), and R3 (`subscribe.mqtt`, `websocket`, `sse`) are
implemented; R4 (Python sources) is not. This replaces
"Listener" as GoFi's top-level object and implements the streamer/fetcher
extension points tracked as PY-06.

## Why

A Listener only describes one way data arrives: GoFi opens a socket and waits.
GoFi's real unit is the whole connection, from where data comes from, through
framing, decoding and stages, to its destinations. A **Flow** names that unit,
and its **source** says how data arrives. Everything after the source works on
frames and stays the same: framing, decoders, parser, CEL and Python stages,
sinks, dead letters, test cases, Simulate, the debugger, and live traffic.

## Resource

```yaml
apiVersion: gofi.wcrum.dev/v1alpha1
kind: Flow
metadata:
  name: telemetry
spec:
  source:                       # exactly one of:
    listen:                     # others push to GoFi (today's listener)
      network: tcp              # tcp | udp
      port: 10001
      service: { type: NodePort, nodePort: 30101 }   # Kubernetes only
    fetch:                      # GoFi pulls on an interval
      http:
        url: https://api.example/items
        method: GET
        headers: {}
        auth: { type: bearer, token: … }             # same shape as the HTTP sink
        every: 10s
        timeout: 5s
      split: "$.items[*]"       # optional: one response -> many messages
    subscribe:                  # GoFi attaches to a stream
      nats: { url: nats://nats:4222, subject: sensors.>, queue: gofi, auth: … }
      # mqtt: { url: tcp://broker:1883, topic: devices/+/telemetry, qos: 1, auth: … }
      # websocket: { url: wss://feed.example/ws, headers: {} }
      # sse: { url: https://feed.example/events, headers: {} }
    python:                     # isolated worker source
      library: radar
      function: poll            # @gofi.fetcher or @gofi.streamer
      config: {}
  framing: { mode: raw }        # pulled sources default to one message per frame
  parser: { format: json }
  stages: []
  sinks: []
  deadLetterSink: null
  testCases: []
```

## Sources

| Source | Data arrives by | Runtime notes |
| --- | --- | --- |
| `listen` | Senders connect to a GoFi TCP/UDP port. | Existing listener runtime. A Kubernetes Service/NodePort is optional. Python framers and decoders replace the former ingress hook. |
| `fetch.http` | GoFi requests a URL every interval. | Each response body is one frame, or one frame per element when `split` is set. Non-2xx responses and timeouts are counted, logged, and retried at the next interval. No overlapping polls. |
| `subscribe.nats` | Core NATS subscription, optionally with a queue group. | Auth/TLS options shared with the NATS sink. No durable cursor (not JetStream) in the first cut. |
| `subscribe.mqtt` | MQTT topic subscription. | Adds an MQTT client dependency. QoS 0/1. |
| `subscribe.websocket` / `sse` | Each WebSocket message or SSE event becomes one frame. | Reconnect with capped backoff. |
| `python` | `@gofi.fetcher(every="10s")` returns bytes, a dict, or a list of these; `@gofi.streamer` yields them. | Runs in the library's isolated worker and requires that library's egress allowlist. Never runs in the controller. |

Pulled sources need outbound network from the flow runtime (the
`gofi-listener-*` Pod or container), never from the controller. On UDS, the
flow's runtime needs an egress allowance for its source endpoint (a
`network.allow` Egress rule for `gofi.io/managed-by: "true"` Pods, to the
remote namespace/selector, CIDR, or `Anywhere` for an external URL); without
one the source logs connection errors and keeps retrying. Delivery is
at-most-once: GoFi keeps no durable cursor or outbox, as documented in the
README.

### Runtime behavior (implemented)

- Each fetched response (or each `split` element, re-encoded as JSON) and each
  received message or SSE event enters Frame as its own stream, like one TCP
  connection or UDP datagram. Keep `framing.mode: raw` (the default) for one
  frame per message; line or delimiter framing can still split a message.
- `fetch.http`: `every` (default `30s`, minimum `1s`) and `timeout` (default
  `10s`, maximum `5m`) are Go durations. `split` accepts `$[*]` or
  `$.a.b[*]`. Polls run one at a time; non-2xx responses, timeouts, and split
  errors are counted, logged, and retried at the next interval. Bodies are
  capped at 16 MiB.
- `subscribe.nats`: `url` (`nats://` or `tls://`), `subject`, optional `queue`,
  and `auth` with the NATS sink's options. `subscribe.mqtt`: `url`
  (`tcp://`, `ssl://`, `mqtt(s)://`, `ws(s)://`), `topic`, `qos` 0 or 1,
  optional `clientId` (default `gofi-<flow>`), `username`, `password`.
  `subscribe.websocket`/`sse`: `url`, `headers`, and `auth` (the HTTP sink's
  shape); WebSocket can `send` one text message after connecting; SSE resends
  `Last-Event-ID` after a reconnect.
- Streams reconnect with exponential backoff from 1 s to 30 s.
- Metrics: `gofi_source_events_total{listener,source,outcome}` (`ok`, `error`,
  `reconnect`) and `gofi_source_last_success_timestamp_seconds`, alongside the
  existing per-frame counters. Live traffic shows each message's origin
  (`fetch:<url>`, `nats:<subject>`, `mqtt:<topic>`, …).
- Fetch and subscribe flows publish no port: Docker binds none and Kubernetes
  creates no Service.

## Compatibility

- `kind: Listener` resources (CRD and file store) keep loading. They convert
  to a Flow with a `listen` source: `source.{network,port,service}` becomes
  `source.listen`, and `source.python` becomes `source.listen.hook`.
- Everything new is written as `kind: Flow`. Listener is deprecated, and the UI
  marks legacy resources and offers an explicit Convert, which writes a Flow
  and deletes the Listener.
- ConnectRPC: Flow management RPCs replace the Listener ones. In-repo clients
  (UI, `testenv/`, `scripts/`) move together.

## R1 decisions (implemented)

- **API:** `Flow` replaces `Listener` in the protobuf contract. ListFlows,
  GetFlow, UpsertFlow, and DeleteFlow replace the Listener RPCs. The source is
  `FlowSource { listen }`, with field numbers 2 to 4 reserved for fetch,
  subscribe, and python.
- **Legacy removed:** the Listener CRD, the flat source shape, the
  `ConvertLegacyFlow` RPC, and the `LISTENER_SPEC` runtime payload are gone.
  Stores read and write only `kind: Flow`.
- **No source hook:** `listen.hook` and the Python `listener`/`ingress` kind are
  removed (`ListenSource.hook` is reserved). Python plugs into the flow's own
  steps instead: `@gofi.framer` (Frame), `@gofi.decoder` (Decode),
  `@gofi.parser` (Parse), `@gofi.transformer` (Stages), `@gofi.encoder` (a sink's
  Encode), and `@gofi.sink`. See [Python](python.md#one-decorator-per-flow-step).
- **Runtimes keep their names:** Kubernetes Pod names and labels
  (`gofi-listener-*`, `gofi.io/listener`), Docker container names and labels,
  the binary file path (`gofi/listeners.yaml`), the `listener/` Go package (the
  TCP/UDP socket implementation), and the `droppedBy: listener` value are
  unchanged, so existing runtimes are not orphaned.

## UI

- Navigation, URLs, and copy say Flows (`#/flows/...`).
- The flow canvas's first node becomes a **Source** node with a type picker
  (Listen / Fetch / Subscribe / Python), each with a Grafana form in the
  inspector.
- Explore and the dashboard pick flows. The topology's first column shows
  sources by type.

## Phases

1. **R1:** rename to Flow with compatibility, `listen` only. No behavior change.
2. **R2 (implemented):** `fetch.http` and `subscribe.nats`.
3. **R3 (implemented):** `subscribe.mqtt`, `websocket`, `sse`.
4. **R4:** Python `@gofi.fetcher` / `@gofi.streamer` with egress allowlists.

Each phase ships with Simulate and Test-flow support, live traffic, metrics per
source (polls, errors, last success, reconnects), and docs.
