# QUIC

GoFi accepts data over QUIC (`source.listen.network: quic`) and delivers it
over QUIC (`sinks[].type: quic`). QUIC is a first-class transport next to TCP
and UDP: the same framing, decoders, parser, stages, sinks, live traffic, and
test cases apply; only the socket changes.

## Why QUIC

- **Encrypted by construction.** QUIC runs on TLS 1.3. There is no plaintext
  mode, so a flow's data port is authenticated and confidential without a
  sidecar or a TLS terminator in front of it.
- **Multiplexed.** One connection carries many independent streams. A sender
  with many sources (one per device, tenant, or file) opens one stream each
  instead of one connection each, and a stall on one stream does not block
  the others. Each stream is framed on its own, like one TCP connection.
- **Fast reconnects.** A QUIC handshake is one round trip, and the client
  learns that the far side went away from its own idle timer rather than from
  a TCP reset that may never arrive. GoFi does **not** accept 0-RTT data
  (replayable), so a reconnect costs one round trip.
- **Lossy and edge links.** Loss on one stream is recovered on that stream
  only (no head-of-line blocking across streams), congestion control adapts
  per path, and the connection survives the client's address changing (NAT
  rebinding on a cellular or satellite link). TCP's per-connection
  retransmission stalls on the same links.

QUIC rides on UDP: the flow binds UDP `port`, a Kubernetes Service for it is
a UDP Service, and firewalls must pass UDP to that port.

## Listen source

### Default: ephemeral self-signed certificate

```yaml
apiVersion: gofi.wcrum.dev/v1alpha1
kind: Flow
metadata:
  name: telemetry-quic
spec:
  source:
    listen:
      network: quic
      port: 10443
  framing: { mode: line }
  parser: { format: json }
  sinks:
    - type: http
      url: http://collector.svc:8080/ingest
```

With no `tls` block the runtime generates a self-signed certificate at start
and logs its SHA-256 fingerprint once (`QUIC listener using an ephemeral
self-signed certificate`). The certificate changes on every restart or
redeploy, so clients either pin nothing (skip verification, suitable for a
private network or a test) or you provide a certificate as below. Data is
still encrypted; what the ephemeral certificate lacks is a stable identity a
client can verify.

### With a provided certificate and client authentication

```yaml
spec:
  source:
    listen:
      network: quic
      port: 10443
      service: { type: NodePort, nodePort: 30443 }   # Kubernetes: a UDP Service
      tls:
        cert: |
          -----BEGIN CERTIFICATE-----
          ...
          -----END CERTIFICATE-----
        key: |
          -----BEGIN PRIVATE KEY-----
          ...
          -----END PRIVATE KEY-----
        ca: |                       # optional: verify client certificates against this bundle
          -----BEGIN CERTIFICATE-----
          ...
          -----END CERTIFICATE-----
        requireClientCert: true     # reject clients without a certificate chaining to ca
        alpn: [gofi/1]              # default
```

`cert` and `key` are inline PEM text, never file paths: a flow definition is
one self-contained document that travels to whichever process runs it. On
Kubernetes, template them from a Secret with your deployment tooling (Helm
`.Files`, Kustomize secretGenerator plus a replacement, or a GitOps secret
operator) the same way as the NATS sink's `natsAuth` material. When `ca` is
set without `requireClientCert`, a client certificate is verified if the
client presents one and ignored otherwise.

### Runtime behavior

- Every accepted bidirectional stream is one byte stream through the pipeline:
  framing, idle timeout, and the connection cap work per stream exactly as for
  one TCP connection. Unidirectional streams are refused (nothing would read
  them). A message's source address is `host:port#streamID`.
- One connection may hold at most 32 concurrent streams, so one peer cannot
  take the whole budget. `GOFI_MAX_CONNECTIONS` caps QUIC connections per flow
  and, separately, the streams in flight across all of them (a stream costs
  what a TCP connection costs: a pipeline goroutine and a framer buffer); a
  surplus connection is closed with application error code 1, a surplus
  stream is reset with stream error code 1 and may be retried once the budget
  frees. Both count in `gofi_listener_connections_refused_total`.
  `GOFI_IDLE_TIMEOUT` (default 30s for QUIC) closes connections and streams
  that sent nothing for that long.
- Handshakes must finish within 5s. Flow-control windows bound what a peer can
  push before the pipeline consumes it (1 MiB per stream, 4 MiB per
  connection), so a slow sink applies backpressure to the sender instead of
  growing memory.
- 0-RTT is never accepted. Above 100 connection attempts per second from
  addresses that have not proven themselves, the listener answers new
  attempts with a Retry packet (RFC 9000 address validation) before it
  allocates handshake state: a flood of Initials with spoofed sources costs
  the attacker a round trip per attempt and cannot use GoFi as an amplifier.
  Below that rate a handshake stays one round trip.
- Stopping, restarting, or editing a flow drains like TCP: accepted streams
  finish, then connections are closed with application error code 0. Clients
  that keep sending see the close (or their idle timer) and reconnect.
- The existing per-flow metrics (`gofi_frames_received_total`,
  `gofi_listener_connections_*`, sink dispatch counters) cover QUIC flows.

## QUIC sink

```yaml
spec:
  sinks:
    - type: quic
      url: quic://collector.example.net:10443
      delimiter: "\n"              # appended after every payload; a line-framed receiver needs it
      encoding: { format: fields } # optional: send the parsed fields instead of the wrapped envelope
      tls:
        ca: |                      # verify the server against this bundle instead of the system roots
          -----BEGIN CERTIFICATE-----
          ...
          -----END CERTIFICATE-----
        serverName: collector.example.net   # SNI/verification name; defaults to the URL host
        # insecureSkipVerify: true          # only for a peer with an ephemeral certificate
```

The sink holds one long-lived connection with one long-lived stream and
writes each message followed by `delimiter`, the way a GoFi QUIC listen
source (or any stream-oriented QUIC server) reads it. Sends are synchronous
and never interleave; messages arrive in send order, and a message is
delivered whole or the stream dies (QUIC retransmits within the stream). A
failed write drops the connection; the next send redials, with exponential
backoff (200ms to 30s) once dials start failing. Like the `tcp` and `udp`
sinks, `quic` is not wrapped in the asynchronous sink queue: a write
completes into the local send buffer, and the dial backoff keeps a dead
destination from stalling the pipeline on handshake timeouts. The connection
sends keep-alives every 5s and is declared dead after 15s of silence, which
is how a destination that vanished without closing is noticed.

A QUIC write succeeds locally; the destination's receipt is confirmed only
by its acknowledgements. When a flow stops (drain) or a test case finishes,
the sink flushes: it closes its send side (FIN) and waits, within the drain
deadline, for the destination to finish its side, which a GoFi QUIC source
does once it has read everything. Only then is the connection closed, since
closing a QUIC connection discards data not yet acknowledged. The next send
after a flush opens a new stream on the same connection.

`kubernetes` selector targets work as for `tcp` and `udp`: the sink dials the
target Service GoFi publishes (fanning out to every ready Pod unless
`delivery: one`), with UDP egress allows and a UDP Service port. The TLS
server name stays the Service host (or `tls.serverName`) for every Pod, so
the receivers' certificate must carry that name, or pin their `ca` plus
`serverName`. Without `tls` the sink verifies the server against the system
roots.

Two GoFi flows chain naturally: flow A listens on QUIC and has a `quic://`
sink pointing at flow B's QUIC port; B listens on QUIC and delivers onward.
Use `encoding: { format: fields }` on A's sink so B parses the same JSON
object A received rather than A's wrapped `{id, fields, meta}` envelope.

## TLS fields

`tls` is the same object on a listen source (server) and a sink (client);
some fields apply to one side only and are rejected on the other.

| Field | Side | Meaning |
| --- | --- | --- |
| `cert` | both | Inline PEM certificate (chain). Server: the certificate it serves (default: ephemeral self-signed). Client: the client certificate presented to a server with `requireClientCert`. Set together with `key`. |
| `key` | both | Inline PEM private key matching `cert`. |
| `ca` | both | Inline PEM bundle. Server: verifies client certificates against it. Client: verifies the server against it instead of the system roots. |
| `insecureSkipVerify` | sink | Do not verify the server certificate. Cannot be combined with `ca`. Logged as a warning at start. |
| `serverName` | sink | SNI and verification name; defaults to the host in `url`. |
| `alpn` | both | Application protocols to negotiate; default `[gofi/1]`. Clients must offer one of them. |
| `requireClientCert` | source | Reject peers without a certificate chaining to `ca` (which is then required). |

Validation runs on save: mismatched cert/key, non-PEM `ca`, a source with
`insecureSkipVerify` or `serverName`, a sink with `requireClientCert`, and
`tls` on a non-QUIC listen source are all rejected.

**Secrets:** `key` (and a client `cert`/`key` on a sink) is inline in the flow
definition like the NATS sink's `natsAuth` and the fetch sources' credentials,
so it appears in the Flow resource, in the runtime's `FLOW_SPEC`, and in the
flow as the API returns it. Kubernetes Secret references for TLS material are
not implemented yet; keep Flow resources and API access as restricted as the
key they carry, and prefer a dedicated certificate per flow so a leaked key
is scoped to it.

## What a client must do

- Dial QUIC (TLS 1.3) to the flow's UDP port and offer ALPN `gofi/1` (or one
  of the flow's `alpn` values). A handshake with no matching protocol fails.
- Against a flow with no `tls` block, skip certificate verification or pin
  the fingerprint from the flow's log line. Against a provided certificate,
  verify it normally (and present a client certificate when the flow sets
  `requireClientCert`).
- Open one or many **bidirectional** streams, up to 32 per connection (a
  stream reset with error code 1 means the flow's stream budget is spent:
  retry it later). Each
  stream is framed independently with the flow's `framing` (for
  `mode: line`, newline-delimited records). Interleave nothing across
  streams; a record lives on one stream.
- To finish a stream, close your send side (FIN), then read until GoFi closes
  its side before closing the connection. Closing the connection right after
  the FIN discards data that has not been acknowledged yet. GoFi closes its
  side once it has read your EOF.
- Expect the connection to end on a flow restart or edit (application error
  code 0) and on 30s of silence; keep a connection alive with QUIC keep-alive
  pings or reconnect on demand.

### Send a test message

A self-contained Go program (needs `github.com/quic-go/quic-go`):

```go
package main

import (
	"context"
	"crypto/tls"
	"io"
	"os"
	"time"

	"github.com/quic-go/quic-go"
)

func main() {
	addr := os.Args[1] // host:port of the flow
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tlsConf := &tls.Config{NextProtos: []string{"gofi/1"}, InsecureSkipVerify: true} // ephemeral certificate
	conn, err := quic.DialAddr(ctx, addr, tlsConf, nil)
	if err != nil {
		panic(err)
	}
	defer conn.CloseWithError(0, "")
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		panic(err)
	}
	if _, err := stream.Write([]byte(`{"id":"probe-1","level":"info","msg":"hello over quic"}` + "\n")); err != nil {
		panic(err)
	}
	_ = stream.Close()                            // FIN
	_, _ = io.Copy(io.Discard, stream)            // wait for GoFi's FIN: the data was read
}
```

Save it as `main.go`, run `go mod init probe && go get github.com/quic-go/quic-go`,
then `go run . 127.0.0.1:10443`. The message appears in the flow's live
traffic and at its sinks.

In this repository, Go tests use the testkit generator, which sends
identified newline-delimited JSON over one QUIC connection with `Connections`
parallel streams and reconnects through listener restarts:

```go
gen := &traffic.Generator{Network: "quic", Addr: "127.0.0.1:10443", Prefix: "probe", Connections: 4, Reconnect: true}
res := gen.Run(ctx, 1000) // res.Sent, res.Failed, res.Reconnect, res.IDs
```

Its `QUIC traffic.QUICOptions` field overrides the defaults (skip
verification, ALPN `gofi/1`, 5s idle timeout): `CA`, `Cert`/`Key`,
`ServerName`, `ALPN`, `VerifyServer`, `IdleTimeout`, or a complete `TLS
*tls.Config`. `traffic.SendOnceQUIC` sends one payload and
`traffic.WaitListening(ctx, "quic", addr)` probes a port with a handshake.
The end-to-end scenarios in `tests/e2e/quic_test.go` (restart under load, 64
streams, QUIC to QUIC sink, edit during traffic) run with
`go test -tags e2e -run TestQUIC ./tests/e2e/`.

## Deployment notes

- **Binary and Docker Compose:** the flow binds UDP `port` on the host (Compose
  publishes it one-to-one as `port/udp`). See [deployment.md](deployment.md#binary)
  and [Docker Compose](deployment.md#docker-compose).
- **Kubernetes:** `source.listen.service` creates a UDP Service (ClusterIP,
  NodePort, LoadBalancer) or a UDPRoute for Gateway exposure, since QUIC's
  layer-4 protocol is UDP; `externalTrafficPolicy: Local` keeps the sender's
  address for connection migration and logs. Load balancers and NodePort
  ranges must allow UDP. See [Kubernetes](deployment.md#kubernetes).
- **UDS:** the generated network allows for a QUIC flow are UDP allows; a QUIC
  sink needs an egress allow to its destination like any other sink. See
  [UDS network allows for flows](deployment.md#uds-network-allows-for-flows).
- **UDP buffers:** quic-go logs `failed to sufficiently increase receive
  buffer size` when `net.core.rmem_max` is small; raise it (for example
  `sysctl -w net.core.rmem_max=7500000 net.core.wmem_max=7500000`) on hosts
  that carry high-rate QUIC flows. The flow works without it at lower
  throughput.
- **Certificates:** an ephemeral certificate is fine for a private network or
  a pilot; for anything a client must trust, provide `cert`/`key` (and `ca`
  with `requireClientCert` for mutual TLS). Rotate by editing the flow; the
  runtime is replaced and clients reconnect.
