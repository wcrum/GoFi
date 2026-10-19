# gofictl: the GoFi command line

`gofictl` manages a running GoFi controller over the same ConnectRPC API the
portal uses (`GoFiService`, `OperationsService`, `FlowHistoryService`) and
reads throughput from the controller's `/metrics` endpoint. It needs no UI:
in binary mode the controller, a YAML file of Flow resources, and `gofictl`
are the whole system.

```sh
task cli:build          # or: GOWORK=off CGO_ENABLED=0 go build -o bin/gofictl ./cmd/gofictl
```

The controller address is `--server URL` (anywhere on the command line), the
`GOFI_SERVER` environment variable, or `http://127.0.0.1:9090`.

## Binary-mode quickstart

1. Start the controller. `GOFI_CONFIG_FILE` is the file of Flow resources it
   loads on start and keeps up to date; it is created on the first save when it
   does not exist. Without `GOFI_CONFIG_FILE` the file is
   `gofi/listeners.yaml` under the user configuration directory.

   ```sh
   GOWORK=off CGO_ENABLED=0 go build -o bin/gofi .
   GOFI_DRIVER=inprocess GOFI_CONFIG_FILE=./flows.yaml INGRESS_API_ADDR=127.0.0.1:9090 ./bin/gofi
   ```

   Flows already in the file run as soon as the controller is ready
   (`listener bound` in the log). The file is also where portal and `gofictl`
   saves land, so it stays the declarative source of truth. History is kept in
   a `history/` directory next to it.

2. Apply Flow resources. A file may hold one `kind: Flow`, a `kind: List`
   (`apiVersion: v1`) or `kind: FlowList` of flows, or several documents
   separated by `---`. Decoding is the file store's: strict (an unknown key is
   an error), `metadata.name` names the flow, and the retired `kind: Listener`
   is rejected with a pointer to [flows.md](flows.md).

   ```yaml
   apiVersion: gofi.wcrum.dev/v1alpha1
   kind: Flow
   metadata:
     name: telemetry
   spec:
     source:
       listen: { network: tcp, port: 10001 }
     framing: { mode: line }
     parser: { format: json, errorPolicy: drop }
     stages:
       - { id: only-warm, kind: filter, type: cel, expression: "fields.temp >= 21" }
     sinks:
       - { type: stdout }
       - { type: http, url: http://127.0.0.1:8080/events, method: POST }
   ```

   ```sh
   gofictl apply -f telemetry.yaml
   flow/telemetry created
   gofictl apply -f telemetry.yaml
   flow/telemetry unchanged
   ```

   Each flow prints `created`, `configured`, or `unchanged`. A flow the
   controller rejects prints its validation error and `apply` exits 1 after
   trying the others. `--dry-run` runs the controller's checks without saving;
   `-m` records a revision message.

3. Send data and watch it.

   ```sh
   printf '{"id":"a","temp":22}\n' | nc 127.0.0.1 10001
   gofictl get flows
   gofictl top
   ```

## Commands

| Command | Does |
| --- | --- |
| `apply -f FILE [-f FILE...] [--dry-run] [-m MSG]` | Create or update every Flow in the files (`-` reads stdin) |
| `get flows [-o table\|wide\|yaml\|json\|name]` | List flows: NAME, SOURCE, PORT, STATE, PHASE, RESTARTS, RUNTIME (`wide` adds SINKS, AGE, MESSAGE) |
| `get flow NAME [NAME...] [-o yaml\|json]` | Print a flow as a Flow resource (YAML by default) |
| `delete flow NAME [NAME...]` | Remove flows |
| `start NAME`, `stop NAME`, `restart NAME` | Change the declared run state, or recreate the runtime; `--wait 10s` waits for the phase |
| `history NAME` | Revisions of a flow, newest first; `--revision N -o yaml` prints that revision's definition |
| `rollback NAME REVISION [-m MSG]` | Restore a revision through the normal save path |
| `top [--interval 1s] [--no-tui] [--count N]` | Live phase and throughput of every flow |

`get flows` combines `ListFlows` (definitions) with `ListFlowStatus` (run
state): STATE is the declared state, PHASE what the controller observes
(`running`, `starting`, `stopped`, `crashed`, `failed`, `invalid`), RUNTIME
the driver's status of the runtime.

## top

`gofictl top` is a full-screen view in the style of k9s: an information
header (server, mode, flow count, total rates, controller RSS and goroutines)
with key hints, a bordered flows panel, and an optional describe panel.

```
GoFi top
Server:  http://127.0.0.1:9090                       <↑/↓ j/k> select   <enter> describe
Mode:    binary/inprocess                            <s> stop/start   <r> restart
Flows:   2 (2 running)                               <g/G> first/last   <q> quit
Rates:   80 frames/s  4.7 KiB/s  130 sinks/s  15 rejected/s
Process: RSS 61.2 MiB  goroutines 23
╭─ flows(2) ───────────────────────────────────────────────────────────────────────────────────╮
│ NAME         PHASE    FRAMES/s    BYTES/s  SINK OK/s  SINK ERR/s  REJECT/s  FRAMES     BYTES  TREND │
│ cli-events   running        40  2.4 KiB/s         50           0        15     478  28.1 KiB   ▁▇▇█ │
│ file-events  running        40  2.4 KiB/s         80           0         0     480  28.3 KiB   ▁▇▇█ │
│ TOTAL                       80  4.7 KiB/s        130           0        15     958  56.4 KiB        │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
```

Every second (`--interval`) it reads `ListFlowStatus` for the phase of each
flow and scrapes `/metrics` for the counters, and shows the change per second:

| Column | Metric |
| --- | --- |
| FRAMES/s, FRAMES | `gofi_frames_received_total{listener=<flow>}` |
| BYTES/s, BYTES | `gofi_source_bytes_total` |
| SINK OK/s, SINK ERR/s | `gofi_sink_dispatch_total` by `outcome` (all sink types summed) |
| REJECT/s | `gofi_messages_rejected_total` (parser and every stage) |
| TREND | the last 16 FRAMES/s values as a sparkline |
| RSS, goroutines | `process_resident_memory_bytes`, `go_goroutines` of the controller |

Keys: `↑`/`↓` or `j`/`k` select a flow, `g`/`G` jump to the first or last,
`Enter` (or `d`) toggles the describe panel for the selected flow (its status
and Flow resource; `PgUp`/`PgDn` scroll it), `s` stops a running flow or
starts a stopped one, `r` restarts it, `q`, `Esc`, or Ctrl-C exits. A counter
that decreases (the runtime restarted) reads as 0 for that tick. Set
`NO_COLOR` to disable colours.

When stdout is not a terminal, or with `--no-tui`, `top` prints one line per
flow and a total line per tick instead, which suits logs and scripts;
`--count N` stops after N ticks:

```
$ gofictl top --no-tui --count 2
14:31:51 flow=cli-events phase=running frames/s=39 bytes/s=2.3 KiB/s sink_ok/s=48 sink_err/s=0 rejected/s=15 frames=119 bytes=7126 sink_err=0
14:31:51 flow=file-events phase=running frames/s=39 bytes/s=2.3 KiB/s sink_ok/s=78 sink_err/s=0 rejected/s=0 frames=119 bytes=7120 sink_err=0
14:31:51 total flows=2 running=2 frames/s=78 bytes/s=4.6 KiB/s sink_ok/s=126 sink_err/s=0 rejected/s=15 rss=49.6 MiB goroutines=23
```

The per-flow counters come from the controller's own `/metrics`, so `top`
shows throughput for the in-process (binary) driver. With the Docker or
Kubernetes drivers each runtime exposes its own metrics; the controller's
scrape then carries only process gauges, and the per-flow columns stay 0.

## Notes

- `gofictl` is a plain Go program on the standard library plus GoFi's
  generated ConnectRPC clients (`internal/testkit/gofiapi`); it has no
  terminal dependency. Single-key input (arrows, `q` without Enter) uses the
  terminal's raw mode on Linux; elsewhere keys are read line by line.
- A controller that is down produces `connection refused (unavailable)`;
  `top` keeps running and shows `controller unavailable` until it returns.
- The history `SOURCE` of a `gofictl apply` is `portal`, the API's label for
  any ConnectRPC save.
