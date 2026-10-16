# Python function libraries

A GoFi Sandbox is a durable Python function library executed in its own worker
container or Kubernetes Pod. The controller saves source, starts the worker,
reads its registered functions through ConnectRPC, and supplies private runtime
bindings to listeners. Python source is never imported into the controller.

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

    @gofi.port(7000, "tcp")
    def strip_prefix(raw: bytes):
        return raw.removeprefix(b"prefix:")

    @gofi.sink("debug")
    def debug(fields, meta):
        print(json.dumps(fields))
```

Watch the library's status before selecting a registered function in a flow.
A saved definition can exist while its worker is starting or failed; persistence
alone does not mean the function is available. The expanded source example is
[`examples/python-library.py`](../examples/python-library.py).

## Select functions in a Listener

```yaml
source:
  port: 7000
  network: tcp
  python:
    library: telemetry-library
    function: strip_prefix
framing:
  mode: line
parser:
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

`@gofi.listener(name)` and `@gofi.port(port, network)` register listener hooks.
GoFi still owns the real TCP/UDP socket. It frames each incoming message and
calls the hook before decoding/parsing. Port annotations describe a hook;
`Listener.spec.source.port` and exposure configure the actual socket. Autonomous
background listeners are not enabled. The reserved `WorkerService.Listen`
streaming extension currently returns `unimplemented`.

Parsers receive bytes and return a dictionary. Transformers receive fields and
optional metadata and return a replacement dictionary. Listener hooks may return
replacement bytes or decoded fields. Sinks receive fields and optional metadata;
returning `None` completes a sink call. Parser and transformer `None` results
explicitly reject the message. Explicit parser rejection always stops processing,
even with `errorPolicy: continue`. Other custom Python parser failures drop by
default; set `parser.errorPolicy: continue` explicitly if those errors should
continue into later stages. Synchronous and asynchronous functions are supported.

Worker invocation, source, and transformer failures are surfaced in flow errors
and drop attribution. Python transforms fail closed. Mock mode executes Python
source/parser/transformer functions through their worker while withholding real
sink dispatch.

## Runtime and isolation

Binary and Compose installations use Docker workers; Kubernetes uses managed
worker Pods. Binary operation without Python functions needs no Docker daemon.
The administrator selects a fixed worker image through `GOFI_SANDBOX_IMAGE`;
library authors cannot choose arbitrary images or runtime credentials.

Workers run as a nonroot user with a read-only root filesystem, dropped
capabilities, private temporary storage, and CPU/memory limits. Docker workers
join an internal bridge. Kubernetes workers have restricted RPC ingress and a
deny-egress NetworkPolicy; platform policies must preserve these restrictions.
The supervisor supplies a separate random control credential per worker and does
not expose it in public library status.

Each function invocation runs in a separate process inside its worker, allowing
the configured deadline to terminate running code. Defaults are five seconds and
128 MiB. Allowed ranges are 1–30 seconds and 64–1024 MiB. Source is capped at
65,536 bytes, and function input/output payloads are bounded. These controls
isolate the extension runtime from GoFi; they do not replace the host/container
runtime's security boundary.

Sandbox definitions persist in namespaced CRDs on Kubernetes. Other deployments
use a separate file, defaulting to `sandboxes.yaml` beside the Listener file;
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
