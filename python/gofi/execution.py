"""Per-invocation process lifetime bounds, inside the isolated worker container."""
import asyncio
import base64
import contextlib
import inspect
import io
import json
import linecache
import multiprocessing
import re
import sys
import threading
import time
import traceback
from . import _functions, _load, data_arity
from . import debugger, logs

MAX_RESULT = 1048576
MAX_OUTPUT = 65536
MAX_TRACE_LINES = 2000
LIBRARY_FILENAME = "<gofi-library>"
_DEF = re.compile(r"\s*(async\s+)?def\s")


class _CappedText(io.TextIOBase):
    """A write-only text stream that keeps the first MAX_OUTPUT characters."""

    def __init__(self):
        self.parts, self.size, self.truncated = [], 0, False

    def writable(self):
        return True

    def write(self, text):
        room = MAX_OUTPUT - self.size
        if room > 0:
            self.parts.append(text[:room])
            self.size += min(len(text), room)
        if len(text) > room:
            self.truncated = True
        return len(text)

    def value(self):
        text = "".join(self.parts)
        return text + "\n[output truncated at 64 KiB]\n" if self.truncated else text


def _error_line(exc):
    line = 0
    for frame in traceback.extract_tb(exc.__traceback__):
        if frame.filename.startswith("<gofi-"):
            line = frame.lineno or 0
    if not line and isinstance(exc, SyntaxError) and (exc.filename or "").startswith("<gofi-"):
        line = exc.lineno or 0
    return line


def _coerce(entry, config):
    """Struct numbers arrive as floats; honor int annotations for config."""
    types = {p.name: p.type for p in entry.params}
    return {key: int(value) if types.get(key) == "int" and isinstance(value, float) and value.is_integer() else value for key, value in config.items()}


def _call(entry, kind, raw, fields, meta, config):
    callback = entry.callback
    config = _coerce(entry, config)
    data = _data(kind, raw, fields, meta)[:data_arity(kind, callback)]
    parameters = inspect.signature(callback).parameters.values()
    if any(p.kind is p.VAR_KEYWORD for p in parameters):
        options = dict(config)
    else:
        accepted = {p.name for p in parameters if p.kind in (p.POSITIONAL_OR_KEYWORD, p.KEYWORD_ONLY)}
        unknown = sorted(set(config) - accepted)
        if unknown:
            raise TypeError(f"unknown configuration for {entry.name}: {', '.join(unknown)}")
        options = config
    result = callback(*data, **options)
    if inspect.isawaitable(result):
        result = asyncio.run(result)
    return result


def _data(kind, raw, fields, meta):
    """Positional data arguments for each kind of step."""
    if kind in ("parser", "decoder"):
        return (raw,)
    if kind == "framer":
        return (raw, meta.get("final") == "true")
    if kind == "encoder":
        return (raw, fields, meta)
    return (fields, meta)


def _shape(kind, result, output):
    """Validate a step's return value and place it in the RPC result."""
    if kind in ("decoder", "encoder"):
        if isinstance(result, str):
            result = result.encode()
        if not isinstance(result, (bytes, bytearray)):
            raise TypeError(f"a {kind} must return bytes (got {type(result).__name__})")
        output["raw"] = base64.b64encode(bytes(result)).decode("ascii")
        output.pop("fields", None)
    elif kind == "framer":
        if not (isinstance(result, tuple) and len(result) == 2):
            raise TypeError("a framer must return (frames, rest)")
        frames, rest = result
        if not all(isinstance(f, (bytes, bytearray)) for f in frames) or not isinstance(rest, (bytes, bytearray)):
            raise TypeError("a framer must return (list of bytes, bytes)")
        output["fields"] = {"frames": [base64.b64encode(bytes(f)).decode("ascii") for f in frames], "rest": base64.b64encode(bytes(rest)).decode("ascii")}
    elif isinstance(result, dict):
        output["fields"] = result
    elif result is None:
        output["drop"] = kind in ("parser", "transformer")
    elif kind != "sink":
        raise TypeError(f"a {kind} must return a dict or None")


def _resolve(name, kind):
    if name:
        entry = _functions.get((kind, name))
        if entry is None:
            raise LookupError(f"unknown {kind} function: {name}")
        return entry
    candidates = [entry for (entry_kind, _), entry in _functions.items() if entry_kind == kind]
    if len(candidates) != 1:
        raise LookupError(f"specify a function name: the source declares {len(candidates)} {kind} functions")
    return candidates[0]


def _entry_line(callback):
    """The `def` line that receives the input, in its source's coordinates.

    co_firstlineno points at the first decorator, so scan forward (through
    multi-line decorators) to the definition itself.
    """
    code = getattr(inspect.unwrap(callback), "__code__", None)
    if code is None:
        return 0
    lines = linecache.getlines(code.co_filename)
    for index in range(code.co_firstlineno - 1, min(len(lines), code.co_firstlineno + 50)):
        if _DEF.match(lines[index]):
            return index + 1
    return code.co_firstlineno


def _line_tracer(filename, hits):
    """sys.settrace hook recording unique executed lines of one source file,
    in first-hit order, bounded to MAX_TRACE_LINES."""
    seen = set()

    def local(frame, event, _arg):
        if event == "line" and len(hits) < MAX_TRACE_LINES:
            line = frame.f_lineno
            if line not in seen:
                seen.add(line)
                hits.append(line)
        return local

    def start(frame, _event, _arg):
        return local if frame.f_code.co_filename == filename else None

    return start


def _execute(pipe, source, name, kind, raw, fields, meta, config, invocation_id="", trace=False):
    """Child-process body of a one-shot invocation."""
    pipe.send_bytes(_run(True, source, name, kind, raw, fields, meta, config, invocation_id, trace))
    pipe.close()


_loaded = False  # a warm executor's library loaded successfully


def _serve(pipe, source):
    """Child-process body of a warm executor: load the library once, then run
    invocations sent over the pipe until it closes or the parent kills us."""
    global _loaded
    # Preload so the first call is warm. Import-time output was already shown
    # when the worker itself loaded the library, so it is discarded here.
    with contextlib.suppress(BaseException), contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
        _load(source)
        _loaded = True
    while True:
        try:
            seq, args = pipe.recv()
        except EOFError:
            return
        pipe.send_bytes(seq.to_bytes(8, "big") + _run(not _loaded, source, *args))


def _run(load, source, name, kind, raw, fields, meta, config, invocation_id="", trace=False):
    """Run one invocation in this (child) process; returns the encoded result."""
    global _loaded
    stdout, stderr = _CappedText(), _CappedText()
    echo = logs.Echo(invocation_id, name, kind)
    tees = (logs.Tee(stdout, echo, "stdout"), logs.Tee(stderr, echo, "stderr"))
    output = {"fields": fields, "drop": False}
    executed = []
    started = time.perf_counter_ns()
    try:
        with contextlib.redirect_stdout(tees[0]), contextlib.redirect_stderr(tees[1]):
            try:
                if load:
                    _loaded = False
                    _load(source)
                    _loaded = True
                entry = _resolve(name, kind)
                echo.context["function"] = entry.name
                output["entryLine"] = _entry_line(entry.callback)
                logs.system(f"invocation started: {kind} {entry.name}", **echo.context)
                started = time.perf_counter_ns()
                if trace:
                    code = getattr(inspect.unwrap(entry.callback), "__code__", None)
                    sys.settrace(_line_tracer(code.co_filename if code else LIBRARY_FILENAME, executed))
                try:
                    result = _call(entry, kind, raw, fields, meta, config)
                finally:
                    sys.settrace(None)
            finally:
                for tee in tees:
                    tee.finish()
        output["name"] = entry.name
        _shape(kind, result, output)
    except BaseException as exc:
        output = {"error": f"{type(exc).__name__}: {exc}"[:1024], "errorLine": _error_line(exc), "entryLine": output.get("entryLine", 0)}
    output["durationUs"] = (time.perf_counter_ns() - started) // 1000
    output["stdout"], output["stderr"] = stdout.value(), stderr.value()
    output["executedLines"] = executed
    if "error" in output:
        location = f" at line {output['errorLine']}" if output.get("errorLine") else ""
        logs.system(f"invocation failed after {output['durationUs']} us{location}: {output['error']}", level="error", **echo.context)
    else:
        logs.system(f"invocation finished in {output['durationUs']} us" + (" (dropped)" if output.get("drop") else ""), **echo.context)
    try:
        encoded = json.dumps({"result": output}, allow_nan=False, default=str).encode()
        if len(encoded) > MAX_RESULT + 2 * MAX_OUTPUT + 16 * MAX_TRACE_LINES:
            raise ValueError("function result exceeds 1 MiB")
    except (TypeError, ValueError) as exc:
        encoded = json.dumps({"result": {"error": f"{type(exc).__name__}: {exc}"[:1024], "errorLine": 0, "entryLine": output.get("entryLine", 0), "durationUs": output["durationUs"], "stdout": output["stdout"], "stderr": output["stderr"], "executedLines": executed}}).encode()
    return encoded


def _debug_execute(pipe, source, name, kind, raw, fields, meta, config, invocation_id, watches, max_steps, budget):
    """Child-process body of a debug run: one traced invocation, recorded."""
    stdout, stderr = _CappedText(), _CappedText()
    echo = logs.Echo(invocation_id, name, kind)
    tees = (logs.Tee(stdout, echo, "stdout"), logs.Tee(stderr, echo, "stderr"))
    output = {"fields": fields, "drop": False}
    recording = {"steps": [], "initialGlobals": [], "truncated": False, "truncationReason": ""}
    recorder = None
    started = time.perf_counter_ns()
    try:
        with contextlib.redirect_stdout(tees[0]), contextlib.redirect_stderr(tees[1]):
            try:
                _load(source)
                entry = _resolve(name, kind)
                echo.context["function"] = entry.name
                output["entryLine"] = _entry_line(entry.callback)
                filename, namespace = debugger.target(entry.callback)
                recorder = debugger.Recorder(filename, namespace, stdout, watches, max_steps, time.monotonic() + budget)
                recording["initialGlobals"] = recorder.initial_globals()
                logs.system(f"debug recording started: {kind} {entry.name}", **echo.context)
                started = time.perf_counter_ns()
                sys.settrace(recorder.trace)
                try:
                    result = _call(entry, kind, raw, fields, meta, config)
                finally:
                    sys.settrace(None)
            finally:
                for tee in tees:
                    tee.finish()
        output["name"] = entry.name
        _shape(kind, result, output)
    except BaseException as exc:
        message = "DebugAbort: stopped at the debug time budget" if isinstance(exc, debugger.DebugAbort) else f"{type(exc).__name__}: {exc}"[:1024]
        output = {"error": message, "errorLine": _error_line(exc), "entryLine": output.get("entryLine", 0)}
    output["durationUs"] = (time.perf_counter_ns() - started) // 1000
    output["stdout"], output["stderr"] = stdout.value(), stderr.value()
    if recorder is not None:
        recording.update(steps=recorder.steps, truncated=recorder.truncated, truncationReason=recorder.reason)
    logs.system(f"debug recording finished: {len(recording['steps'])} steps" + (f" (truncated: {recording['truncationReason']})" if recording["truncated"] else ""), **echo.context)
    try:
        encoded = json.dumps({"result": output, "recording": recording}, allow_nan=False, default=str).encode()
    except (TypeError, ValueError) as exc:
        encoded = json.dumps({"result": {"error": f"{type(exc).__name__}: {exc}"[:1024], "durationUs": output["durationUs"], "stdout": output["stdout"], "stderr": output["stderr"]}, "recording": {"steps": [], "truncated": True, "truncationReason": "size"}}).encode()
    pipe.send_bytes(encoded)
    pipe.close()


def debug(source, name, kind, raw, fields, meta, timeout, config=None, invocation_id="", watches=(), max_steps=0):
    """Record one traced invocation in a fresh process.

    The recorder aborts user code shortly before the deadline so a partial
    recording is still returned; the deadline itself still kills the process.
    Returns {"result": ..., "recording": ...}.
    """
    budget = max(0.5, timeout * 0.85 - 0.2)
    context = multiprocessing.get_context("spawn")
    reader, writer = context.Pipe(duplex=False)
    process = context.Process(target=_debug_execute, args=(writer, source, name, kind, raw, fields, meta, config or {}, invocation_id, list(watches), max_steps, budget))
    process.start()
    writer.close()
    try:
        if not reader.poll(timeout):
            logs.system(f"debug run killed: exceeded the {timeout}s deadline", level="error", invocation_id=invocation_id, function=name, kind=kind)
            raise TimeoutError("Python function exceeded its configured deadline")
        try:
            return json.loads(reader.recv_bytes(debugger.MAX_RECORDING_BYTES + MAX_RESULT + 4 * MAX_OUTPUT + (1 << 20)))
        except EOFError as exc:
            raise RuntimeError("Python debug run exited without a result (crash or memory limit)") from exc
    finally:
        reader.close()
        if process.is_alive():
            process.kill()
        process.join(timeout=1)


def invoke(source, name, kind, raw, fields, meta, timeout, config=None, invocation_id="", trace=False):
    """Run one function in a fresh process; a deadline kills it.

    User-code failures are returned as {"error", "errorLine"} alongside the
    captured stdout/stderr and duration; only deadline and crash conditions
    raise. Container/Pod isolation is the security boundary. Never execute
    user code in the Go controller process.
    """
    context = multiprocessing.get_context("spawn")
    reader, writer = context.Pipe(duplex=False)
    process = context.Process(target=_execute, args=(writer, source, name, kind, raw, fields, meta, config or {}, invocation_id, trace))
    process.start()
    writer.close()
    try:
        return _receive(reader, timeout, invocation_id, name, kind)
    finally:
        reader.close()
        if process.is_alive():
            process.kill()
        process.join(timeout=1)


def _receive(conn, timeout, invocation_id, name, kind, seq=None):
    if not conn.poll(timeout):
        logs.system(f"invocation killed: exceeded the {timeout}s deadline", level="error", invocation_id=invocation_id, function=name, kind=kind)
        raise TimeoutError("Python function exceeded its configured deadline")
    try:
        data = conn.recv_bytes(MAX_RESULT + 4 * MAX_OUTPUT + 16 * MAX_TRACE_LINES + 8)
        if seq is not None:
            # A reply for another request means the pipe is out of step (for
            # example, user code wrote to it); never hand it to this caller.
            if int.from_bytes(data[:8], "big") != seq:
                raise RuntimeError("Python executor reply out of sequence; executor restarted")
            data = data[8:]
        return json.loads(data)["result"]
    except EOFError as exc:
        logs.system("invocation process exited without a result (crash or memory limit)", level="error", invocation_id=invocation_id, function=name, kind=kind)
        raise RuntimeError("Python invocation exited without a result (crash or memory limit)") from exc


class Executor:
    """A warm child process for a saved library's invocations.

    Spawning a process and re-running the library for every call costs about a
    second on a worker's CPU share, which per-frame steps (framers, decoders,
    encoders) cannot afford. The executor loads the library once and serves
    calls in order. Module-level state therefore persists between calls of
    the same library. A deadline overrun or crash kills the process, and the
    next call starts a fresh one, so the deadline guarantee is unchanged.
    Every reply carries the request's sequence number, and the process is
    recycled after MAX_CALLS calls or MAX_AGE seconds, which bounds how long
    state left by one message (or a compromised function) can affect others.
    """

    MAX_CALLS = 10000
    MAX_AGE = 600

    def __init__(self, source):
        self.source, self.process, self.conn = source, None, None
        self.lock = threading.Lock()
        self.seq = self.calls = 0
        self.started = 0.0

    def start(self):
        if self.process is not None and (self.calls >= self.MAX_CALLS or time.monotonic() - self.started >= self.MAX_AGE):
            self.stop()
        if self.process is None or not self.process.is_alive():
            self.stop()
            self.calls, self.started = 0, time.monotonic()
            context = multiprocessing.get_context("spawn")
            self.conn, child = context.Pipe(duplex=True)
            self.process = context.Process(target=_serve, args=(child, self.source), daemon=True)
            self.process.start()
            child.close()

    def stop(self):
        if self.conn is not None:
            self.conn.close()
        if self.process is not None:
            if self.process.is_alive():
                self.process.kill()
            self.process.join(timeout=1)
        self.process, self.conn = None, None

    def invoke(self, name, kind, raw, fields, meta, timeout, config=None, invocation_id="", trace=False):
        with self.lock:
            self.start()
            self.seq += 1
            self.calls += 1
            try:
                self.conn.send((self.seq, (name, kind, raw, fields, meta, config or {}, invocation_id, trace)))
                return _receive(self.conn, timeout, invocation_id, name, kind, self.seq)
            except BaseException:
                self.stop()
                raise
