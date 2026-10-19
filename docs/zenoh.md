# Zenoh integration (source and sink)

GoFi integrates with [Zenoh](https://zenoh.io/) in both directions through the
router's [REST plugin](https://zenoh.io/docs/apis/rest/):

| Direction | GoFi config | REST operation | Zenoh operation |
| --- | --- | --- | --- |
| Into a flow | `source.subscribe.zenoh` | `GET /<keyExpr>` with `Accept: text/event-stream` (SSE) | `declare_subscriber(keyExpr)` |
| Into a flow (optional) | `source.subscribe.zenoh.query: true` | `GET /<keyExpr>` with `Accept: application/json` | `get(keyExpr)` (storages) |
| Out of a flow | sink `type: zenoh` | `PUT /<key>` (default) or `DELETE /<key>` | `put(key, value)` / `delete(key)` |

Both directions need the router's HTTP REST endpoint (port 8000 by default).
They do not use the native Zenoh transport on port 7447. See
[Native transport](#native-transport-assessment) for the reasoning.

## Source: `subscribe.zenoh`

```yaml
source:
  subscribe:
    zenoh:
      url: http://zenoh-router.iot.svc.cluster.local:8000
      keyExpr: demo/sensors/**
      kinds: [PUT]          # default; add DELETE to receive deletions
      query: false          # true: also read stored values on each (re)connect
      auth: { type: bearer, token: example }
      headers: { X-Tenant: demo }
```

- `url` is the router origin. It must be `http://` or `https://`, without a
  path, query, fragment, or URL credentials. Put credentials under `auth`.
- `keyExpr` may use whole-chunk wildcards: `*` matches one chunk and `**`
  matches any number of chunks. GoFi rejects the admin space and verbatim
  chunks (`@`), `$*` partial-chunk wildcards, selector and query syntax (`?`,
  `#`), percent-encoded paths, `.`/`..` and empty segments, control characters,
  and `**/**`. Use Zenoh 1.x routers. In 1.x, `@` chunks are verbatim, so `*`
  and `**` never match admin keys. On 0.x routers, a leading wildcard such as
  `**` could also match `@/...`, and with `query` the flow could read router
  admin data. On any router, deny the GoFi principal access to `@/**` with
  router ACLs.
- Credentials in `auth` travel in clear over `http://`. Use `https://` for
  routers outside the cluster.
- `auth` and `headers` have the same shape as in the HTTP and Zenoh sinks:
  bearer, basic, or API-key auth.
- `kinds` filters samples by kind, `PUT` and/or `DELETE`. The default is `PUT`
  only.
- With `query: true`, GoFi queries the storages each time the stream opens,
  after the subscriber is declared. Every reply becomes a `PUT` frame with
  `meta["zenoh.query"] == "true"`. Because the query runs after the
  subscription opens, no update falls in a gap, but a value may arrive twice.
  A failed query is logged and counted, and the subscription continues.

The router sends each sample as an SSE event. The event name is the sample kind,
and the data is a JSON sample with `key`, `value`, `encoding`, and `timestamp`.
Each sample becomes one frame, as with the other subscribe sources; keep
`framing.mode: raw` for exactly one frame per sample. GoFi recovers the payload
bytes from the plugin's JSON as follows:

| Sample encoding | Plugin sends `value` as | Frame bytes |
| --- | --- | --- |
| `application/json`, `text/json`, `text/json5` | the parsed JSON | the JSON text |
| `text/plain`, `zenoh/string` | a string | the UTF-8 string |
| anything else (`zenoh/bytes`, `application/octet-stream`, …) | base64 | the decoded bytes |
| empty payload | `null` | empty |

The plugin itself falls back to base64 when a text sample is not valid UTF-8 or
a JSON sample is not valid JSON. GoFi cannot tell those cases apart, so such a
sample arrives as its base64 string.

A `DELETE` sample has no value, so its frame is the JSON document
`{"key": "...", "kind": "DELETE", "timestamp": "..."}`.

Each frame carries this metadata, which CEL expressions can read (for example
`meta["zenoh.key"].startsWith("demo/sensors/a")`):

| Meta | Value |
| --- | --- |
| `zenoh.key` | The concrete key of the sample. |
| `zenoh.kind` | `PUT` or `DELETE`. |
| `zenoh.encoding` | The Zenoh encoding, which plays the role of a content type. |
| `zenoh.timestamp` | The router's HLC timestamp, when it supplies one. |
| `zenoh.query` | `"true"` for frames from the optional storage query. |
| `source` | `zenoh:<keyExpr>`, also shown in live traffic. |

The stream reconnects with the subscribe sources' capped exponential backoff
(1 s to 30 s). Metrics are reported under `source="zenoh"`. Router redirects
are refused. Delivery is at-most-once: samples published while GoFi is
disconnected are lost unless `query` reads them back from a storage.

## Sink: `type: zenoh`

```yaml
sinks:
  - type: zenoh
    url: http://zenoh-router.apps.svc.cluster.local:8000
    subject: applications/payments/events
    bodyMode: raw
    contentType: application/json
    when: fields["app"] == "payments"
```

`url` follows the same origin rules as the source. `subject` is one concrete
Zenoh key, so wildcards are rejected along with everything the source rejects.
These checks follow the concrete-key restrictions described in
[Zenoh abstractions](https://zenoh.io/docs/manual/abstractions/) and add path
restrictions so that publications stay inside the selected key.

`method` selects the operation. It defaults to `PUT`. `method: DELETE` removes
the key (`delete`), and no body is sent. Any other method is rejected.
`bodyMode: raw` is the default and preserves the original frame bytes. With
`bodyMode: wrapped`, the sink publishes the JSON object that contains `fields`
and `meta`; set `contentType: application/json` for it. The content type becomes
the Zenoh encoding and defaults to `application/octet-stream`.

The sink supports the same `auth` (bearer, basic, or API key), static `headers`,
and CEL `when` routing condition as HTTP sinks. Each request has a ten-second
timeout. Redirects fail rather than publish to a different key. Router errors
are reported as sink failures. GoFi does not retry and has no durable outbox.

The sink publishes to one fixed key. Templated keys, where the key is built
from fields of each message, are not supported. A per-message key would let
message content choose what GoFi writes or deletes. That would require the same
validation at runtime, plus an allowed prefix. To publish to several keys, use
several sinks with `when` conditions.

## Networking

In Kubernetes, the flow runtime connects out to the router's REST port; the
controller never does. With UDS-managed allows, GoFi adds an Egress allow for the
source's `url` (or the sink's), as it does for other pulled sources. A
`*.svc.cluster.local` host maps to a namespace allow, an IP maps to a CIDR, and
any other external host maps to an HTTP or TLS host allow. Use a Service DNS
name for an in-cluster router. In Docker or binary mode, use an HTTP address
that the flow runtime process can reach.

## Native transport assessment

The question is whether GoFi should speak the Zenoh protocol directly over
TCP, UDP, or QUIC on port 7447, rather than go through the REST plugin. The
recommendation is to stay on REST and not implement the native transport now.

| | REST plugin (current) | Native via `zenoh-go` (CGO over zenoh-c) | Pure-Go client |
| --- | --- | --- | --- |
| Availability | Ships with `zenohd`; it must be enabled in the router config. | Official binding since Zenoh 1.9 (2026). It needs zenoh-c headers and libraries built with the unstable API. | None maintained. `atolab/zenoh-go` was archived in 2020 and predates the 1.x protocol. |
| Performance | One HTTP request per published sample. Subscriptions use a single long-lived SSE stream, but values are JSON with base64 for binary. Fine at thousands of samples/s; per-message overhead dominates for small samples. | Zero-copy, batched, binary wire protocol; peer or client mode; shared memory. One to two orders of magnitude lower per-sample overhead. | Same as native, in principle. |
| Image impact | None. GoFi stays a static `CGO_ENABLED=0` binary on `scratch`. | CGO and a libc. The image would leave `scratch` (or need a fully static musl build of zenoh-c, ~10-20 MB more), plus a Rust toolchain in the build and a C/Rust supply chain to scan. | None, but GoFi would own a protocol implementation. |
| Airgap / Zarf | Nothing extra to mirror. | zenoh-c release artifacts or a Rust build with vendored crates must be mirrored and pinned per architecture. | Nothing extra. |
| Features | Pub/sub, `get` against storages, delete. No QoS, priority, or congestion control, and no liveliness. | Everything, including QoS, reliability, priorities, liveliness, and queryables. | Whatever gets implemented. |
| Security surface | Ordinary HTTP egress; reuses GoFi's HTTP auth and policy classification. | Zenoh TLS, mTLS, and user/password over a non-HTTP port; UDS needs a TCP (or UDP/QUIC) allow on 7447. | Same as native. |

### Recommendation

Keep REST as the supported transport. It covers GoFi's model, where each
sample is one frame, delivery is at-most-once, and every integration produces
one HTTP-style egress allow. It also keeps the scratch-image and airgap
guarantees.

Revisit native support if a deployment needs any of the following:

- sustained rates of more than about 5-10k samples/s per flow
- binary payloads where base64 inflation matters
- Zenoh QoS or priority
- routers that cannot enable the REST plugin

If that happens, add the transport as a separate opt-in `gofi-zenoh` runtime
image built with CGO, with zenoh-c pinned and vendored, rather than linking CGO
into the main binary. The `subscribe.zenoh` and sink configuration would gain a
`transport: native` field that keeps the same key rules and meta. Do not adopt
an unmaintained pure-Go client.
