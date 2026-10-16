# Isolated Python function libraries

GoFi starts one worker container or Kubernetes Pod per durable `Sandbox`
resource. The controller never imports or evaluates Python source. Workers use
an administrator-configured image (`ghcr.io/wcrum/gofi-python:0.2.0`), a
nonroot user, read-only root filesystem, private temporary storage, dropped
capabilities, and CPU/memory bounds. Docker workers join an internal bridge;
Kubernetes workers receive a deny-egress NetworkPolicy and permit RPC ingress
from GoFi controllers and listener Pods.

```python
import json
import gofi

@gofi.parser("device_json")
def device_json(raw: bytes):
    return json.loads(raw.removeprefix(b"device:"))

@gofi.transformer("normalize")
def normalize(fields, meta):
    return {**fields, "app": str(fields.get("app", "unknown")).lower()}

@gofi.sink("debug")
def debug(fields, meta):
    print(json.dumps(fields))

@gofi.port(7000, "tcp")
def clean_frame(raw: bytes):
    return raw.removeprefix(b"prefix:")
```

`@gofi.listener("name")` and `@gofi.port(port, network)` register listener
hooks. GoFi owns the TCP/UDP socket and invokes the hook on each framed
message before the selected parser. Port annotations describe the hook;
listener resources still declare their actual source port and exposure.
Background fetchers and autonomous streaming listeners are not enabled.

Parsers receive bytes and return a dictionary. Transformers and sinks receive
fields and optional metadata dictionaries. A transformer or parser returning
`None` drops the message. A sink returning `None` succeeds without changing
fields. Listener hooks may return replacement bytes or decoded fields.
Functions may be synchronous or asynchronous. Each invocation runs in a
separate process inside its worker so a configured deadline can terminate
running code; the container or Pod supplies isolation from the controller.
Inputs/results are bounded to 1 MiB; deadlines are 1–30 seconds and memory
limits are 64–1024 MiB (defaults: 5 seconds and 128 MiB).

Workers implement generated `gofi.v1.WorkerService` with the official
`connectrpc` Python runtime. Go and browser clients use the same protobuf
contract in `proto/gofi/v1/sandbox.proto`. The supervisor supplies a separate
random control credential per worker; credentials stay out of public library
status responses. `WorkerService.Listen` reserves a typed streaming extension
and returns `unimplemented` for the current frame-based listener model.

Build from the repository root:

```sh
docker build -f python/Dockerfile -t ghcr.io/wcrum/gofi-python:0.2.0 .
```

Regenerate Python bindings locally (the local generator also works without a
Buf registry account):

```sh
python -m venv /tmp/gofi-python-dev
. /tmp/gofi-python-dev/bin/activate
pip install -r python/requirements-dev.txt
python/generate.sh
PYTHONPATH=python python -m unittest discover -s python -p 'test_*.py'
```

`python/buf.gen.yaml` provides the equivalent official remote plugin setup.
Durable source examples are in `examples/python-sandbox.yaml` and the expanded
library in `examples/python-library.py`.

The editor's Format and Validate actions call generated worker RPC methods.
Formatting uses pinned Ruff 0.17.1; validation uses Python AST parsing plus
Ruff's syntax/undefined-name checks. Saving a library separately requires a GoFi decorator.
Requested source is never imported or executed by either action. Static
validation does not resolve arbitrary third-party imports or establish that a
function will succeed on every input. Run invokes the saved registered
function in its isolated worker with the configured deadline.
