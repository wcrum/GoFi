# Security review: warm Python executor and crash-text surfacing

Date: 2026-10-16. Reviewer: security-engineer agent (read-only; no code changed).

Scope:

- `python/gofi/execution.py`: `Executor`, `_serve`, `_run`, `_receive`.
- `python/gofi/server.py`: `Worker.__init__` and `Worker.invoke`. Saved libraries now use `Executor`; lazy (RunSource) workers still use `invoke`.
- `sandbox/docker.go:197` `exited`, `sandbox/docker.go:214` `workerExited`, `sandbox/kubernetes.go:202` `crashed`, and `TerminationMessagePolicy: FallbackToLogsOnError` (`sandbox/kubernetes.go:61`).

## Verdict

The isolation boundary does not change. Nothing here blocks release, provided
mitigation 1 (request/response sequence check) lands soon. The change does add
three things:

- a compromise caused by one payload can now persist across messages;
- the pipe protocol can desynchronize, after which results are attributed to the wrong message;
- state accumulates without bound, so a library can be poisoned for good or grow until it is out of memory.

The crash-text change is low risk.

## Verified vs inferred

Verified empirically. These were run in `ghcr.io/wcrum/gofi-python:0.2.0` with `--network none --read-only`, mounting the working-tree `python/gofi` and driving `Executor` directly:

- Module-level state persists across calls: a list grew with values from "flowA" and then "flowB".
- A thread started in call N prints during call N+1. That output lands in call N+1's `stdout` result and live log, tagged with N+1's invocation id. Observed: `innocent -> stdout: "LEAK from earlier call: flowA-secret"`.
- User code can find the child's `Connection` through `gc.get_objects()` and send an extra frame. After that, **every later call got the previous call's result**, with no self-healing until a timeout or crash. The forged result was delivered to the next caller, and each subsequent caller got its predecessor's output.
- The host has `kernel.yama.ptrace_scope = 0`.
- The worker token is regenerated on every `Start` (`sandbox/sandbox.go:290`).
- The controller logs with `slog.NewJSONHandler` (`main.go:29`).
- The UI has no `dangerouslySetInnerHTML` or `innerHTML` sinks.
- `connectrpc` 0.12.1 enforces no server-side deadline for unary calls (it does not cancel the handler).

Inferred, not tested:

- uvicorn does not cancel a unary handler when an HTTP/1.1 client disconnects, so the cancellation path in F2 is latent today.
- When the cgroup is out of memory, the kernel kills the warm child rather than the uvicorn parent.

## 1. Isolation boundary: what changed

The real boundary is unchanged: **the worker container or Pod**. That means:

- Linux namespaces and a shared host kernel;
- the RuntimeDefault/Docker default seccomp profile;
- `cap-drop ALL`, `no-new-privileges`, UID 65532, and a read-only rootfs with a 16 MiB noexec `/tmp`;
- pids, memory and CPU cgroup limits;
- an internal network or deny-all NetworkPolicy, plus the per-library egress allowlist;
- the per-start bearer token on the controller-to-worker RPC.

There was never a security boundary *inside* the container, and the warm executor does not remove one:

- `Worker.__init__` already runs `_load(source)` in the uvicorn parent (`server.py:89`). User import-time code therefore already ran in the process that holds `GOFI_SANDBOX_TOKEN` and does the auth check.
- Spawned children inherit the environment, so user code could always read the token. The token is per start and only authorizes that library's own functions, so the impact is nil.
- With `ptrace_scope=0` and the same UID, any process in the container can ptrace any other.

The process split exists for **deadline enforcement and crash containment, not for confidentiality**. That property still holds: `_receive` polls with the deadline, `Executor.invoke` kills on any exception, and the next call respawns.

What actually changes is the **lifetime of the process that touches untrusted bytes**. It used to be one message; now it is every message until a crash or timeout. That matters for F1 and F3.

## 2. Cross-invocation risks within a library

One library is one trust domain: one author, one container. GoFi has a single admin tenant, and all portal users can edit all libraries. So anything a malicious *author* can do here, they could already do by returning whatever they want. The concerns below are about integrity bugs and about separating *external senders* whose traffic shares a library.

| Risk | Status | Cross-trust? |
|---|---|---|
| Module-level state (caches, buffers, counters) shared across flows and sources | By design, documented in `docs/python.md:260` | Only between external senders, via author bugs (F3) |
| Background threads outliving a call: CPU use, output landing in the next call, writes to real fd 1 between calls | Verified | Misattributed logs and data; same domain |
| `sys.settrace` left installed | Cleared: `sys.settrace(None)` runs in every call's `finally` (`execution.py:217`). Not cleared: `threading.settrace`, `sys.setprofile`, `sys.monitoring`, and monkeypatched builtins persist | Same domain |
| stdout/stderr redirection left behind | Restored by `contextlib.redirect_*`. Threads write to whatever stream is current | Same domain |
| Pipe left in a bad state (extra frame, concurrent use) | Verified permanent off-by-one (F2) | Results land on the wrong message, which can mean the wrong flow |
| A partial failure leaves a global invariant broken | Persists until a crash; user exceptions never recycle the process | Availability for every flow on that library (F3) |
| Debug (`debug`) and RunSource still use a fresh process | Debug replays may not reproduce warm-state behavior | Correctness only |

## 3. Malicious payloads (attacker-controlled network bytes)

Payloads reach framers, decoders and parsers as `raw`. Pickle exposure is unchanged and safe. Parent-to-child traffic uses `Connection.send` (pickle), but the parent builds those objects, so attacker bytes are only ever `bytes` inside the pickle. Child-to-parent traffic uses `recv_bytes` plus `json.loads`, so the parent never unpickles anything the child produces.

What a payload can now do that it could not before:

- **Persistence (F1).** Suppose the library has a payload-reachable code-execution or memory-corruption bug: `eval`/`pickle.loads` on input, or a vulnerable C extension parser. Before, the attacker's code lived for one call in a process that saw only the attacker's own message. Now it lives in the warm child and can read and alter **every later message from every source and flow** that uses the library. Data can leave through egress, if allowlisted, or by being written into other senders' records bound for sinks. This is the one real cross-trust-domain escalation: external sender A against external sender B. It is partly mitigated already, because `ptrace_scope=0` meant a forked grandchild could in theory attach to later per-call children. That was harder and less reliable.
- **Cumulative resource exhaustion (F3).** If a library keeps per-key state (sessions, reassembly buffers) keyed by attacker data, memory grows until the cgroup is out of memory. Before, every call reset the state. If the kernel kills the parent instead of the child, Docker has no restart policy, so the worker stays down until the supervisor's health path acts.
- **State poisoning (F3).** A crafted frame that raises partway through a mutation can leave a module-level structure invalid. All later messages then fail or are processed wrongly, with no automatic recovery.

A payload cannot trigger the pipe desync (F2) unless the library itself writes to the connection. That is not reachable from data alone.

## 4. Surfaced crash text

- **Source.** The text is the last non-empty line of the container output. Docker reads the tail of 20 lines, capped at 64 KiB total with no per-line cap. Kubernetes uses the kubelet's termination message, at most about 2 KiB or 80 lines. The author controls it, for example with `raise Exception("...")` at import. An external sender cannot reach it, because the crash happens at load, before any traffic.
- **Log forging.** None in controller logs: slog's JSON handler escapes `\n`, `\r` and control characters. The worker's own JSON log stream on fd 1 was already forgeable by user code through `os.write(1, ...)`, so that is not new.
- **XSS.** None found. React renders it as text.
- **Remaining issues:**
  - The text is unbounded in length on Docker (up to 64 KiB in `Status.Error`).
  - ANSI and control bytes pass through to any CLI or terminal that prints the status raw.
  - A portal user can show other portal users arbitrary text after "worker exited with code N:", which enables mild social engineering ("re-authenticate at ...").
  - A token placed in the message is useless, because tokens are rotated on every start.
  - In Kubernetes, `FallbackToLogsOnError` copies the log tail into Pod status for **any** error exit, including runtime crashes. Echoed user stdout, possibly containing payload or processed data, becomes readable by anyone with `get pods`. Holders of the standard `view` role can usually read `pods/log` anyway.

## Findings (ranked)

### F1: Medium. A payload-level compromise now persists and sees other senders' traffic

- **Location:** `execution.py:173-187` (`_serve` loop) and `350-390` (`Executor`).
- **Exploit:** A library decodes with `pickle.loads(raw)`, or uses a vulnerable native parser. The attacker sends one frame, which installs a hook (for example by wrapping a function in `gofi._functions`). Every later frame from other devices is then copied out or tampered with.
- **Fix:** Recycle the warm process after N invocations or T seconds (mitigation 3). For high-risk deployments, run workers under gVisor (`runsc`) or Kata and set `kernel.yama.ptrace_scope>=1` on hosts.
- **Blocks release:** No.

### F2: Medium (integrity). The pipe protocol has no request/response correlation

- **Location:** `execution.py:383-390` and `server.py:117-122`.
- **Evidence:** One extra frame from the child shifts every later result by one call, permanently (verified). The same desync would happen if `Worker.invoke` were ever cancelled while its `to_thread` call is still running. The `asyncio.Semaphore` is released on cancellation, but the thread keeps using the pipe, so a second request would then share the connection concurrently. That path is latent today because neither connectrpc nor uvicorn cancels unary handlers (inferred), but adding middleware or a timeout would expose it.
- **Fix:** Mitigations 1 and 2.
- **Blocks release:** No. Fix soon.

### F3: Low-Medium (availability). Unbounded state and poisoning

- **Location:** `execution.py:173-187`.
- **Evidence:** No recycling occurs on user exceptions or memory growth.
- **Fix:** Mitigation 3, plus a documentation warning (mitigation 5).
- **Blocks release:** No.

### F4: Low. Background threads leak output and data into the next invocation's result and logs

- **Location:** `execution.py:200` (`redirect_stdout` is process-global).
- **Evidence:** Verified.
- **Fix:** After each call, record `threading.active_count()` and warn, or recycle the process, if threads survive the call. Optionally replace `sys.stdout` between calls with a sink that drops writes, or tags them `stream=background`, rather than writing raw to fd 1.
- **Blocks release:** No.

### F5: Low. Surfaced crash text is unbounded and can carry control characters; Kubernetes termination message is widened

- **Location:** `docker.go:214-221` and `kubernetes.go:61`.
- **Fix:** Mitigation 4.
- **Blocks release:** No.

### F6: Informational. No in-container boundary (pre-existing)

User code already runs in the token-holding uvicorn parent. Do not treat the parent/child split as a security control in future designs.

## Recommended mitigations (ranked, minimal)

1. **Correlate requests and responses (F2).** Send a monotonically increasing `seq` with each request. `_serve` echoes it in the result JSON. In `Executor.invoke`, if the `seq` that comes back does not match, call `stop()` and raise `RuntimeError("worker protocol desync")`. This is about 6 lines. Also have the child send results on a pipe that user code cannot reach. One option is to `os.dup` the fd at the start of `_serve` and drop every reference to the `Connection` object, though `gc` and `/proc/self/fd` still expose the fd. The `seq` check is the real fix.
2. **Serialize inside `Executor` (F2).** Add a `threading.Lock` around the body of `invoke`, so a cancelled caller's still-running thread cannot interleave with the next call whatever happens to the asyncio semaphore. That is 2 lines.
3. **Recycle the warm process (F1, F3).** Call `stop()` after `max_calls` invocations (e.g. 10,000) or `max_age` (e.g. 10 minutes). Optionally also stop when the child's RSS (`/proc/<pid>/statm`) goes above about 75% of `MemoryMiB`. This caps both how long a compromise lasts and how long poisoned state lasts. It is about 10 lines and costs one spawn per recycle.
4. **Sanitize surfaced crash text (F5).** In `workerExited`:
   - strip runes that fail `unicode.IsPrint`;
   - truncate to about 512 bytes;
   - keep the "worker exited with code N:" prefix so the UI makes clear the rest is library output.

   For Kubernetes, either keep `FallbackToLogsOnError` and document that Pod status can contain the log tail, or write an explicit, capped `/dev/termination-log` from `create_app`'s load-failure handler and leave the default policy (`File`) in place.
5. **Documentation (F3, F4).** Extend `docs/python.md:260`:
   - warn that module-level state is shared across **all flows and sources** using the library;
   - say that per-connection state must be keyed by connection and bounded;
   - say that threads must not outlive a call.
6. **Platform hardening (F1, F6):**
   - recommend gVisor or Kata runtime classes for workers that parse internet-facing traffic;
   - set `kernel.yama.ptrace_scope=1` on hosts (this dev host is `0`);
   - optionally use a custom seccomp profile that denies `ptrace` and `process_vm_readv` in workers.
