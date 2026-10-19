# GoFiBot

GoFiBot is a chat assistant built into the GoFi controller. It answers
questions about this deployment and, through server-side tools, inspects and
(when an administrator allows it) creates flows, CEL expressions, and Python
function libraries. It opens from the portal's chat control once an
administrator enables it under **Administration**.

What it is: one conversation loop in the controller over the
[Anthropic Messages API](https://docs.anthropic.com/en/api/messages), using the
official Go SDK. The controller holds the API key; browsers never see it. The
model only sees what a tool returns or a user types. Apart from a small
readiness probe (below), nothing is sent to the model endpoint unless a user
chats or an administrator presses **Check connection**.

What it is not: it is not a provider abstraction, it has no memory beyond the
controller process, and it never runs code. Every change it makes goes through
the same handlers as a portal save.

## Enable it

1. Open **Administration** and find the GoFiBot section. The configuration
   is read from the store (`GetAssistantConfiguration`); the key is withheld.
2. Set the API key, or leave it empty and supply one through the environment
   (see [Where the configuration lives](#where-the-configuration-lives)).
3. Optionally set a base URL for a gateway or proxy, a model, and effort.
4. Press **Check connection**. The controller sends one tiny request
   (`max_tokens: 16`, "Reply with the single word OK.") to the configured
   endpoint, or to the form's unsaved values, and reports the model that
   answered and the latency. The check has a 30 s deadline.
5. Turn **Enabled** on and save (`ApplyAssistantConfiguration`). Saving with
   an empty key keeps the stored one, so the form can round-trip a
   configuration it never sees. Saves are logged with the author's identity.

Until **Enabled** is on, the chat control is hidden and `Chat` fails with
`FailedPrecondition`.

### Readiness

`GetAssistantConfiguration` also returns `ready` and `ready_message`. Ready
means all of: `enabled` is on, a key resolves, and the endpoint answered a
probe recently. The probe is the same 16-token "Reply with the single word
OK." request as **Check connection**, with a 15 s deadline. Its result is
cached for 5 minutes; a failed probe is retried after 1 minute; a save or
**Check connection** refreshes it immediately; a configuration change
invalidates it (`assistant/client.go`, `readiness`).

The portal shows the GoFiBot header control only when `ready` is true, so a
wrong key or an unreachable gateway hides the chat instead of failing on first
use. `ready_message` says why it is hidden (`disabled`, `no API key`, or the
endpoint's error) or names the probed model when it is shown. The probe runs
only when someone loads the portal, at most once per 5 minutes while the
endpoint answers, so an idle controller sends nothing.

## Configuration

| Field | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | Off hides the chat control; `Chat` fails. |
| `provider` | `anthropic` | The protocol the endpoint speaks: `anthropic` (the Messages API, used through the official SDK) or `openai` (Chat Completions with streaming and function calling: Groq, vLLM, LiteLLM, Ollama, OpenRouter, Azure-style gateways). |
| `baseUrl` | empty (`https://api.anthropic.com`, or `https://api.openai.com/v1` for `openai`) | The model endpoint. Set it for a gateway or proxy that speaks the Anthropic Messages API. Must be an `http(s)` URL with a host and no credentials. Ignored and read-only when `GOFI_ASSISTANT_BASE_URL` pins it (`endpointLocked`). |
| `model` | empty (`claude-opus-5-5`) | Model id, at most 128 characters. The server default lives in `assistant.DefaultModel`. |
| `apiKey` | empty | Write-only: never returned by reads. Empty on save keeps the stored key; the redaction sentinel `***` is treated the same way; `clearApiKey` on the request forgets it. Refused when the environment supplies the key (`keyLocked`). |
| `hasApiKey` | read-only | A key is stored or supplied by the environment. |
| `apiKeySource` | read-only | `stored`, `environment`, or `none`. |
| `allowChanges` | `false` | Off: the model may only read, validate, and draft. On: the apply tools are offered. See [Allow changes](#allow-changes). |
| `allowFunctions` | `false` | On: every Python library may call this endpoint through `gofi.llm` via the controller's LLM proxy (the key never reaches a worker; each call is checked). Off: `gofi.llm` raises in every library. One switch for the deployment; the per-library `ai` flag is deprecated and ignored. See [python.md](python.md#ai-and-langchain-integrations). |
| `instructions` | empty | Extra operator text appended to the system prompt, at most 8192 bytes. |
| `maxToolSteps` | `8` | Tool calls per user message before the model must answer; 1 to 20. Every call counts, including proposals. |
| `effort` | empty (model default) | `low`, `medium`, or `high`; passed as the request's output effort (`reasoning_effort` for `openai`). |
| `toolApproval` | `changes` | `changes`: only apply tools wait, as proposals decided after the reply. `all`: every tool call pauses the reply until the user approves or denies it in the chat (two minutes, then it is denied). |

Validation (`assistant.Normalize`) rejects a save that breaks these bounds with
`InvalidArgument`; a store failure returns `Unavailable`.

### API key resolution

`assistant.Key` resolves the key for every request, first match wins:

1. `GOFI_ASSISTANT_API_KEY`.
2. The trimmed contents of the file named by `GOFI_ASSISTANT_API_KEY_FILE`.
3. The stored key (`apiKey` in the file or Secret).

The environment wins so that an operator-supplied key cannot be replaced from
the portal: while one is set, saves that carry a key are refused
(`FailedPrecondition`) and the portal shows `keyLocked`. The portal shows
which source applies as `apiKeySource`.

### Providers and base URLs

Two wire protocols are supported, chosen with `provider`:

| Provider | Base URL | Model | Notes |
| --- | --- | --- | --- |
| `anthropic` | empty, or a gateway root such as `https://llm-gateway.example` (a trailing `/v1` is removed; the SDK adds `/v1/messages`) | `claude-opus-5-5` (default) | Effort maps to the request's output effort; the system prompt is cached. |
| `openai` | the API root ending in `/v1`, for example `https://api.groq.com/openai/v1` | the endpoint's model id, for example `openai/gpt-oss-120b` | Streaming via `stream: true` with usage; tools as functions; effort is sent as `reasoning_effort` (ignored by endpoints without it). |

A wrong pairing shows as `HTTP 404` on Check connection: the Anthropic SDK
posting to `/v1/messages` on an OpenAI-style root, or an OpenAI-style client
posting to `/chat/completions` on the Anthropic API. The check's message
names the request id the endpoint returned and hints at what to look at.

### Pinning the endpoint

GoFi has no in-app authorization, so every portal user can open
Administration. `GOFI_ASSISTANT_BASE_URL` takes the endpoint out of their
hands: the controller calls only that URL, reads report it as `baseUrl` with
`endpointLocked`, and a save with a different `baseUrl` is refused. Set it
together with `GOFI_ASSISTANT_API_KEY_FILE` on any deployment whose portal
users are not all administrators.

Whatever the endpoint, the controller's HTTP client never follows redirects
(a redirect would carry the key to another host), reads at most 8 MiB of a
response, and by default refuses endpoints that resolve to loopback,
link-local, private (RFC 1918, ULA), or unspecified addresses, checked at
dial time so a rebinding hostname is caught too. An in-cluster gateway needs
`GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT=1`. The connection test sends the
stored key only to the stored endpoint: testing a different endpoint from the
form requires typing that endpoint's key.

## Where the configuration lives

The configuration is one resource. The key is part of it because it is the one
value that must not sit in a plain file.

```yaml
apiVersion: gofi.wcrum.dev/v1alpha1
kind: AssistantConfiguration
spec:
  enabled: true
  baseUrl: https://llm-gateway.example.internal
  model: claude-opus-5-5
  allowChanges: false
  instructions: |
    Flows in this deployment deliver to Loki; prefer the loki sink.
  maxToolSteps: 8
  effort: medium
```

| Mode | Store | Notes |
| --- | --- | --- |
| Binary, Docker | `assistant.yaml` beside the flow file (`GOFI_CONFIG_FILE`), or `gofi/assistant.yaml` under `os.UserConfigDir()` when there is no flow file path. `GOFI_ASSISTANT_CONFIG_FILE` overrides the path. | Written atomically with mode `0600`; unreadable files are recovered from the atomic backup with a warning. Prefer `GOFI_ASSISTANT_API_KEY_FILE` where the host has a secrets mount. |
| Kubernetes | The Secret `gofi-assistant` in GoFi's namespace, key `config.yaml`. | Created on first save with the label `app.kubernetes.io/managed-by: gofi`. The chart's RBAC already grants get/create/update on Secrets (worker tokens use the same rule). |

The portal shows the backend as `file:<path>` or
`kubernetes:<namespace>/<secret>`.

| Variable | Purpose |
| --- | --- |
| `GOFI_ASSISTANT_CONFIG_FILE` | Path of the configuration file (binary/Docker); defaults to `assistant.yaml` beside the flow file |
| `GOFI_ASSISTANT_API_KEY` | API key; overrides a stored key and locks the field |
| `GOFI_ASSISTANT_API_KEY_FILE` | File whose trimmed contents are the API key (read when `GOFI_ASSISTANT_API_KEY` is empty); overrides a stored key and locks the field |
| `GOFI_ASSISTANT_BASE_URL` | Pins the endpoint; the portal cannot change it |
| `GOFI_ASSISTANT_ALLOW_FUNCTIONS` | `1`/`0` pins `allowFunctions` (whether Python libraries may use the endpoint); the portal shows it locked |
| `GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT` | `1` allows an endpoint on a private, loopback, or link-local address (an in-cluster gateway) |

## Tools

The model works through tools the controller runs. Every tool calls the same
handlers the portal uses (`UpsertFlow`, `ApplyExpression`, `ApplySandbox`,
`GetMode`, the endpoint inventory), so validation, credential redaction,
admission, and revision history apply unchanged. `ListAssistantTools` reports
each tool and whether the current configuration offers it.

| Tool | What it does | Mutating |
| --- | --- | --- |
| `list_flows` | Each flow's name, source, stage count, and sinks. | no |
| `get_flow` | One flow's full definition as YAML, credentials redacted as `***`, derived Python library names stripped. | no |
| `get_flow_problems` | The flow's open problems as the Problems tab shows them, plus a check of every Python step's library and function binding. | no |
| `validate_flow` | Runs every check a save runs (`UpsertFlow` with `validateOnly`) on a complete definition and returns `valid` with warnings, or the error. Accepts the bare definition or a full `kind: Flow` resource. | no |
| `apply_flow` | Proposes creating or replacing a flow; applied on approval. Records a revision whose message is prefixed `GoFiBot:`. | yes |
| `check_cel` | Compiles a CEL expression against GoFi's message environment and reports its output type or diagnostics with line and column. `result_type` is `bool` or `any`. | no |
| `list_expressions` | The saved CEL library without revision history. | no |
| `apply_expression` | Proposes saving a CEL expression by name; applied on approval, and flows on the latest generation are recompiled. | yes |
| `list_libraries` | Python libraries: name, worker phase and error, managed-by, registered functions, and egress rule count. | no |
| `get_library` | One library's source and settings (tests omitted). | no |
| `validate_python_source` | Static syntax and undefined-name check of library source through the tooling worker; nothing is executed. 45 s deadline. | no |
| `apply_library` | Proposes creating or replacing a library; applied on approval, which (re)starts its worker. Tests and egress rules of an existing library are kept; omitted timeout and memory keep their values. | yes |
| `list_endpoints` | Destinations GoFi can discover: containers, Services, hosts. | no |
| `describe_deployment` | Mode, driver, process role, NodePort range, and whether Python workers and the CEL library are configured. | no |
| `list_docs` | The documentation pages embedded in this build, with titles. | no |
| `read_docs` | One embedded documentation page by name (`flows`, `python`, ...). | no |
| `open_page` | Sends the user's browser to a portal page (`home`, `flows`, `new-flow`, `flow`, `flow-edit`, `flow-traffic`, `flow-problems`, `explore`, `functions`, `cel`, `endpoints`, `cluster`, `administration`) as a `navigate` event. Changes nothing on the server. | no |

Each tool call has a 60 s deadline. A tool result is clipped to 48 KiB before
it is returned to the model. Tools that need a component this deployment lacks
(no CEL library, no Python workers) say so instead of failing.

`read_docs` serves the `docs/*.md` files embedded at build time
(`assistant_runtime.go`), so answers follow this build's documentation rather
than the model's memory of it.

### Allow changes

With `allowChanges` off (the default), the three mutating tools are not sent to
the model at all. The system prompt tells the model to draft and validate
definitions, hand them to the user to apply in the portal, and to say that an
administrator can enable changes under Administration when asked to apply.

With `allowChanges` on, `apply_flow`, `apply_expression`, and `apply_library`
are offered, but a call never applies anything by itself. It creates a
**proposal**: the chat panel shows what would change (the tool, its input,
and a one-line summary such as "save flow telemetry") with Approve and Reject
buttons, and the model is told to describe the proposal and stop. Nothing
runs until someone clicks Approve (`ConfirmAssistantAction`). A conversation
holds at most 10 undecided proposals; decisions are told to the model with
the next message, so it knows what happened. An approval is refused if
`allowChanges` was turned off in the meantime. Every approved apply:

- runs the same validation, inline Python checks, and platform admission as a
  portal save, and fails the same way;
- records a revision through the same history recorder, with `source: portal`
  and a message prefixed `GoFiBot:`;
- is attributed to the person who approved it. The `ConfirmAssistantAction`
  request's identity headers (`X-Auth-Request-Preferred-Username`,
  `Authorization: Bearer <JWT>`, and the rest of the list in
  [versioning](versioning.md#authors)) are copied onto the inner request, so
  `history.AuthorFromHeader` names the approver, not the bot.

Changing `allowChanges` takes effect on the next message; running turns keep
the tool set they started with.

## What the model receives

Each turn sends one `messages` request (streamed) with:

- **System prompt** (`assistant/chat.go`, `systemPrompt`): what GoFi is, how
  to work (look before answering, read docs for exact shapes, validate before
  apply, keep `***` in edits, never ask for secrets), the allow-changes rule,
  and the operator's `instructions`. It is marked for prompt caching.
- **Conversation history**: every prior user message, model reply, tool call,
  and tool result in this conversation.
- **The user's message**, at most 32 KiB, prefixed with
  `[The user is looking at <page>]` when the portal sends a `pageContext`
  (the hash route, under 512 characters).
- **Tool definitions** for the offered tools.
- **Tool outputs**, which can include flow YAML with credentials redacted as
  `***`, CEL expressions, full Python library source, endpoint inventories,
  deployment mode, and documentation pages.

The reply streams back as `AssistantEvent`s: `text_delta`, `tool_call`,
`tool_result`, `navigate`, and a final `done` with the stop reason
(`end_turn`, `max_tokens`, `max_tool_steps`, or `refusal`), token counts, and
the number of tool steps.

### Network egress

Enabling GoFiBot creates outbound HTTPS traffic from the controller to the
configured endpoint: `api.anthropic.com` by default, or the gateway in
`baseUrl`. No other component (flow runtimes, Python workers, the browser)
talks to it. On a default-deny cluster, allow egress from the controller Pod
to that host; nothing else needs changing. Each chat request has a 5 minute
deadline and up to 2 retries, and a whole turn ends after 10 minutes. The
readiness probe is one more small request, made at most every 5 minutes while
the portal is in use. Endpoint errors reach users and logs as a status code
and request id only; the endpoint's response body is never echoed.

### Approving tool calls

With `toolApproval: all`, each tool call the model makes is shown with
Approve and Deny buttons before it runs; the reply waits (at most two
minutes) and continues with the result or a "denied by the user" result. An
approved apply tool runs at once, since the approval is the confirmation.
With the default `changes`, read-only tools run immediately and apply tools
become proposals (above).

### Pointing at a flow block

The `point_at_flow_block` tool lets the model show which step of a flow it
means: the chat draws the flow's pipeline with that block highlighted and
the model's note, next to the reply. It changes nothing.

### Conversation history

`ListAssistantConversations` lists the conversations the controller still
holds (newest first, with the first message as title) and
`GetAssistantConversation` returns a transcript, so the chat panel can
reopen and continue an earlier conversation. Conversations live in the
controller's memory for 12 hours of inactivity and are lost on restart.

### Rate limits

A `429` from the endpoint is retried twice, honouring `Retry-After` up to
10 seconds (the Anthropic SDK does the same on its path). A free-tier key
on Groq or a small per-minute token budget is still exceeded by a chat with
tools, because every request carries the system prompt, the tool
definitions, and the conversation; the error names the request id so the
endpoint's dashboard can be checked.

## Limits

| Limit | Value | Where |
| --- | --- | --- |
| Conversation memory | In the controller process, lost on restart | `assistant/chat.go` |
| Conversations held | 200; the oldest is evicted | `maxConversations` |
| Messages per conversation | 80; older ones are trimmed so history starts at a plain user message | `maxMessages` |
| Idle expiry | 12 h since the last message | `conversationTTL` |
| User message | 32 KiB | `maxMessageBytes` |
| Output per model request | 16000 tokens; a turn with tool calls makes several requests | `maxTokensPerReply` |
| Tool steps per message | `maxToolSteps` (default 8, at most 20); the next call returns "tool step limit reached" and the turn ends with `max_tool_steps` | `Configuration.MaxToolSteps` |
| Tool result | 48 KiB, then `[truncated]` | `MaxToolOutput` |
| Tool call | 60 s | `Service.run` |
| Model request | 5 min, 2 retries; at most 8 MiB read | `assistant/client.go` |
| Turn | 10 min end to end | `TurnTimeout` |
| Concurrent turns | 4 across all users; more get `ResourceExhausted` | `MaxConcurrentTurns` |
| Configuration cache | 30 s; a change made elsewhere (another replica, `kubectl`) applies within it | `cacheTTL` |

A failed turn (endpoint error, dropped stream) rolls the conversation back to
the state before the message, so the next message does not replay a
half-answered one. `ResetConversation` forgets a conversation; a conversation
id the controller no longer knows starts a new one.

## Kubernetes

- The configuration is the Secret `gofi-assistant` in GoFi's namespace. The
  chart's Role already permits get/create/update on Secrets; no RBAC change is
  needed. Supply the key through the Secret (save it in the portal), or mount
  one and set `GOFI_ASSISTANT_API_KEY_FILE` in the controller's `env`.
- With leader election, `Chat`, `ApplyAssistantConfiguration`, and
  `ResetConversation` are leader-only: their names have no read-only prefix,
  so a standby answers `503` with `Retry-After` and `X-GoFi-Leader`
  (`kubernetes_runtime.go`, `readOnlyRPCPrefixes`). `Chat` must be, because
  its tools can change flows and admission runs on the leader.
  `GetAssistantConfiguration`, `ListAssistantTools`, and
  `CheckAssistantConnection` are served by every replica.
- Conversations live in the leader's memory. After a failover, the next
  message starts a new conversation.
- If the Secret store cannot be initialized at startup (no in-cluster client,
  or no namespace), the controller logs
  `assistant configuration initialization failed` and does not mount the
  service.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| The chat control is missing from the portal header | `ready` is false. Open Administration: `ready_message` says `disabled`, `no API key`, or the endpoint's error. Fix it and press **Check connection**; the control appears on the next portal load. |
| `FailedPrecondition: the assistant is not enabled` | `enabled` is off. Turn it on under Administration. |
| `FailedPrecondition: the assistant has no API key` | No stored key and neither `GOFI_ASSISTANT_API_KEY` nor a readable, non-empty `GOFI_ASSISTANT_API_KEY_FILE`. Check `apiKeySource` on the Administration page. |
| `Unimplemented` on any assistant RPC | The service is not mounted: the controller started without an assistant store (see the Kubernetes note above) or this build predates GoFiBot. Check the controller log at startup. |
| `Unavailable: model endpoint: ...` | The endpoint did not answer: wrong `baseUrl`, a gateway that does not speak the Messages API, a blocked egress path, an invalid key, or an unknown `model`. Press **Check connection** with the same values; its message is the endpoint's error. |
| `InvalidArgument: baseUrl must be an http(s) URL` | The gateway URL needs a scheme and host, for example `https://gateway.example.internal`. Paths are kept; the SDK appends `/v1/messages`. |
| Reply ends with `max_tool_steps` | The model used every allowed tool call. Raise `maxToolSteps` (up to 20) or ask a narrower question. |
| Reply ends with `max_tokens` | The reply hit 16000 output tokens. Ask for a smaller piece. |
| The bot says it cannot apply | `allowChanges` is off. It will hand you validated YAML to apply in the portal. |
| An apply fails with a validation or admission error | The same error a portal save would give; the model is told and usually fixes and retries. Nothing was saved. |
| 503 `this replica is a standby` | Kubernetes with leader election: the portal was routed to a standby. Retry; the Service normally points at the leader. |

## Security

- **Prompt injection through tool outputs.** Flow names, CEL expressions,
  Python library source, endpoint names, and documentation pages are text the
  model reads and may follow. Anyone who can save a flow or library can place
  instructions where the bot will read them. Keep `allowChanges` off when
  people who are not trusted to change flows can chat, or when operators
  with portal access are not trusted to write what the bot reads. With
  `allowChanges` on, every change is still validated, admitted, recorded, and
  attributed to the chatting user, with the revision message prefixed
  `GoFiBot:`, so history shows what happened. An injected instruction can
  at most produce a proposal; a person has to approve it, and the proposal
  card shows the full input, so read it before clicking. Tool outputs are
  also framed as data rather than instructions, and each tool call counts
  against `maxToolSteps`.
- **Credentials.** `get_flow` returns definitions through `spec.Redact`, so
  sink and source secrets reach the model as `***` and are preserved on
  apply. The system prompt tells the model never to ask the user for secrets.
  Library source is sent as-is; do not keep secrets in Python source.
- **The key.** It is stored with mode `0600` (file) or in a Secret
  (Kubernetes), withheld from every read, and never sent to a browser. On a
  host with a secrets mount, leave `apiKey` empty and point
  `GOFI_ASSISTANT_API_KEY_FILE` at the mounted file so the store never holds
  it. Do not set `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, or
  `ANTHROPIC_BASE_URL` on the controller: the SDK would read them.
- **The endpoint.** Without `GOFI_ASSISTANT_BASE_URL`, any portal user can
  point the controller at another endpoint; the stored key is sent only to
  the stored endpoint, redirects are not followed, and private addresses are
  refused unless opted in, but pin the endpoint on any shared deployment.
- **Functions.** `allowFunctions` is a grant to every library, including
  flow-managed inline Python and scratch runs from the editor: each can spend
  the deployment's model budget and send frame data to the endpoint. Without
  an authenticating proxy any portal user can turn it on, so pin it with
  `GOFI_ASSISTANT_ALLOW_FUNCTIONS` on a shared deployment. The proxy bounds
  concurrency (4 in flight per library, 32 overall) and logs every call per
  library; it does not yet bound spend.
- **Who can chat.** GoFi does not authenticate users itself. Anyone who can
  reach the portal can chat and, with `allowChanges` on, change flows under
  the identity the proxy forwards. Put the controller behind an
  authenticating proxy (UDS Authservice or similar) as [versioning](versioning.md#authors)
  describes, and treat `allowChanges` as a grant to every portal user.
- **Egress.** The controller contacts only the configured endpoint. A gateway
  in `baseUrl` is the place to add logging, rate limits, or data-handling
  rules.
