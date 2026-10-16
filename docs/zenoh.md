# Zenoh REST router sink

GoFi publishes to a Zenoh router through its REST plugin using `PUT /<key>`.
The payload becomes the Zenoh value and `Content-Type` describes its encoding,
as specified by the [Zenoh REST API](https://zenoh.io/docs/apis/rest/).
This sink requires the router's HTTP REST endpoint; it does not use the native
Zenoh transport on port 7447.

```yaml
sinks:
  - type: zenoh
    url: http://zenoh-router.apps.svc.cluster.local:8000
    subject: applications/payments/events
    bodyMode: raw
    contentType: application/json
    when: fields["app"] == "payments"
```

`url` is the router origin, without a path, query, fragment, or URL credentials.
`subject` is one concrete Zenoh key. GoFi rejects empty segments, traversal
segments, wildcards, query syntax, encoded paths, and admin-space keys. These
checks follow the concrete-key restrictions described in
[Zenoh abstractions](https://zenoh.io/docs/manual/abstractions/) and add path
restrictions to keep publications inside the selected key.

The operation is always `PUT`. `bodyMode: raw` is the default and preserves
original frame bytes, including their original encoding. `bodyMode: wrapped`
publishes the JSON object containing `fields` and `meta`. Set
`contentType: application/json` for wrapped JSON values. The default content type
is `application/octet-stream`.

The sink supports the same `auth` (bearer, basic, or API key), static `headers`,
and CEL `when` routing condition as HTTP sinks. Supply credentials for the REST
router or its authentication proxy. Each request has a ten-second timeout;
redirects fail instead of publishing to a different key. Router errors are
reported as sink failures. GoFi does not retry or maintain a durable outbox.

In Kubernetes, allow the flow runtime's egress to the router's REST port and use a
Service DNS name for the router. In Docker or binary mode, use an HTTP address
reachable from the flow runtime process.
