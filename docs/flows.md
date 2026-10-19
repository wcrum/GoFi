# Flows and sources (design)

Status: approved direction, 2026-10-16. R1 (the Flow rename), R2 (`fetch.http`,
`subscribe.nats`), R3 (`subscribe.mqtt`, `websocket`, `sse`), and R4
(Python sources) are implemented. This replaces
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
      network: tcp              # tcp | udp | quic (quic: optional tls block, see quic.md)
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
      # s3:  { endpoint: https://minio:9000, bucket: logs, prefix: in/, glob: "*.json", moveTo: done/ }
      # sql: { driver: postgres, host: db, database: app, username: ro, password: …,
      #        query: "SELECT * FROM events WHERE id > $1 ORDER BY id", cursorColumn: id, cursorStart: "0" }
    subscribe:                  # GoFi attaches to a stream
      nats: { url: nats://nats:4222, subject: sensors.>, queue: gofi, auth: … }
      # mqtt: { url: tcp://broker:1883, topic: devices/+/telemetry, qos: 1, auth: … }
      # websocket: { url: wss://feed.example/ws, headers: {} }
      # sse: { url: https://feed.example/events, headers: {} }
      # kafka: { brokers: [kafka:9092], topics: [orders], group: gofi-orders, startOffset: earliest,
      #          tls: { enabled: true }, sasl: { mechanism: SCRAM-SHA-512, username: …, password: … } }
      # amqp: { url: amqp://rabbitmq:5672/, queue: orders, prefetch: 100 }   # or exchange + routingKey
      # jetstream: { url: nats://nats:4222, stream: ORDERS, consumer: gofi-orders, subject: orders.>, deliverPolicy: new }
      # redis: { url: redis://redis:6379/0, stream: events, group: gofi, field: data }   # or channels: [a, b]
    python:                     # isolated worker source
      library: radar
      function: poll            # @gofi.fetcher or @gofi.streamer
      config: {}
  python: { library: radar }    # optional default library for Python steps
  framing: { mode: raw }        # pulled sources default to one message per frame
  decode: { decoders: [] }      # optional: base64, gzip, … before the parser
  parser: { format: json }      # json | xml | none | text | python (none and text: the decoded frame as fields.raw)
  stages: []                    # filter, transform, parse, python, …
  sinks: []                     # http, tcp, udp, quic (quic://host:port with a tls block, see quic.md), nats, …
  deadLetterSink: null
  testCases: []
  state: running                # running (default when omitted) | stopped
```

### Run state

`spec.state: stopped` keeps the flow's definition but runs no runtime. It is
stored with the flow (file store, Flow resource, and GitOps manifests), so a
stopped flow stays stopped across controller restarts. The portal's Start and
Stop buttons (OperationsService `StartFlow`/`StopFlow`) save the state through
the normal save path; Start removes the field again rather than writing
`running`. Run state is operational, not part of the versioned definition:
Start and Stop record **no revision** in the flow's history (see
[versioning](versioning.md)), and a rollback keeps the flow's current state.
`RestartFlow` recreates the runtime without a definition change. Changing only
`state` never recreates a running runtime.

Stopping drains the runtime (see [Logs and metrics](observability.md#graceful-drain)).
With the Kubernetes driver the flow's Service, Gateway route, and destination
Services are owned by its Pod and are removed with it; they come back on
Start. Senders addressing a stopped flow's Service get "no such host" or
"connection refused" until then.

## Pipeline order and layered data

A flow reads in pipeline order, and so does its resource:

```
source → framing → decode → parser → stages (any number of parse stages) → encode (per sink) → sinks
```

### Decode step

`spec.decode` runs ordered decoding steps on each frame before the parser.
Steps are built-in codecs (`base64`, `base64url`, `base32`, `hex`, `url`,
`gzip`, `zlib`, `deflate`, `zstd`, `snappy`, `lz4`, `brotli`, `bzip2`) or a
Python `@gofi.decoder` (`{type: python, python: {function: …}}`). A bare name is
shorthand for `{type: name}`.

```yaml
decode:
  decoders: [base64, gzip]
  maxDecodedBytes: 1048576     # cap on each step's output; 0 = 1 MiB
```

The raw frame never changes: taps, the CEL `raw` variable, and `raw` encodings
see the bytes that arrived. A decode failure is a parse error (subject to
`parser.errorPolicy`).

**Compatibility.** Before the decode step, these settings lived on the parser
as `parser.decoders` and `parser.maxDecodedBytes`. Every read path (file and
Kubernetes stores, the API, FlowRevision history, and flow runtimes) still
accepts them and reads them as `decode`; GoFi only writes `decode`. Setting
both locations is accepted when they agree and rejected when they differ. In
the protobuf contract `ParserConfig.decoders` (6) and
`ParserConfig.max_decoded_bytes` (3) are kept but deprecated, so older clients
keep working; the server never returns them. History hashes the current
shape, so the conversion alone never records a revision.

### Default Python library

`spec.python.library` names the library that Python steps use when they give
only a function. It applies to the framer, decoders, parser, parse stages,
`python` stages, sink encoders, sinks, and the dead-letter sink. A step that
names its own library keeps it, and inline stage source still runs from the
flow's managed library. The stored flow keeps what was written (references
without a library); validation, Simulate, the debugger, and runtimes use the
resolved library. Saving a flow with a library-less reference and no default
fails with `python.library is required`. A Python *source* always names its
library.

```yaml
python: { library: parsers }
parser: { format: python, python: { function: parse_syslog } }   # parsers.parse_syslog
```

### Parse stages

A stage of kind `parse` parses data already on the message, as many times as
needed:

```yaml
- id: inner
  kind: parse
  parse:
    from: 'fields["message"]'   # CEL yielding string or bytes; "raw" or empty = the raw frame
    decoders: [base64, gzip]    # optional, same steps as decode.decoders
    format: json                # json | xml | text | python
    python: { function: parse_x }   # format python (library may come from spec.python)
    into: payload               # "" = merge into top-level fields; else fields[into]
    mode: merge                 # merge | replace
    onError: continue           # continue | drop
```

- **Input.** `from` is a CEL expression over `fields`, `meta`, and `raw`,
  compiled once when the flow starts. It must yield a string or bytes; a
  missing key or another type is an error. `raw` (or empty) uses the frame's
  raw bytes directly. Each decoder's output is capped at 1 MiB.
- **Formats.** `json` and `xml` behave as the flow parser does (nested JSON
  objects flatten to dotted keys; XML attributes and text become
  `element.attr` / `element` keys). `text` yields the decoded string itself and
  requires `into`. `python` calls a `@gofi.parser` function, which receives the
  input as `raw`, **empty** `fields`, and a copy of `meta`, and returns the
  parse result.
- **Output.** With `mode: merge` and no `into`, result keys are written into
  the top-level fields (overwriting equal keys). With `into`, the result is
  stored at `fields[into]` as a nested map (reach it in CEL as
  `fields["payload"]["user"]`); if `fields[into]` is already a map, the result
  merges into a copy of it. `mode: replace` makes the result the message's
  fields and requires an empty `into`.
- **What never changes.** A parse stage never alters the raw frame or `meta`.
- **Ordering.** Merging into the top-level fields overwrites keys that earlier
  stages set. When the parsed data is untrusted and earlier stages add
  routing or tenant fields, use `into` so the payload cannot shadow them.
- **Limits.** A stage's input (after `from`) and each decoder's output are
  capped at 1 MiB; a stage has at most 16 decoders and a flow at most 128
  stages. A reference to another flow's GoFi-managed inline library
  (`flow-…`) or a scratch run (`run-…`) is rejected.
- **Errors.** With `onError: continue` (default) the message passes on
  unchanged. With `drop` it is rejected (`droppedBy` is the stage ID) and goes to
  the dead-letter sink. A `@gofi.parser` that rejects the input (returns
  `None`) always drops. Either way the error is reported as a bulletin on
  component `stage:<id>`, counted in `gofi_stage_errors_total{listener,stage}`,
  and drops count in `gofi_messages_rejected_total{stage="<id>"}`.
- **Simulate and debugging.** Simulate and test cases trace each parse stage
  (`kind: parse`, `type` = the format, fields after the stage). The debugger
  can stop at a parse stage's Python parser (step `<id>/parser`) or Python
  decoder (`<id>/decoder:<i>`) with the exact bytes it receives.

### Layered data example

A syslog line carries a JSON message, and one field of that JSON is
gzip-compressed, base64-encoded JSON:

```
<34>1 2026-10-17T12:00:00Z web-1 auth - - {"user":"ana","blob":"H4sIAAAA…"}
```

```yaml
spec:
  python: { library: parsers }
  source: { listen: { network: udp, port: 5514 } }
  framing: { mode: line }
  parser:
    format: python
    python: { function: parse_syslog }       # -> host, app, message
  stages:
    - id: message                            # the JSON inside the syslog line
      kind: parse
      parse: { from: 'fields["message"]', format: json }
    - id: blob                               # base64-gzip JSON inside the JSON
      kind: parse
      parse:
        from: 'fields["blob"]'
        decoders: [base64, gzip]
        format: json
        into: payload
        onError: drop
    - id: admins
      kind: filter
      type: cel
      expression: 'fields["payload"]["role"] == "admin"'
  sinks: [{ type: stdout }]
```

```python
import re
import gofi

SYSLOG = re.compile(r"^<\d+>\d \S+ (?P<host>\S+) (?P<app>\S+) \S+ \S+ (?P<message>.*)$")

@gofi.parser
def parse_syslog(raw, meta=None, **config):
    match = SYSLOG.match(raw.decode())
    return match.groupdict() if match else None   # None rejects the frame
```

After the three steps the message's fields are `host`, `app`, `message`,
`user`, `blob`, and `payload` (`{"role": "admin", …}`).

## Sources

| Source | Data arrives by | Runtime notes |
| --- | --- | --- |
| `listen` | Senders connect to a GoFi TCP/UDP port. | Existing listener runtime. A Kubernetes Service/NodePort is optional. Python framers and decoders replace the former ingress hook. |
| `listen` (`network: quic`) | Senders open a QUIC connection (TLS 1.3 over UDP on `port`) and one or many streams. | Each accepted stream is one byte stream through the pipeline, framed like one TCP connection. Without `tls` the flow serves an ephemeral self-signed certificate; `tls.{cert,key,ca,requireClientCert,alpn}` configure it. Kubernetes exposure is a UDP Service. See [quic.md](quic.md). |
| `fetch.http` | GoFi requests a URL every interval. | Each response body is one frame, or one frame per element when `split` is set. Non-2xx responses and timeouts are counted, logged, and retried at the next interval. No overlapping polls. |
| `fetch.s3` | GoFi lists a bucket prefix (AWS S3 or S3-compatible: MinIO, Ceph, GCS interop) every interval. | Each new object is one frame, or one per `split` element. In-memory cursor; optional delete or move after hand-off. See [S3 and SQL fetch](#s3-and-sql-fetch-implemented). |
| `fetch.sql` | GoFi runs a read-only query (PostgreSQL, MySQL, SQL Server) every interval. | Each row is one JSON object frame. Optional incremental cursor column. See [S3 and SQL fetch](#s3-and-sql-fetch-implemented). |
| `subscribe.nats` | Core NATS subscription, optionally with a queue group. | Auth/TLS options shared with the NATS sink. No durable cursor (not JetStream) in the first cut. |
| `subscribe.mqtt` | MQTT topic subscription. | Adds an MQTT client dependency. QoS 0/1. |
| `subscribe.websocket` / `sse` | Each WebSocket message or SSE event becomes one frame. | Reconnect with capped backoff. |
| `subscribe.zenoh` | Zenoh key expression (`*`/`**` wildcards) through the router's REST plugin (SSE); each sample is one frame with `zenoh.key`/`kind`/`encoding`/`timestamp` meta. | PUT samples by default (`kinds` adds DELETE); optional `query` reads storages on connect. REST, not the native 7447 transport — see [zenoh.md](zenoh.md#native-transport-assessment). |
| `subscribe.kafka` | Kafka consumer group on one or more topics (franz-go). | Offsets committed after each record's pipeline run. SASL PLAIN/SCRAM, TLS. See [Broker sources](#broker-sources-implemented). |
| `subscribe.amqp` | AMQP 0-9-1 (RabbitMQ) queue, or an exchange bound to a temporary queue. | Manual ack after the pipeline run; `prefetch` bounds in-flight deliveries. |
| `subscribe.jetstream` | NATS JetStream durable pull consumer. | Explicit ack after the pipeline run; redelivery after `ackWait`. NATS sink auth. |
| `subscribe.redis` | Redis Stream consumer group (XREADGROUP/XACK), or Pub/Sub channels/patterns. | Streams ack after the pipeline run and recover pending entries; Pub/Sub is at-most-once. |
| `python` | `@gofi.fetcher(every="10s")` returns bytes, a dict, or a list of these; `@gofi.streamer` yields them. | Runs in the library's isolated worker and requires that library's egress allowlist. Never runs in the controller. See [Python sources](#python-sources-implemented). |

Pulled sources need outbound network from the flow runtime (the
`gofi-listener-*` Pod or container), never from the controller. On UDS, the
flow's runtime needs an egress allowance for its source endpoint (a
`network.allow` Egress rule for `gofi.io/managed-by: "true"` Pods, to the
remote namespace/selector, CIDR, or `Anywhere` for an external URL); without
one the source logs connection errors and keeps retrying. Delivery is
at-most-once: GoFi keeps no durable cursor or outbox, as documented in the
README. A `python` source is the exception: its function connects out from the
library's worker, so the library's egress rules apply and the flow runtime
needs no source egress (it only calls the worker in GoFi's namespace).

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
- `subscribe.zenoh`: `url` (router REST origin only), `keyExpr`, `kinds`,
  `query`, `headers`, and `auth`; payload decoding, meta, and key rules are in
  [zenoh.md](zenoh.md).
- `subscribe.kafka`, `amqp`, `jetstream`, `redis`: see
  [Broker sources](#broker-sources-implemented).
- Streams reconnect with exponential backoff from 1 s to 30 s.
- Metrics: `gofi_source_events_total{listener,source,outcome}` (`ok`, `error`,
  `reconnect`) and `gofi_source_last_success_timestamp_seconds`, alongside the
  existing per-frame counters. Live traffic shows each message's origin
  (`fetch:<url>`, `nats:<subject>`, `mqtt:<topic>`, …).
- Fetch and subscribe flows publish no port: Docker binds none and Kubernetes
  creates no Service.

### Broker sources (implemented)

Kafka, AMQP, JetStream, and Redis Streams acknowledge each message only
after `emit` returns, and `emit` runs the whole pipeline synchronously:
frame, decode, parse, stages, and every sink send (or the dead-letter send).
Precisely:

- **Into the pipeline: at-least-once.** A message whose run never finished
  (crash, Pod restart, rebalance, disconnect, ctx cancelled mid-run) is not
  acknowledged and is redelivered (Kafka: from the last committed offset;
  AMQP: requeued by the broker when the channel closes; JetStream: after
  `ackWait`; Redis: stays in the group's pending list). Pipelines and sinks
  may therefore see duplicates; use `meta["kafka.offset"]`, `amqp.messageId`,
  `jetstream.sequence`, or `redis.id` to de-duplicate downstream.
- **To sinks: still at-most-once.** A finished run is acknowledged even when
  a sink send failed or the message was dropped by a stage or parser (that is
  what the dead-letter sink is for). GoFi keeps no outbox or retry queue.
- Kafka commits marked offsets with franz-go's autocommit (every 5 s), before
  a rebalance revokes partitions, and on shutdown; a crash between a run and
  the next commit redelivers up to ~5 s of records. Messages over 16 MiB are
  counted as `error` and skipped (Kafka: marked; AMQP: rejected without
  requeue, so a dead-letter exchange receives it; JetStream: terminated;
  Redis: acked).

Each message is one frame. Per-message metadata is added to `meta` (visible to
CEL and Python as `meta[...]`), and live traffic shows the origin:

| Source | Origin | Meta |
| --- | --- | --- |
| `kafka` | `kafka:<topic>` | `kafka.topic`, `partition`, `offset`, `key`, `timestamp`, `kafka.header.<name>` |
| `amqp` | `amqp:<queue>` or `amqp:<exchange>/<routingKey>` | `amqp.exchange`, `routingKey`, `redelivered`, `messageId`, `contentType`, `correlationId`, `type`, `appId`, `timestamp`, `amqp.header.<name>` |
| `jetstream` | `jetstream:<subject>` | `jetstream.subject`, `stream`, `consumer`, `sequence`, `delivered`, `timestamp`, `jetstream.header.<name>` |
| `redis` | `redis:<stream>` or `redis:<channel>` | `redis.stream`, `redis.id` (streams); `redis.channel`, `redis.pattern` (Pub/Sub) |

**`subscribe.kafka`**: `brokers` (`host:port` seeds), `topics`, `group`
(default `gofi-<flow>`; replicas in one group share partitions), `startOffset`
(`latest` default, or `earliest`; only for a group without committed offsets),
`clientId`, `tls` (`enabled`, `ca`, `cert`/`key`, `insecureSkipVerify`), and
`sasl` (`PLAIN`, `SCRAM-SHA-256`, `SCRAM-SHA-512` with `username`/`password`).
Azure Event Hubs' Kafka endpoint works with `tls.enabled`, `sasl.mechanism:
PLAIN`, username `$ConnectionString`, and the connection string as password.

**`subscribe.amqp`**: `url` (`amqp://` or `amqps://`, vhost as the path),
then either `queue` (an existing queue; replicas share it) or `exchange` with
`routingKey` (GoFi declares a server-named, exclusive, auto-delete queue and
binds it: every replica gets every message, and nothing is kept while
disconnected). `prefetch` (default 100), `username`/`password` (override URL
userinfo), `tls` (with `amqps://`).

**`subscribe.jetstream`**: `url`, `stream`, `consumer` (durable name,
default `gofi-<flow>`; created or updated, so replicas sharing it share the
stream), `subject` (filter), `deliverPolicy` (`all` default, `new`, `last`,
`last_per_subject`; fixed when the consumer is created), `ackWait` (default
`30s`, 1 s to 1 h; a run longer than this is redelivered), and the NATS sink's
`auth`.

**`subscribe.redis`**: `url` (`redis://` or `rediss://`, optional
`user:password@` and `/db`), `username`/`password`, `tls`. Stream mode:
`stream`, `group` (default `gofi-<flow>`, created with MKSTREAM), `consumer`
(default the runtime hostname), `start` for a new group (`latest` or
`earliest`), and `field` (emit one entry field; default all fields as a JSON
object). On connect the consumer re-reads its own pending entries, and every
minute it claims entries other consumers left pending for 5 minutes
(XAUTOCLAIM), so a replaced Pod's in-flight entries are not stranded.
Pub/Sub mode: `channels`, with `pattern: true` for PSUBSCRIBE globs.

On UDS each endpoint gets an egress allow (`amqps://`, `rediss://`, and
Kafka with TLS are allowed by host name; plain TCP to an external host needs
`Anywhere` on the port). Only Kafka's seed brokers are known: brokers a
cluster advertises on other hosts need their own allowance. Credentials are
inline in the flow for now; Kubernetes Secret references are planned.

Not yet implemented: Google Pub/Sub, AWS SQS and Kinesis (each needs a large
cloud SDK; SQS fits the AMQP ack model, Kinesis needs a checkpoint store),
and Kafka topic regex subscriptions.

### S3 and SQL fetch (implemented)

Both follow `fetch.http`: `every` (default `30s`, minimum `1s`), `timeout`
(default `10s`, maximum `5m`), one poll at a time, failures counted under
`gofi_source_events_total{source="s3"|"sql"}`, logged, and retried at the next
interval.

**`fetch.s3`** (minio-go client): `endpoint` (blank for AWS S3; `http(s)://host:port`
for MinIO/Ceph/GCS interop, no path), `region` (default `us-east-1`),
`bucket`, `prefix` (listed recursively), `glob` (`path.Match` on the key after
`prefix`; `*` does not cross `/`), `pathStyle`, `tlsCa`, `insecureSkipVerify`,
`maxObjects` per poll (default 100, max 10000), `since` (RFC 3339),
`deleteAfter` or `moveTo` (a prefix outside `prefix`). `timeout` applies to
each request (the listing, and each object's read, copy, or delete). Origin:
`s3://<bucket>/<key>`.

- Credentials: `accessKeyId` + `secretAccessKey` (+ optional `sessionToken`)
  inline, or omit them for ambient credentials: `AWS_*`/`MINIO_*` environment,
  then IRSA web identity (`AWS_WEB_IDENTITY_TOKEN_FILE`, `AWS_ROLE_ARN`), then
  instance/container metadata; with none of these requests are anonymous.
- Objects larger than 16 MiB, and objects whose `split` fails, are logged,
  counted as errors, and skipped (not retried); the rest of the poll continues.
  Use `framing.mode: line` for one frame per line of an object.
- **Cursor (default, keep objects):** in memory only. Objects are handed off in
  (LastModified, key) order and an object is new when it sorts after the last
  one handed off. A restart re-reads the whole prefix (at-least-once across
  restarts) unless `since` excludes old objects. An object that appears later
  with an older LastModified (multipart uploads report their start time;
  copies may keep times) is missed. Every poll lists the whole prefix, so keep
  prefixes small or use move/delete.
- **`deleteAfter` / `moveTo`:** the prefix is the queue: every listed object is
  read, handed to the pipeline, then deleted (or copied under `moveTo` and
  deleted). A crash or failed delete between hand-off and removal re-reads the
  object next poll: at-least-once, not exactly-once. "Hand-off" means the
  pipeline accepted the frames, not that every sink delivered them. The flow
  needs `s3:DeleteObject` (and `s3:PutObject` for `moveTo`).

**`fetch.sql`** (`database/sql` with pgx, go-sql-driver/mysql, go-mssqldb;
one open connection, kept idle up to 5 minutes): `driver` (`postgres`,
`mysql`, `sqlserver`), either `dsn` or `host`/`port`/`database`/`username`/
`password`/`tls` (`disable`, `require`, `verify-full`)/`params`, `query`,
`maxRows` per poll (default 1000, max 100000). MySQL always gets
`parseTime=true`. Origin: `sql:<driver>://<host>:<port>[/<database>]`.

- Each row becomes a JSON object with keys in column order: times as RFC 3339
  strings, NUMERIC/DECIMAL as exact JSON numbers, JSON/JSONB columns embedded,
  binary columns (and non-UTF-8 bytes) as base64, other text as strings, NULL
  as `null`, SQL Server UNIQUEIDENTIFIER as its usual string form.
- **Incremental mode:** set `cursorColumn` and `cursorStart`. The query must
  bind the last value with the driver's placeholder (`$1`, `?`, or `@p1`) and
  `ORDER BY` that column, e.g. `SELECT * FROM events WHERE id > $1 ORDER BY id`.
  After a poll the cursor becomes the cursor column of the last row read.
  `cursorStart` is bound as an integer if it parses as one, else an RFC 3339
  time, else text. The cursor lives in memory: a restart begins again at
  `cursorStart`. Use a unique, increasing column; with ties (for example a
  timestamp) rows sharing the value at a `maxRows` boundary are skipped.
  Without a cursor every poll re-reads the same rows.
- **Read-only:** use a database user that can only read the tables it needs.
  PostgreSQL and MySQL polls run in a `READ ONLY` transaction, and validation
  rejects queries that start with a write keyword (`INSERT`, `DELETE`, `DROP`,
  …). Neither is a security boundary: functions, stored procedures, and CTEs
  can still write, and SQL Server has no read-only transaction in the driver.

**Egress:** on UDS the generated allow targets the S3 endpoint (for AWS,
`<bucket>.s3.<region>.amazonaws.com`, or `s3.<region>.amazonaws.com` with
`pathStyle`) or the database host and port (parsed from `dsn` when possible;
plain TCP to an external host is `Anywhere` on that port). Ambient AWS
credentials also need STS (IRSA) or instance-metadata egress, which GoFi does
not add.

**Secrets:** credentials (`secretAccessKey`, `sessionToken`, `password`, and
`dsn`, which usually embeds a password) are inline in the flow definition like
the other sources' credentials, so they appear in the Flow resource and the
runtime's `FLOW_SPEC`. Kubernetes Secret references for source credentials are
not implemented yet.

### Python sources (implemented)

```yaml
source:
  python:
    library: radar          # a saved library (not a flow's inline library)
    function: poll          # its @gofi.fetcher or @gofi.streamer
    config: { limit: 100 }  # keyword arguments (at most 64 KiB of JSON)
    every: 30s              # fetchers only; default: the decorator's every, else 30s; at least 1s
```

- **Kind from the registry.** The flow runtime asks the library's worker
  (`ListFunctions`) whether `function` is a fetcher or a streamer, then polls
  or streams. Saving a flow checks the same thing when the library's worker is
  ready: the library must exist and be a user library, and the function must
  be a fetcher or streamer. A library that is still starting is accepted; the
  runtime retries (1 s to 30 s backoff) until the worker registers the
  function.
- **Fetchers** use the `fetch.http` poll loop: one poll at a time, failures
  (user exceptions, the library's deadline, an unreachable worker) counted and
  retried at the next interval. Each call runs under the library's
  `timeoutSeconds` (1–30 s).
- **Streamers** use the subscribe reconnect loop: the `WorkerService.Stream`
  call stays open while the generator runs; when it ends or fails, GoFi
  reconnects with exponential backoff from 1 s to 30 s. Stopping, draining, or
  replacing the flow cancels the call, and the worker closes the generator.
  The worker runs at most 8 messages ahead of the runtime (bounded
  backpressure).
- **State.** The function's `ctx.state` (a JSON dict, at most 64 KiB) is kept
  by the flow runtime in memory between polls and across stream reconnects.
  A runtime restart starts from `{}`: delivery stays at-most-once.
- **Frames and meta.** Each returned or yielded item is one frame (a dict
  becomes JSON); `gofi.Frame(data, meta={...})` adds metadata to the message.
  Live traffic shows the origin `python:<library>.<function>`.
- **Metrics.** `gofi_source_events_total{source="python"}` counts polls
  (`ok`, `error`), streamed messages (`ok`), registry lookups that fail
  (`error`), and reconnects; `gofi_source_last_success_timestamp_seconds`
  follows them.
- **Where it runs.** The function executes only in the library's worker
  (Docker container or Kubernetes Pod), under the library's egress allowlist.
  The flow runtime (in-process in binary mode, the `gofi-listener-*`
  container or Pod otherwise) resolves the worker the same way as every other
  Python step: the supervisor's bindings in-process, `GOFI_PYTHON_BINDINGS` in
  a Docker or Kubernetes runtime. Kubernetes enforces the library's egress
  with NetworkPolicy; Docker attaches a library with any egress rule to a
  network with unrestricted outbound access. The connectivity panel lists no
  source path for a Python source and explains where its egress is set.

The worker RPCs are `WorkerService.Fetch` (unary) and `WorkerService.Stream`
(server streaming) in `proto/gofi/v1/sandbox.proto`. Decorator details and
examples are in [python.md](python.md#python-sources-fetchers-and-streamers).

## Capacity, queues, and replicas

Connection caps, UDP buffers and kernel-drop accounting, sink queues with
drop policies, retries, circuit breakers and the on-disk spool, the
per-message decoded-bytes budget, and what each source does on more than one
replica are described in [data-plane.md](data-plane.md).

## Compatibility

- `parser.decoders` / `parser.maxDecodedBytes` are read as `spec.decode` (see
  [Decode step](#decode-step)).
- `kind: Listener` resources (CRD and file store) keep loading. They convert
  to a Flow with a `listen` source: `source.{network,port,service}` becomes
  `source.listen`. (The flat `source.python` hook was removed in R1;
  `source.python` is now the R4 Python source.)
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
2. **R2 (implemented):** `fetch.http` and `subscribe.nats`; later `fetch.s3`
   and `fetch.sql`.
3. **R3 (implemented):** `subscribe.mqtt`, `websocket`, `sse`.
   Broker sources with acknowledgement after the pipeline run: `subscribe.kafka`,
   `amqp`, `jetstream`, `redis` (implemented).
4. **R4 (implemented):** Python `@gofi.fetcher` / `@gofi.streamer` with egress
   allowlists. See [Python sources](#python-sources-implemented).

Each phase ships with Simulate and Test-flow support, live traffic, metrics per
source (polls, errors, last success, reconnects), and docs.
