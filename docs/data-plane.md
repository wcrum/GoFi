# Data-plane capacity and resilience

How a flow runtime bounds what senders and destinations can make it hold, what
it does when a destination is slow or down, and what changes when a flow runs
on more than one replica. Tunables come from the `config` package
(`GOFI_PROFILE` presets `edge` / `default` / `large`, each overridable by the
environment variables named here; see [configuration](configuration.md)).

## Memory per connection and datagram

Framers start with a small buffer (`GOFI_FRAMER_INITIAL_BUFFER`, 64 KiB by
default, 4 KiB on the edge profile) and grow it on demand up to the frame cap
(`framing.maxBytes`, 1 MiB by default). A datagram or pulled message gets a
buffer of exactly its size. One shared pipeline per listener (parser, compiled
sink conditions, metric handles) serves every connection, datagram, and pulled
message; nothing is compiled or preallocated per stream. Measured: 1000 idle
TCP connections cost about 74 KiB each with the default profile (about 1 MiB
each before).

## Connection and rate caps

| Knob | Default | Effect |
| --- | --- | --- |
| `GOFI_MAX_CONNECTIONS` | 1024 | Concurrent TCP connections per listen source. Past the cap a connection is accepted and closed at once (the sender reads EOF); `gofi_listener_connections_refused_total` counts it and a bulletin is raised once per 10 s. |
| `GOFI_IDLE_TIMEOUT` | 0 (off); 2m on edge | Closes a TCP connection that sent nothing for this long (`gofi_listener_connections_idle_closed_total`). Senders reconnect. |
| `GOFI_UDP_READ_BUFFER` | 4 MiB | Kernel receive buffer requested for a UDP socket (`SO_RCVBUF`; the kernel clamps it to `net.core.rmem_max`, and `gofi_udp_read_buffer_bytes` shows the effective value). On Linux the socket also reports datagrams the kernel dropped for lack of buffer (`SO_RXQ_OVFL`) as `gofi_udp_kernel_drops_total`: loss the process never saw. |
| `GOFI_UDP_WORKERS` | 1 | Goroutines reading and processing datagrams. More than one processes datagrams concurrently and so **out of order**; stateful stages stay correct, ordered destinations (a TCP sink expecting sequence) do not. |
| `GOFI_SOURCE_WORKERS` | 1 | Goroutines processing pulled messages for sources that deliver at most once (NATS, MQTT, WebSocket, SSE, Redis Pub/Sub, fetch). Also out of order. Acknowledging brokers (Kafka, AMQP, JetStream, Redis Streams) always process on their consumer goroutine so an acknowledgement means the pipeline ran. |

## Sink queues

HTTP-family sinks (`http`, `loki`, `aster`, `zenoh`) deliver from a bounded
in-memory queue with one worker per sink; the pipeline goroutine only enqueues.
`stdout` writes through a 64 KiB buffer flushed every 100 ms; `tcp`/`udp`
write to a local socket with vectored writes; `nats` buffers in its client;
`python` sinks are bounded by their worker pool. These stay synchronous.

| Knob | Default | Effect |
| --- | --- | --- |
| `GOFI_SINK_QUEUE_DEPTH` | 4096 (512 edge) | Messages queued per sink. `gofi_sink_queue_depth` is the current depth. |
| `GOFI_SINK_DROP_POLICY` | `block` (`drop-oldest` on edge) | Full queue: `block` makes the pipeline wait for room (back-pressure to the source; for UDP the kernel buffer then absorbs or drops), `drop-newest` rejects the incoming message, `drop-oldest` evicts the oldest queued one. Drops count in `gofi_sink_queue_dropped_total`; waits in `gofi_sink_queue_blocked_total`. |
| `GOFI_SINK_SPOOL_DIR` | empty (off) | With a directory, overflow goes to an on-disk spool (`<dir>/<flow>/<sink route>`) instead of the drop policy, and so do messages that arrive while the circuit breaker is open. The worker drains it oldest first once the memory queue is idle. The spool survives restarts; `GOFI_SINK_SPOOL_MAX_BYTES` bounds it (then the drop policy applies: `drop-oldest` deletes the oldest segment, anything else refuses). `gofi_sink_spooled_total`, `gofi_sink_spool_depth`. |
| `GOFI_SINK_BATCH_MAX`, `GOFI_SINK_BATCH_BYTES`, `GOFI_SINK_BATCH_INTERVAL` | 256, 1 MiB, 1s | Loki pushes one request per batch (one stream, many values); a partial batch waits at most the interval. `gofi_sink_batch_size`. |
| `GOFI_SINK_RETRIES`, `GOFI_SINK_RETRY_INITIAL_BACKOFF`, `GOFI_SINK_RETRY_MAX_BACKOFF` | 3, 200ms, 30s | Transient failures (network errors, HTTP 429 and 5xx) are retried with exponential backoff and jitter; 4xx and encoding errors are not. `gofi_sink_retries_total`. A retry may deliver a message the destination received but failed to acknowledge. |
| `GOFI_SINK_BREAKER_FAILURES`, `GOFI_SINK_BREAKER_COOLDOWN` | 5, 10s | After this many consecutive failed deliveries the breaker opens: deliveries fail fast (or spool) without touching the destination until the cooldown passes, then one probe closes it. `gofi_sink_breaker_open`, `gofi_sink_breaker_rejected_total`. |
| `GOFI_HTTP_MAX_IDLE_CONNS` | 64 | Idle connections kept per destination host (the Go default is 2). Dial, TLS, and response-header timeouts are 5 s / 5 s / 20 s. |

Delivery outcomes of a queued sink are recorded by its worker:
`gofi_sink_dispatch_total` counts each message by outcome when its batch
completes (a batch's duration is observed once), and route health reflects the
last delivery, not the enqueue. A graceful stop (`Listener.Shutdown`) flushes
the memory queue, drains the spool while the deadline allows, then closes the
sink; what remains in the spool is delivered after the next start.

Delivery to sinks is still at most once from the source's point of view
([flows](flows.md#runtime-behavior-implemented)): a Kafka or AMQP message is
acknowledged when its pipeline run returns, which with a queued sink is when
the message was enqueued (or spooled), not delivered.

## Decoded-bytes budget

Each decoder step caps its own output (1 MiB by default), but a flow may
decode in the decode step and again in any number of parse stages. The sum of
all decoder output for one message is bounded by
`GOFI_MAX_DECODED_BYTES_PER_MESSAGE` (8 MiB). A message that would exceed it is
dropped whatever the parser's error policy or the stage's `onError` says
(`droppedBy` is `decode` or the stage ID), counted in
`gofi_decode_budget_exceeded_total`, and reported as a bulletin. Simulate and
the debugger run at most `GOMAXPROCS` dry runs at once and abandon one after
10 s.

## Running more than one replica

A flow's runtime keeps its state in memory and per process. Scaling a flow to
N replicas means N independent runtimes; what that does depends on the source:

- **listen (TCP/UDP)**: the Service spreads connections (or datagrams) across
  replicas. Rate-limit and dedup stages are **per replica**: a limit of 100/s
  on 3 replicas admits up to 300/s in total, and a duplicate arriving at a
  different replica than the original passes. Either put the stage behind a
  single replica or size the limit per replica.
- **subscribe.nats**: the subscription joins queue group `gofi-<flow>` by
  default, so each message reaches one replica. Set `queue` to name the group,
  or to `-` for plain fan-out (every replica gets every message).
- **subscribe.mqtt**: the default client ID is `gofi-<flow>-<hostname>` (the
  Pod name), because a broker disconnects the previous session when a client
  ID reconnects. Set `sharedGroup` to subscribe through an MQTT 5 shared
  subscription (`$share/<group>/<topic>`) so replicas split the topic; without
  it every replica receives every message.
- **kafka / amqp / jetstream / redis streams**: consumer groups and queues
  already split work across replicas (see [flows](flows.md#broker-sources-implemented)).
- **websocket / sse / redis pub-sub / zenoh**: every replica receives every
  message.
- **fetch (http / s3 / sql)**: every replica polls, so each response is
  emitted once per replica. `leaderOnly: true` records that only one replica
  should poll; leader election is not implemented yet, so the flag only logs a
  warning at start. Until it is, run fetch flows with one replica.
- **slow consumers**: a NATS subscription buffers at most 64 Ki messages /
  64 MiB; past that the client drops and `gofi_source_slow_consumer_total`
  counts the loss. MQTT (`paho`) has no equivalent limit: with
  `SetOrderMatters(true)` the client blocks the network reader while the
  handler runs, so the broker's in-flight window (QoS 1) or the TCP window is
  the back-pressure, and QoS 0 messages queue in the broker until it drops
  them by its own policy.
