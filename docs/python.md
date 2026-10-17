# Python function libraries

A GoFi Sandbox is a durable Python function library executed in its own worker
container or Kubernetes Pod. The controller saves source, starts the worker,
reads its registered functions through ConnectRPC, and supplies private runtime
bindings to flow runtimes. Python source is never imported into the controller.

## Create a library

Use the portal's Python libraries tool, or apply
[`examples/python-sandbox.yaml`](../examples/python-sandbox.yaml) in Kubernetes.
A library declares source and worker resource bounds:

```yaml
apiVersion: gofi.wcrum.dev/v1alpha1
kind: Sandbox
metadata:
  name: telemetry-library
spec:
  timeoutSeconds: 5
  memoryMib: 128
  sourceCode: |
    import json
    import gofi

    @gofi.parser("telemetry")
    def telemetry(raw: bytes):
        return json.loads(raw.removeprefix(b"device:"))

    @gofi.transformer("normalize")
    def normalize(fields, meta):
        return {**fields, "app": str(fields.get("app", "unknown")).lower()}

    @gofi.decoder("strip-prefix")
    def strip_prefix(data: bytes):
        return data.removeprefix(b"prefix:")

    @gofi.transformer("threshold")
    def threshold(fields, meta, limit: int = 30):
        """Keep hot readings.

        limit: minimum temperature
        """
        print("checking", fields.get("temperature"))
        return fields if fields.get("temperature", 0) >= limit else None

    @gofi.sink("debug")
    def debug(fields, meta):
        print(json.dumps(fields))
```

The editor supports Python syntax highlighting, source formatting, validation
with line/column diagnostics, and sample function execution. Format and Validate
work before a library is saved: the supervisor starts a private fixed-source
tooling worker and invokes its authenticated Ruff/AST tooling RPCs. Submitted
editor source is parsed/formatted there and is never imported or executed for
these operations. The private worker is not a public Sandbox or persisted source
resource and is cleaned up during controller shutdown. Validation checks syntax
and Ruff diagnostics without importing dependencies or verifying runtime function
registration. A valid static result therefore does not replace Save and launch
or a sample run.

`RunFunction` executes a registered function in the saved, ready library worker.
`RunSource` executes unsaved editor source: GoFi first validates it statically
(syntax errors return immediately, without a worker), then starts a dedicated,
short-lived worker with the same isolation and limits as a saved library, makes
one call, and removes the worker. At most two such runs execute concurrently;
expect a few seconds of worker startup. With an empty function name, the only
function of the requested kind is used.

Both return the function's fields, optional raw bytes (including an explicitly
empty byte string), and drop decision, plus everything the call printed to
stdout and stderr (each capped at 64 KiB) and its duration. A user-code
exception is part of the response, not an RPC failure: `error` carries the
exception and `errorLocation.line` its line in your source, alongside whatever
was printed before it failed. Deadline and crash conditions remain RPC errors.
A Python sink run is a real function invocation, so it can perform the effects
allowed inside its isolated worker.

Libraries can save sample inputs as `tests` (function, kind, raw/fields/meta,
config, and optional `expectFields`/`expectDrop`). Saving tests or other
metadata does not restart a running worker.

Watch the library's status before selecting a registered function in a flow.
A saved definition can exist while its worker is starting or failed; persistence
alone does not mean the function is available. The expanded source example is
[`examples/python-library.py`](../examples/python-library.py).

## Select functions in a flow

```yaml
source:
  listen:
    network: tcp
    port: 7000
framing:
  mode: line
parser:
  decoders:
    - type: python
      python:
        library: telemetry-library
        function: strip_prefix
  format: python
  python:
    library: telemetry-library
    function: telemetry
  errorPolicy: drop
stages:
  - id: normalize
    kind: python
    python:
      library: telemetry-library
      function: normalize
sinks:
  - type: python
    python:
      library: telemetry-library
      function: debug
```

### One decorator per flow step

Each decorator plugs into one step of a flow, in pipeline order. The flow
editor offers a library's functions only in the matching step.

| Decorator | Flow step | Receives | Returns |
| --- | --- | --- | --- |
| `@gofi.framer` | Frame (`framing.mode: python`) | `buffer: bytes`, optional `final: bool` | `(frames, rest)` |
| `@gofi.decoder` | Decode (`parser.decoders`) | `data: bytes` | `bytes` |
| `@gofi.parser` | Parse (`parser.format: python`) | `raw: bytes` | `dict`, or `None` to reject |
| `@gofi.transformer` | Stages | `fields`, optional `meta` | `dict`, or `None` to reject |
| `@gofi.encoder` | Encode (`sinks[].encoding.encoders`) | `data: bytes`, optional `fields`, `meta` | `bytes` |
| `@gofi.sink` | Sink (`type: python`) | `fields`, optional `meta` | `None` |

GoFi owns the socket: the flow's `source.listen` sets the network and port.

**Framers** split a byte stream into frames when the built-in modes (`line`,
`delimited`, `length-prefix`, `raw`) do not fit. GoFi calls the framer with
everything buffered so far and keeps the returned `rest` for the next read.
When the connection ends it calls once more with `final=True` so a trailing
partial frame can be flushed. A framer that buffers more than `framing.maxBytes`
(default 1 MiB) without producing a frame fails the connection.

```python
@gofi.framer("records")
def records(buffer: bytes, final: bool):
    *frames, rest = buffer.split(b";")
    if final and rest:
        frames, rest = frames + [rest], b""
    return frames, rest
```

**Decoders and encoders** form ordered chains. Each step is either a built-in
codec (`base64`, `base64url`, `base32`, `hex`, `url`, `gzip`, `zlib`,
`deflate`, `zstd`, `snappy`, `lz4`, `brotli`, `bzip2` (decode only)) or a Python
function, so a chain like `strip-prefix → base64` decodes a prefixed Base64
frame before parsing. On the way out, a sink's `encoding` chooses the payload
format and then applies encoders in order:

```yaml
sinks:
  - type: http
    url: http://collector:8080/ingest
    encoding:
      format: fields      # wrapped (default: id, fields, meta) | fields | raw (original frame)
      encoders:
        - type: python
          python: { library: telemetry-library, function: sign }
        - gzip            # a bare name is shorthand for { type: gzip }
```

An encoder can read `fields` and `meta` to build the payload (for example, to
add a header from a field). A sink with an `encoding` sends exactly the encoded
bytes; without one it keeps its default payload.

The `@gofi.ingress`, `@gofi.listener`, `@gofi.port`, `gofi.TCP`, and `gofi.UDP`
names have been removed. A library that still uses them fails to start, and its
status names the replacement: put the port on the Flow's Listen source and move
the hook's logic into a framer or decoder.

### Per-node configuration

Keyword parameters after the data arguments are configuration. `ListFunctions`
reports them as `params` (name, type from the annotation, default, required,
and a description taken from a `name: description` docstring line). A flow node
supplies values in `python.config`; they are passed as keyword arguments, and
unknown keys are rejected unless the function accepts `**kwargs`. Configuration
travels as JSON, so numbers arrive as floats unless the parameter is annotated
`int`. Fields and metadata are JSON values too.

```yaml
stages:
  - id: hot-only
    kind: python
    python:
      library: telemetry-library
      function: threshold
      config:
        limit: 25
```

### Inline Python stages

A processing stage can carry its Python directly instead of referencing a
library. Write one top-level `transform` function:

```yaml
stages:
  - id: enrich
    kind: python
    python:
      inlineSource: |
        def transform(fields, meta, factor: int = 2):
            print("enriching", fields.get("sensor"))
            return {**fields, "scaled": fields["value"] * factor}
      config:
        factor: 10
```

GoFi folds every inline stage of a flow into one managed library named
`flow-<flow>-<hash>` (`managedBy: flow:<flow>`), registering each snippet
as a transformer named after its stage ID. Each snippet runs in its own
namespace, so names never collide across stages, and error lines refer to the
snippet you wrote. The controller fills `python.library`/`python.function` for
you; `inlineSource` stays the source of truth, so declarative files and CRDs may
omit them. The managed library uses the same isolated workers and limits as any
library, is read-only through the library API, and is removed when the flow
is deleted or no longer has inline stages. Saving a flow validates each
snippet statically (never executing it) and rejects invalid source with
line-numbered diagnostics. Until the managed worker is ready, inline stages fail
closed like any unavailable Python function.

Simulations always run the snippet being simulated: a saved snippet reuses its
ready managed worker, and an edited, unsaved snippet runs in a short-lived
scratch worker. Simulation traces include each Python stage's stdout and
duration. Inline source is limited to processing stages (16 KiB each; 64 KiB per
flow in total); framers, decoders, parsers, encoders, and sinks reference
library functions.

Parsers receive bytes and return a dictionary. Transformers receive fields and
optional metadata and return a replacement dictionary. Framers, decoders, and
encoders must return bytes (a framer returns lists of bytes). Sinks receive fields and optional metadata;
returning `None` completes a sink call. Parser and transformer `None` results
explicitly reject the message. Explicit parser rejection always stops processing,
even with `errorPolicy: continue`. Other custom Python parser failures drop by
default; set `parser.errorPolicy: continue` explicitly if those errors should
continue into later stages. Synchronous and asynchronous functions are supported.

Worker invocation, source, and transformer failures are surfaced in flow errors
and drop attribution. Python transforms fail closed. Mock mode executes Python
framer/decoder/parser/transformer/encoder functions through their worker while withholding real
sink dispatch.

## Runtime and isolation

Binary and Compose installations use Docker workers; Kubernetes uses managed
worker Pods. Binary operation without Python functions needs no Docker daemon.
The administrator selects a fixed worker image through `GOFI_SANDBOX_IMAGE`;
library authors cannot choose arbitrary images or runtime credentials.

A saved library's worker keeps one warm Python process that loads the library
once and runs calls in order, so per-frame steps cost milliseconds rather than a
process start. Module-level state therefore persists between calls of the same
library, and the process is shared by every flow and source that uses the
library. Do not rely on that state: a call that exceeds its deadline or crashes
kills the process, and GoFi also recycles it after 10,000 calls or 10 minutes.
Do not start threads that outlive a call; their output can appear in a later
call's stdout and logs. Treat a library as one trust domain: a payload that
exploits a bug in a function (for example `pickle.loads` on received bytes)
can affect later messages from other sources until the process is recycled.
Keep untrusted sources on separate libraries, and prefer a sandboxed runtime
(gVisor or Kata) for workers that parse internet-facing traffic. Unsaved source
(`RunSource`) still runs in a fresh process per call.

Workers run as a nonroot user with a read-only root filesystem, dropped
capabilities, private temporary storage, and CPU/memory limits. Docker workers
join an internal bridge. Kubernetes workers have restricted RPC ingress and a
deny-egress NetworkPolicy; platform policies must preserve these restrictions.

### Installed packages

The worker image provides the Python standard library plus a curated set of
compute-only decoders, pinned to versions with prebuilt wheels:

| Package | Version | Use |
| --- | --- | --- |
| `msgpack` | 1.2.3 | MessagePack |
| `cbor2` | 6.1.5 | CBOR |
| `PyYAML` | 6.0.3 (`import yaml`) | YAML |
| `construct` | 2.10.70 | Declarative binary structures |
| `crc` | 8.0.0 | CRC checksums (pure Python) |

`crcmod` was not used because it publishes no wheels. Packages cannot be
installed at runtime (the root filesystem is read-only); change
`python/requirements.txt` and rebuild the image to add one. `DescribeEnvironment`
reports the Python version, these packages, the SDK decorators, the deployment's
network policy, and the timeout/memory limits.

### Outbound network (egress)

Libraries have no outbound network access by default. A library may declare
`egress` rules:

```yaml
spec:
  egress:
    - namespace: messaging          # in-cluster peers by namespace and labels
      podSelector: {app: nats}
      port: 4222
      protocol: tcp
    - cidr: 10.20.0.15              # or an address/CIDR; port 0 means any port
      port: 8443
```

On Kubernetes each rule becomes a NetworkPolicy egress rule, and DNS to
`kube-system/kube-dns` is allowed only when at least one rule exists.
NetworkPolicy cannot match hostnames, so use CIDRs or namespace/pod selectors.
Docker cannot filter by destination: a library with any egress rule is also
attached to the `gofi-sandbox-egress` network, which permits all outbound
traffic. No network client libraries are installed by default; prefer GoFi's
built-in sinks (NATS, HTTP, TCP/UDP, Loki, Zenoh, Aster) for delivery.
The supervisor supplies a separate random control credential per worker and does
not expose it in public library status.

Each function invocation runs in a separate process inside its worker, allowing
the configured deadline to terminate running code. Defaults are five seconds and
128 MiB. Allowed ranges are 1–30 seconds and 64–1024 MiB. Source is capped at
65,536 bytes, and function input/output payloads are bounded. These controls
isolate the extension runtime from GoFi; they do not replace the host/container
runtime's security boundary.

Sandbox definitions persist in namespaced CRDs on Kubernetes. Other deployments
use a separate file, defaulting to `sandboxes.yaml` beside the flow file;
`GOFI_SANDBOX_CONFIG_FILE` overrides it. Worker recreation reloads source and
rotates control credentials. In-memory Python state is not a durable data store.

## Build and test the worker

```sh
docker build -f python/Dockerfile -t ghcr.io/wcrum/gofi-python:0.2.0 .
python3 -m venv /tmp/gofi-python-dev
. /tmp/gofi-python-dev/bin/activate
pip install -r python/requirements-dev.txt
python/generate.sh
PYTHONPATH=python python -m unittest discover -s python -p 'test_*.py'
```

The official Python `connectrpc` runtime and generated protobuf/Connect bindings
implement `gofi.v1.WorkerService`. See the [worker README](../python/README.md)
for generation details. The optional Go Docker integration test exercises actual
worker startup and authentication:

```sh
GOFI_DOCKER_INTEGRATION=1 GOWORK=off go test ./sandbox -run '^TestDockerIsolatedWorker$' -count=1
```

A completed local worker test does not establish a UDS or Kubernetes deployment;
see [verification evidence](verification.md) for completed checks.


To exercise the real outer editor RPCs against a running local controller and
its rebuilt worker image:

```sh
PYTHONPATH=python python scripts/verify_python_tools.py --api http://127.0.0.1:9090
```

This creates and removes a uniquely named test library. It checks formatting,
syntax diagnostics, non-execution during static tooling, sample results, explicit
rejection, and function-kind errors. The script's completion is separate from
unit-test evidence and must be recorded only after it has actually run.
