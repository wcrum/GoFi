# Credential redaction in the API

Date: 2026-10-17. Scope: what the management API shows of the credentials a
flow definition carries, and where those credentials still appear in clear.

## What a flow carries

A `FlowDefinition` is self-contained: every credential it needs is inline,
never a path (see `spec.NATSAuth` and `spec.TLSConfig`), because the
definition travels as one JSON document (`FLOW_SPEC`) to whichever process
runs it. That is what lets a flow run in a container or Pod without sharing
the controller's filesystem, and it is also why reading a flow back used to
return private keys and passwords in clear.

Secret-bearing fields today:

| Where | Fields |
| --- | --- |
| `source.listen.tls`, `sinks[].tls` (`spec.TLSConfig`) | `key` (PEM private key) |
| `source.subscribe.{kafka,amqp,redis}.tls` (`spec.SourceTLS`) | `key` |
| `sinks[].auth`, `deadLetterSink.auth`, `source.fetch.http.auth`, `source.subscribe.{websocket,sse,zenoh}.auth` (`spec.HTTPAuth`) | `token`, `password`, `apiKey` |
| `sinks[].natsAuth`, `source.subscribe.{nats,jetstream}.auth` (`spec.NATSAuth`) | `password`, `token`, `nkeySeed`, `creds`, `tlsKey` |
| `source.subscribe.kafka.sasl` | `password` |
| `source.subscribe.{mqtt,amqp,redis}` | `password` |
| `source.fetch.s3` | `secretAccessKey`, `sessionToken` |
| `source.fetch.sql` | `dsn`, `password` |
| `sinks[].aster` | `token` |
| any `headers` map | values of `Authorization`, `Proxy-Authorization`, `X-API-Key`, `Api-Key`, `X-Auth-Token`, `Cookie` |
| any `url` | the password in `scheme://user:password@host` |

## What is redacted (`spec.Redact`)

Every read response replaces those values with the sentinel `***`:
`ListFlows`, `GetFlow`, `UpsertFlow`'s echo of the saved flow,
`ListFlowRevisions`, `GetFlowRevision`, and `RollbackFlow`'s returned revision.
Non-secret neighbours (usernames, certificates, CA bundles, header names, the
rest of a URL) are shown as stored. `Redact` works on the JSON shape by key
name, so a new field named `password`, `token`, `apiKey`, `secretAccessKey`,
`sessionToken`, `dsn`, `nkeySeed`, `creds`, or `tlsKey`, or a `key` inside a
`tls` object, is covered without a code change; a credential under any other
name is not, and must be added to `secretKeys` in `spec/redact.go`.

A client that edits what it was shown sends the sentinel back. `UpsertFlow`,
`Simulate`, and `DebugFrame` (`spec.MergeRedacted`) replace each sentinel with
the value at the same position in the stored flow, so saving a redacted read
changes only what the client edited. Positions are structural: the second
sink's password comes from the stored second sink, so reordering sinks while
leaving their credentials redacted moves credentials between them. A sentinel
with nothing stored behind it (a new flow, a new sink) is refused with
`InvalidArgument` naming the field, never saved as the literal `***`. A
credential whose real value is `***` cannot be set through the API.

Revision diffs cannot show that a credential changed between two revisions:
both sides read `***`.

## What is not redacted (the remaining gap)

The redaction is a presentation layer. The real values are still present, in
clear, in:

- **The desired-state store.** Binary and Docker: `listeners.yaml` (mode
  0600) with its `.bak` and `.bak.1` beside it, and the revision files under
  `history/`. Kubernetes: the `Flow` and `FlowRevision` objects; anyone with
  `get` on those resources reads every credential, and etcd holds them unless
  encryption at rest is on.
- **`FLOW_SPEC`.** Each runtime receives its definition as one environment
  variable. `docker inspect` on the flow container, or `get pod -o yaml` on
  the Pod, shows it. Anything that captures process environment (crash dumps,
  node debugging) sees it too.
- **Client input.** `UpsertFlow`, `Simulate`, `DebugFrame`, and
  `AnalyzeConnectivity` requests carry the definition in clear over the API
  connection; the API is served over plain HTTP by default and relies on the
  deployment (ingress, mesh, local socket) for transport security.
- **Logs.** The controller does not log definitions, and broker sources
  strip userinfo from the URLs they log, but a flow's own stdout sink or a
  Python step can print anything the definition contains.

Closing the gap means moving credentials out of the definition: a reference to
a Kubernetes Secret or a controller-held secret store resolved at runtime
start, with the store, history, and `FLOW_SPEC` carrying only the reference.
That changes the resource contract and the runtime hand-off and is not part of
this quick win.
