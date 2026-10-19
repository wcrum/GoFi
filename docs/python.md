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
    def telemetry(raw: bytes) -> dict:
        return json.loads(raw.removeprefix(b"device:"))

    @gofi.transformer("normalize")
    def normalize(fields: dict, meta: dict) -> dict:
        return {**fields, "app": str(fields.get("app", "unknown")).lower()}

    @gofi.decoder("strip-prefix")
    def strip_prefix(data: bytes) -> bytes:
        return data.removeprefix(b"prefix:")

    @gofi.transformer("threshold")
    def threshold(fields: dict, meta: dict, limit: int = 30) -> dict | None:
        """Keep hot readings.

        limit: minimum temperature
        """
        print("checking", fields.get("temperature"))
        return fields if fields.get("temperature", 0) >= limit else None

    @gofi.sink("debug")
    def debug(fields: dict, meta: dict) -> None:
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

### Typed functions and pydantic models

Annotations are optional but recommended: the editor templates carry them, and
they document what each step receives and returns. The data parameters follow
the decorator (`data: bytes` for framers, decoders, parsers and encoders;
`fields: dict, meta: dict` for transformers and sinks; `ctx: gofi.SourceContext`
for sources). Return annotations match the table at the top of the `gofi`
module: `bytes` for decoders and encoders, `dict | None` for parsers and
transformers, `tuple[list[bytes], bytes]` for framers, `list[gofi.Frame]` for
fetchers and `Iterator[dict]` for streamers. Only keyword parameters after the
data arguments become per-node configuration, and only their annotations
(`str`, `int`, `float`, `bool`, `list`, `dict`) are read by GoFi; data and
return annotations are never interpreted at runtime.

The worker image includes [pydantic](https://docs.pydantic.dev/), so a library
can declare the shape of a frame once and validate against it. Returning `None`
from a parser drops the frame, so a model is a compact way to reject malformed
input with a logged reason:

```python
from pydantic import BaseModel, Field, ValidationError
import gofi


class Reading(BaseModel):
    """One telemetry frame. Unknown keys are ignored; types are coerced."""

    sensor: str = Field(min_length=1)
    temperature: float = Field(ge=-50, le=150)
    unit: str = "C"


@gofi.parser("reading")
def reading(data: bytes) -> dict | None:
    try:
        return Reading.model_validate_json(data).model_dump()
    except ValidationError as error:
        print("rejected:", error.errors(include_url=False, include_input=False))
        return None


@gofi.transformer("fahrenheit")
def fahrenheit(fields: dict, meta: dict) -> dict:
    reading = Reading.model_validate(fields)  # fields already validated upstream
    return reading.model_copy(update={"temperature": reading.temperature * 9 / 5 + 32, "unit": "F"}).model_dump()
```

`model_dump()` returns plain JSON-compatible values, which is what a parser or
transformer must return. `include_input=False` keeps the rejected frame's
contents out of the printed line: worker stdout is shipped to the live log
stream and to central logs, so echo a rejected frame only when you mean to.
Avoid `ImportString` fields on frame data; they let a frame name code to
import. Keep models at module level so the warm executor
builds their validators once rather than on every frame.

### AI and LangChain integrations

A library can hand messages to the deployment's AI model without naming a
provider, an endpoint, or a key:

1. **The operator, once.** Under Administration › AI assistant, enable the
   AI endpoint (the same configuration GoFiBot uses): provider (`anthropic`
   or `openai`, which covers every OpenAI-compatible endpoint: Groq, vLLM,
   Ollama, LiteLLM), endpoint, model, and key, and turn on **Functions may
   use the AI endpoint**. That is the only switch: it is deployment-wide,
   and libraries carry no AI setting of their own (`spec.ai` is ignored).
2. **The library author.** Call `gofi.llm`:

```python
import gofi


@gofi.transformer("ai_classify")
def ai_classify(fields: dict, meta: dict, *, labels: list = ["normal", "suspicious", "critical"], drop: list = ["normal"]) -> dict | None:
    fields["label"] = gofi.llm.classify(fields, labels)
    return None if fields["label"] in drop else fields
```

**New function** offers four starting points, all in
[`examples/python-ai.py`](../examples/python-ai.py) with the Sandbox YAML:
*AI classify / route* (one label from `labels`, drop the ones in `drop`),
*AI enrich* (a `prompt` with `{fields}`, the answer under `output_key`),
*AI extract* (a pydantic model declared in the template fixes the shape,
a `prompt` says what to look at, the values land under `output_key`), and
*AI sink* (per delivered message the model picks one action from a
fixed list with `gofi.llm.extract`, and the code after that call carries it
out: the agentic boundary is the function body, not the model).

**The trust model.** While the switch is on, every library can call the deployment's model
through the controller, with its own worker token; it never sees the key.
GoFi gives the worker `GOFI_LLM_PROXY_URL` (the controller's AI proxy,
`http://<controller>:9091` by default), `GOFI_LLM_PROVIDER`, and
`GOFI_LLM_MODEL`; `gofi.llm` builds a LangChain client against the proxy
with the worker's `GOFI_SANDBOX_TOKEN` as its API key, and the proxy
(`POST /v1/chat/completions` or `/v1/messages`) checks the token against
the running workers, requires the deployment's switch, adds the real key, and
forwards to the configured endpoint. The key is kept out of the worker on
purpose: the debugger's `DebugFrame` and watches can read anything the
worker holds (locals, globals, environment), so a key there would be a key
anyone who can debug the library can read.

The proxy has its own listener (`GOFI_LLM_PROXY_ADDR`, default `:9091`;
chart `llmProxy.port`), apart from the API server, so what a worker can
reach is the proxy and nothing else: the portal's RPCs are never on that
port. Egress is to that port only. Kubernetes workers get a NetworkPolicy
rule for the controller Pods (selected by the running Pod's own labels) on
the proxy port; the address is the Service that selects the controller, on
its port targeting the proxy (or `GOFI_LLM_PROXY_HOST`). Docker workers
reach the proxy over the internal sandbox network without any rule (a rule
would attach the all-outbound egress network): the proxy binds the
controller's address on that network alone, its own interface when the
controller is a container, else the network's gateway, never `0.0.0.0`; when
that address cannot be determined, AI stays unavailable and the reason is
logged. Model traffic therefore leaves from the controller, under the same
rules as GoFiBot (no redirects; a private or local endpoint only with
`GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT=1`). Nothing in a library needs a
SaaS address range.

The proxy also bounds what a library can spend: the model is always the
deployment's (a request naming another model is refused with 400; `gofi.llm`
sends none), `max_tokens` / `max_completion_tokens` are capped at
`GOFI_LLM_MAX_OUTPUT_TOKENS` (default 8192; chart `llmProxy.maxOutputTokens`),
`anthropic-beta` headers are not forwarded, and at most 4 calls per library
and 32 per controller are in flight (more get 429 with `Retry-After`, which
surfaces as an `LLMError`). Each call is logged (library, provider, model,
status, duration; never bodies or keys) and counted in Prometheus
(`gofi_llm_proxy_calls_total`, `gofi_llm_proxy_duration_seconds`, by library).
Changing the endpoint, provider, or model under Administration restarts
every AI worker. A standby controller replica answers 503 with `Retry-After`
until it leads; `gofi.llm` retries such an answer up to three times within
the call's `timeout`.

**`gofi.llm` reference.** Every helper raises `gofi.llm.LLMError` when the
switch is off ("the deployment's AI endpoint is not enabled for functions; an
administrator turns it on under Administration › AI assistant") or a call fails; the message never
carries the token. `data` is a dict (sent as compact JSON), a str, or bytes.

| Call | Returns |
| --- | --- |
| `gofi.llm.available()` | `True` when this library may use the endpoint |
| `gofi.llm.chat(prompt, *, system=None, timeout=20.0)` | the model's text answer, stripped |
| `gofi.llm.classify(data, labels, *, instructions="", timeout=20.0)` | exactly one of `labels` (structured output: the schema carries the labels as an enum; any other answer is an `LLMError`) |
| `gofi.llm.extract(data, schema=None, *, instructions="", timeout=20.0)` | a dict: without `schema`, the JSON object the model answers (say which keys you want); with `schema`, a pydantic model class or `{"vendor": str, "amount": float}`, only those fields, `None` when the input does not state one |
| `gofi.llm.model(*, timeout=20.0)` | the LangChain chat model itself (`ChatOpenAI` or `ChatAnthropic`), for `invoke`, `with_structured_output`, `bind_tools`, and chains |

The client is built once per process and reused by every call (the warm
executor keeps module state), with `max_retries=0` so a transient failure is
one failed message rather than a call three times slower. `timeout` is per
call: keep it below the library's `timeoutSeconds` (1–30 s; set 30 for AI
libraries), because a call that overruns the library deadline kills the
warm process. A model call takes hundreds of milliseconds to seconds and a
library's calls run one at a time, so throughput is about one message per
model latency and every flow sharing the library queues behind it: give AI
steps their own library and keep them behind the filters.

An `LLMError` that propagates out of a transformer fails closed for that
message (dropped and counted, like any Python error); catch it to keep the
message going instead:

```python
try:
    fields["summary"] = gofi.llm.chat(prompt)
except gofi.llm.LLMError as error:
    fields["summary_error"] = str(error)
```

**Cost and safety.** Every message is one model call and one bill: put CEL
filters, dedup, and sampling in front, and use the classifier (`drop`) to
gate an expensive enrichment. Never send credentials from frames: the
templates' prompts send the whole message (`{fields}`); narrow them to the
fields you need (`{message}`) when frames carry tokens, passwords, or
personal data. Model output is untrusted text: store it as a field, do not
`eval` it or build code or key expressions from it (frames can carry prompt
injection). Worker stdout ships to the live log stream and central logs; the
AI sink prints the decision and a truncated message. `get_num_tokens` is not usable: it
downloads tiktoken encodings, which needs egress the worker does not have.

**LangChain directly.** The worker image includes `langchain-core`,
`langchain-openai`, and `langchain-anthropic` (versions in
[Installed packages](#installed-packages)); `gofi.llm.model()` is the
supported way to reach them, since a client the library builds itself would
need an endpoint, a key, and egress of its own. The `langchain`
meta-package, LangGraph, vector stores, and embeddings are not installed;
an agent runs behind an OpenAI-compatible gateway configured as the
deployment's endpoint.

**Drafting with GoFiBot.** [GoFiBot](assistant.md) reads this page with
`read_docs` before writing Python, so a plain request works: "classify
syslog lines as normal, suspicious, or critical and drop the normal ones".
It starts from these templates, validates the source with
`validate_python_source`, and reminds you that the endpoint must be enabled
for functions under Administration; it has nothing to configure, because
the endpoint is the deployment's.

### Credentials and the debugger

The portal's step debugger records every local variable of the invoked
function, and watch expressions run as Python inside the worker. Locals and
watches whose name is a credential (`api_key`, `apiKey`, `password`,
`token`, `secret`, and the other names `spec.Redact` hides in flow
definitions) are recorded as `***`, so a recording never carries a key by
accident. Any other expression can still read what the worker holds, which
is why GoFi never places the deployment's model key in a worker: AI-enabled
libraries call the controller's proxy with their own worker token instead.

## Select functions in a flow

```yaml
source:
  listen:
    network: tcp
    port: 7000
python:
  library: telemetry-library   # default for every step below
framing:
  mode: line
decode:
  decoders:
    - type: python
      python: {function: strip_prefix}
parser:
  format: python
  python: {function: telemetry}
  errorPolicy: drop
stages:
  - id: normalize
    kind: python
    python: {function: normalize}
sinks:
  - type: python
    python:
      library: telemetry-library   # an explicit library always wins
      function: debug
```

`spec.python.library` is the default library of every Python step that names
only a `function` (framer, decoders, parser, parse stages, stages, encoders,
sinks). Inline stage source is unaffected, and a Python source names its
library itself.

### One decorator per flow step

Each decorator plugs into one step of a flow, in pipeline order. The flow
editor offers a library's functions only in the matching step.

| Decorator | Flow step | Receives | Returns |
| --- | --- | --- | --- |
| `@gofi.framer` | Frame (`framing.mode: python`) | `buffer: bytes`, optional `final: bool` | `(frames, rest)` |
| `@gofi.decoder` | Decode (`decode.decoders`, or a parse stage's `parse.decoders`) | `data: bytes` | `bytes` |
| `@gofi.parser` | Parse (`parser.format: python`, or a parse stage's `parse.format: python`) | `raw: bytes` | `dict`, or `None` to reject |
| `@gofi.transformer` | Stages | `fields`, optional `meta` | `dict`, or `None` to reject |
| `@gofi.encoder` | Encode (`sinks[].encoding.encoders`) | `data: bytes`, optional `fields`, `meta` | `bytes` |
| `@gofi.sink` | Sink (`type: python`) | `fields`, optional `meta` | `None` |
| `@gofi.fetcher(every=...)` | Source (`source.python`), polled | optional `ctx` | bytes, str, dict, `gofi.Frame`, a list of these, or `None` |
| `@gofi.streamer` | Source (`source.python`), long-lived | optional `ctx` | a generator yielding the same |

For a `listen` source GoFi owns the socket: `source.listen` sets the network
and port. A Python source produces the frames itself; see
[Python sources](#python-sources-fetchers-and-streamers).

**Framers** split a byte stream into frames when the built-in modes (`line`,
`delimited`, `length-prefix`, `raw`) do not fit. GoFi calls the framer with
everything buffered so far and keeps the returned `rest` for the next read.
When the connection ends it calls once more with `final=True` so a trailing
partial frame can be flushed. A framer that buffers more than `framing.maxBytes`
(default 1 MiB) without producing a frame fails the connection.

```python
@gofi.framer("records")
def records(buffer: bytes, final: bool) -> tuple[list[bytes], bytes]:
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

### Python sources (fetchers and streamers)

A flow's `source.python` names a `@gofi.fetcher` or `@gofi.streamer` of a saved
library ([flows.md](flows.md#python-sources-implemented)). GoFi reads the
function's kind from the library's registry, so the same `source.python` shape
serves both:

```yaml
source:
  python:
    library: radar
    function: poll        # a @gofi.fetcher or @gofi.streamer
    config: { limit: 100 } # keyword arguments
    every: 30s            # fetchers only; default: the decorator's every, else 30s
```

```python
@gofi.fetcher("poll", every="30s")
def poll(ctx: gofi.SourceContext, *, url: str = "https://api.example/items", limit: int = 100) -> list[gofi.Frame]:
    since = ctx.state.get("since", 0)
    with urllib.request.urlopen(f"{url}?since={since}&limit={limit}", timeout=10) as r:
        items = json.load(r)
    if items:
        ctx.state["since"] = max(item["id"] for item in items)
    return [gofi.Frame(item, meta={"feed": "items"}) for item in items]


@gofi.streamer("ticks")
def ticks(ctx: gofi.SourceContext, *, interval: float = 5.0) -> Iterator[dict]:
    while not ctx.wait(interval):     # True as soon as the flow stops
        yield {"tick": time.time()}
```

- **Results.** Each item becomes one frame: bytes as is, `str` as UTF-8, a
  `dict` as compact JSON. `gofi.Frame(data, meta={...})` adds per-frame
  metadata (string values) that appears in the message's `meta`. A list
  produces several frames; `None` (or an empty list) produces none. A fetcher's
  whole result is capped at 1 MiB and 10,000 frames; each streamed item at
  1 MiB.
- **Context.** A first parameter named `ctx` or `context` receives a
  `gofi.SourceContext`: `ctx.flow` (the flow name) and `ctx.state`, a
  JSON-compatible dict GoFi keeps between polls and passes again when a
  streamer reconnects (use it for cursors). The flow runtime keeps it in memory
  (at most 64 KiB), so a runtime restart starts from `{}`. Streamers also get
  `ctx.stopping()`, `ctx.wait(seconds)`, and `await ctx.sleep(seconds)`, which
  return early when the flow stops. Other keyword parameters are
  configuration, exactly as for the other steps.
- **Fetchers** run through the library's warm executor under its deadline
  (`timeoutSeconds`, 1–30 s), one poll at a time: the next poll starts `every`
  after the previous one started, or immediately after a slow one. A
  user-code exception or deadline fails that poll (counted, logged, retried at
  the next interval) and emits nothing; the state is kept from the last
  successful poll.
- **Streamers** each run in their own process (not the warm executor, so the
  library's other functions keep running), at most four per worker. Each is a
  separate Python interpreter inside the worker's memory limit (roughly 30 MiB
  before user code), so raise the library's `memoryMiB` above the 128 MiB
  default for more than one or two streamers. The worker
  produces at most 8 messages ahead of what the flow runtime has taken, so a
  slow pipeline pauses the generator instead of buffering. When the flow stops
  (or the connection drops), the worker asks the generator to stop at its next
  `yield`, closes it so `finally` blocks run, and kills the process if it has
  not exited within 2 seconds. A streamer that returns or raises is restarted
  with exponential backoff (1 s to 30 s). Generators and async generators are
  both accepted; `@gofi.streamer` rejects a plain function at load time.
  Idle streams send a heartbeat every 15 seconds.
- **Logs.** `print` output goes to the worker's live log stream with the call's
  invocation id (`WatchWorkerLogs`), as for every function; streamers are
  limited to 1,000 lines or 64 KiB per minute.
- **Network.** The function runs in the library's worker, so it reaches only
  what the library's [egress rules](#outbound-network-egress) allow. The flow
  runtime only calls the worker; GoFi generates no egress allowance for the
  flow itself.
- **Run** in the library editor runs a fetcher once (the Fields input is the
  `ctx.state`); streamers run only as a flow source. **New function** offers
  Fetcher and Streamer templates.

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
        def transform(fields: dict, meta: dict, factor: int = 2) -> dict:
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
packages pinned to versions with prebuilt wheels (the image has no compiler).
The decoders and validators are compute-only and never touch the network; the
LangChain packages exist to call model endpoints, and reach only what the
library's [egress rules](#outbound-network-egress) allow:

| Package | Version | Use |
| --- | --- | --- |
| `msgpack` | 1.2.3 | MessagePack |
| `cbor2` | 6.1.5 | CBOR |
| `PyYAML` | 6.0.3 (`import yaml`) | YAML |
| `construct` | 2.10.70 | Declarative binary structures |
| `crc` | 8.0.0 | CRC checksums (pure Python) |
| `pydantic` | 2.14.1 | Typed models that validate frames and fields |
| `langchain-core` | 1.6.12 | LangChain messages, runnables, structured output (`gofi.llm`) |
| `langchain-openai` | 1.7.2 (`openai` 3.35.1, `tiktoken` 0.14.0) | `ChatOpenAI`: OpenAI and every OpenAI-compatible endpoint (network) |
| `langchain-anthropic` | 1.7.6 (`anthropic` 1.15.0) | `ChatAnthropic`: Claude models (network) |
| `langchain-protocol` | 0.0.19 | LangChain's shared interfaces (pure Python) |
| `langsmith` | 0.14.11 | LangChain tracing client; idle unless configured (pure Python) |
| `httpx` 0.28.1, `httpcore` 1.0.9, `httpx2` 2.13.1, `httpcore2` 2.13.1 | | HTTP clients the SDKs use (pure Python) |
| `requests` 2.34.2, `urllib3` 2.8.0 | | HTTP client `tiktoken` and `langsmith` use (pure Python) |
| `websockets` | 17.2 | `langsmith` dependency (pure Python wheel) |
| `truststore` | 0.10.5 | System trust store for `httpx2` (pure Python) |
| `tenacity` | 9.2.1 | Retry helpers (pure Python) |
| `pyqwest` | 0.12.0 | HTTP client the worker's `connectrpc` runtime uses |

The LangChain closure's compiled dependencies (`jiter`, `orjson`,
`pydantic-core`, `regex`, `tiktoken`, `uuid-utils`, `xxhash`, `zstandard`) all
ship cp314 manylinux wheels for amd64 and arm64; the rest is pure Python
(versions as resolved with the pins on 2026-10-18). The network clients
reach only what the library's egress rules allow; `gofi.llm` needs none of
them configured. The `langchain` meta-package, LangGraph, vector stores and
embeddings are not installed. `crcmod` was not used because it publishes no
wheels. Packages cannot be
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
built-in sinks (NATS, HTTP, TCP/UDP, Loki, Zenoh, Aster) for delivery. A flow
whose source is a Python fetcher or streamer reaches the network only through
these rules: the function's traffic leaves from the library's worker.
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
