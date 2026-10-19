"""Generated ConnectRPC worker service hosted by an ASGI server."""
import asyncio
import base64
import hmac
import importlib.metadata
import inspect
import os
import platform
import re
import threading
import uuid
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from google.protobuf.json_format import MessageToDict, ParseDict
from google.protobuf.struct_pb2 import Struct
from google.protobuf.struct_pb2 import Value
from . import DECORATORS, _load, functions
from .execution import Executor, StreamSession, debug, invoke
from . import debugger
from . import logs
from . import tooling
from .v1.sandbox_connect import WorkerServiceASGIApplication
from .v1.sandbox_pb2 import (
    DebugRecording, DescribePythonEnvironmentResponse, FunctionParam, RunFunctionResponse, PythonDecorator, PythonPackage, SourceDiagnostic, SourceFrame,
    WorkerFetchResponse, WorkerFunction, WorkerInvokeResponse, WorkerStreamResponse, WorkerListFunctionsResponse, WorkerFormatSourceResponse, WorkerValidateSourceResponse,
)

# The worker runtime's own distributions and everything they depend on are
# not user-facing libraries; DescribeEnvironment reports the rest.
RUNTIME_ROOTS = ("pip", "connectrpc", "protobuf", "uvicorn", "ruff")


def _canonical(name):
    return re.sub(r"[-_.]+", "-", name).lower()


def _runtime_closure():
    seen, pending = set(), [_canonical(n) for n in RUNTIME_ROOTS]
    while pending:
        name = pending.pop()
        if name in seen:
            continue
        seen.add(name)
        try:
            requirements = importlib.metadata.requires(name) or []
        except importlib.metadata.PackageNotFoundError:
            continue
        for requirement in requirements:
            if "extra ==" not in requirement:
                pending.append(_canonical(re.split(r"[\s;<>=!~\[(]", requirement, maxsplit=1)[0]))
    return seen


def _value(value):
    holder = Struct()
    try:
        holder.update({"v": value})
    except (TypeError, ValueError):
        holder.update({"v": repr(value)})
    return holder.fields["v"]


def _function_message(f):
    message = WorkerFunction(name=f.name, kind=f.kind, description=f.description, every=f.every)
    for p in f.params:
        param = FunctionParam(name=p.name, type=p.type, required=p.required, description=p.description)
        if not p.required:
            param.default.CopyFrom(_value(p.default))
        message.params.append(param)
    return message


def describe_environment():
    packages, runtime = {}, _runtime_closure()
    for dist in importlib.metadata.distributions():
        name = dist.metadata["Name"] or ""
        if name and _canonical(name) not in runtime:
            packages[name.lower()] = PythonPackage(name=name, version=dist.version, summary=dist.metadata["Summary"] or "")
    decorators = [PythonDecorator(name=f"gofi.{d.__name__}", signature=f"@gofi.{d.__name__}{inspect.signature(d)}", description=inspect.getdoc(d) or "") for d in DECORATORS]
    return DescribePythonEnvironmentResponse(python_version=platform.python_version(), packages=[packages[k] for k in sorted(packages)], decorators=decorators)

def _restore_ints(value):
    """Struct numbers arrive as floats; give whole numbers back as ints so a
    cursor stored in ctx.state keeps its type across calls."""
    if isinstance(value, float) and value.is_integer() and abs(value) < 2 ** 53:
        return int(value)
    if isinstance(value, dict):
        return {k: _restore_ints(v) for k, v in value.items()}
    if isinstance(value, list):
        return [_restore_ints(v) for v in value]
    return value


def _source_frames(frames):
    return [SourceFrame(data=base64.b64decode(f["data"]), meta=f.get("meta") or {}) for f in frames]


# Concurrent @gofi.streamer processes per worker.
MAX_STREAMS = 4
# Idle streams send an empty message this often so a vanished caller is
# noticed (the send fails) and the streamer process is stopped.
STREAM_HEARTBEAT_SECONDS = 15.0


class Worker:
    def __init__(self, source, token, timeout, lazy=False):
        if not token:
            raise ValueError("GOFI_SANDBOX_TOKEN is required")
        self.source, self.token, self.timeout, self.lazy = source, token, timeout, lazy
        # Lazy workers run unsaved editor source: loading happens only inside
        # each invocation process, so load errors are reported, not fatal.
        if not lazy:
            _load(source)
        self.registry = () if lazy else functions()
        self.lock = asyncio.Semaphore(1)
        self.streams = 0
        # Saved libraries keep a warm executor; unsaved (lazy) source runs once
        # per fresh process so a load error is reported for each run.
        self.executor = None if lazy else Executor(source)
        if self.executor:
            self.executor.start()

    def authorize(self, ctx):
        supplied = ctx.request_headers.get("authorization", "").removeprefix("Bearer ")
        if not supplied:
            supplied = ctx.request_headers.get("gofi-sandbox-token", "")
        if not hmac.compare_digest(supplied, self.token):
            raise ConnectError(Code.UNAUTHENTICATED, "worker authentication required")

    async def list_functions(self, request, ctx):
        self.authorize(ctx)
        return WorkerListFunctionsResponse(functions=[_function_message(f) for f in self.registry])

    async def invoke(self, request, ctx):
        self.authorize(ctx)
        if len(request.raw) > 1048576:
            raise ConnectError(Code.RESOURCE_EXHAUSTED, "input exceeds 1 MiB")
        if not self.lazy and not any(f.name == request.name and f.kind == request.kind for f in self.registry):
            raise ConnectError(Code.NOT_FOUND, "function not registered")
        invocation_id = request.invocation_id[:64] or uuid.uuid4().hex[:16]
        try:
            async with self.lock:
                args = (request.name, request.kind, request.raw, MessageToDict(request.fields), MessageToDict(request.meta), self.timeout, MessageToDict(request.config), invocation_id, request.trace_lines)
                if self.executor:
                    result = await asyncio.to_thread(self.executor.invoke, *args)
                else:
                    result = await asyncio.to_thread(invoke, self.source, *args)
            response = WorkerInvokeResponse(drop=result.get("drop", False), stdout=result.get("stdout", ""), stderr=result.get("stderr", ""), duration_us=result.get("durationUs", 0),
                                            entry_line=result.get("entryLine", 0), executed_lines=result.get("executedLines", []), invocation_id=invocation_id)
            if "error" in result:
                response.error = result["error"]
                if result.get("errorLine"):
                    response.error_location.CopyFrom(SourceDiagnostic(line=result["errorLine"], column=1, code="RUNTIME", message=result["error"]))
                return response
            if "fields" in result:
                fields = Struct()
                fields.update(result["fields"])
                response.fields.CopyFrom(fields)
            if "raw" in result:
                response.raw = base64.b64decode(result["raw"])
            return response
        except TimeoutError as exc:
            raise ConnectError(Code.DEADLINE_EXCEEDED, str(exc)) from exc
        except (ValueError, TypeError, RuntimeError) as exc:
            raise ConnectError(Code.INVALID_ARGUMENT, str(exc)) from exc

    async def format_source(self, request, ctx):
        self.authorize(ctx)
        try:
            formatted = await asyncio.to_thread(tooling.format_source, request.source)
            return WorkerFormatSourceResponse(source=formatted)
        except ValueError as error:
            raise ConnectError(Code.INVALID_ARGUMENT, str(error)) from error
        except TimeoutError as error:
            raise ConnectError(Code.DEADLINE_EXCEEDED, "formatter deadline exceeded") from error

    async def validate_source(self, request, ctx):
        self.authorize(ctx)
        try:
            diagnostics = await asyncio.to_thread(tooling.validate_source, request.source)
            return WorkerValidateSourceResponse(valid=not diagnostics, diagnostics=[SourceDiagnostic(**item) for item in diagnostics])
        except ValueError as error:
            raise ConnectError(Code.INVALID_ARGUMENT, str(error)) from error
        except TimeoutError as error:
            raise ConnectError(Code.DEADLINE_EXCEEDED, "validator deadline exceeded") from error

    async def describe_environment(self, request, ctx):
        self.authorize(ctx)
        return await asyncio.to_thread(describe_environment)

    async def debug(self, request, ctx):
        """Record one traced invocation for step-through replay."""
        self.authorize(ctx)
        if len(request.raw) > 1048576:
            raise ConnectError(Code.RESOURCE_EXHAUSTED, "input exceeds 1 MiB")
        if not self.lazy and not any(f.name == request.name and f.kind == request.kind for f in self.registry):
            raise ConnectError(Code.NOT_FOUND, "function not registered")
        if len(request.watches) > debugger.MAX_WATCHES or any(len(w) > 500 for w in request.watches):
            raise ConnectError(Code.INVALID_ARGUMENT, f"at most {debugger.MAX_WATCHES} watch expressions of up to 500 characters")
        if request.max_steps < 0 or request.max_steps > debugger.MAX_STEPS:
            raise ConnectError(Code.INVALID_ARGUMENT, f"max_steps must be between 0 and {debugger.MAX_STEPS}")
        invocation_id = request.invocation_id[:64] or uuid.uuid4().hex[:16]
        try:
            async with self.lock:
                outcome = await asyncio.to_thread(debug, self.source, request.name, request.kind, request.raw, MessageToDict(request.fields), MessageToDict(request.meta),
                                                  self.timeout, MessageToDict(request.config), invocation_id, list(request.watches), request.max_steps)
        except TimeoutError as exc:
            raise ConnectError(Code.DEADLINE_EXCEEDED, str(exc)) from exc
        except (ValueError, TypeError, RuntimeError) as exc:
            raise ConnectError(Code.INVALID_ARGUMENT, str(exc)) from exc
        result, recorded = outcome["result"], outcome["recording"]
        response = RunFunctionResponse(drop=result.get("drop", False), stdout=result.get("stdout", ""), stderr=result.get("stderr", ""), duration_us=result.get("durationUs", 0),
                                       entry_line=result.get("entryLine", 0), run_id=invocation_id)
        if "error" in result:
            response.error = result["error"]
            if result.get("errorLine"):
                response.error_location.CopyFrom(SourceDiagnostic(line=result["errorLine"], column=1, code="RUNTIME", message=result["error"]))
        else:
            if "fields" in result:
                response.fields.update(result["fields"])
            if "raw" in result:
                response.raw = base64.b64decode(result["raw"])
        recording = DebugRecording(result=response, truncated=recorded.get("truncated", False), truncation_reason=recorded.get("truncationReason", ""),
                                   entry_line=result.get("entryLine", 0), run_id=invocation_id)
        ParseDict({"steps": recorded.get("steps", []), "initialGlobals": recorded.get("initialGlobals", [])}, recording, ignore_unknown_fields=True)
        return recording

    def _require(self, name, kind):
        if not self.lazy and not any(f.name == name and f.kind == kind for f in self.registry):
            raise ConnectError(Code.NOT_FOUND, f"no {kind} named {name!r} is registered")

    async def fetch(self, request, ctx):
        """One @gofi.fetcher poll, with the same executor, lock, and deadline
        as Invoke. The state travels as the call's fields."""
        self.authorize(ctx)
        self._require(request.name, "fetcher")
        invocation_id = request.invocation_id[:64] or uuid.uuid4().hex[:16]
        state = _restore_ints(MessageToDict(request.state))
        args = (request.name, "fetcher", b"", state, {"flow": request.flow[:253]}, self.timeout, _restore_ints(MessageToDict(request.config)), invocation_id, False)
        try:
            async with self.lock:
                if self.executor:
                    result = await asyncio.to_thread(self.executor.invoke, *args)
                else:
                    result = await asyncio.to_thread(invoke, self.source, *args)
        except TimeoutError as exc:
            raise ConnectError(Code.DEADLINE_EXCEEDED, str(exc)) from exc
        except (ValueError, TypeError, RuntimeError) as exc:
            raise ConnectError(Code.INVALID_ARGUMENT, str(exc)) from exc
        response = WorkerFetchResponse(stdout=result.get("stdout", ""), stderr=result.get("stderr", ""), duration_us=result.get("durationUs", 0), invocation_id=invocation_id)
        if "error" in result:
            response.error = result["error"]
            if result.get("errorLine"):
                response.error_location.CopyFrom(SourceDiagnostic(line=result["errorLine"], column=1, code="RUNTIME", message=result["error"]))
            return response
        fields = result.get("fields") or {}
        response.frames.extend(_source_frames(fields.get("frames", [])))
        response.state.update(fields.get("state") or {})
        return response

    async def stream(self, request, ctx):
        """Run one @gofi.streamer in its own process until it ends, fails, or
        the caller goes away. Messages are produced at most
        StreamSession.WINDOW ahead of what the caller has taken."""
        self.authorize(ctx)
        if self.lazy:
            raise ConnectError(Code.FAILED_PRECONDITION, "streamers run only in saved libraries")
        self._require(request.name, "streamer")
        if self.streams >= MAX_STREAMS:
            raise ConnectError(Code.RESOURCE_EXHAUSTED, f"this library already runs {MAX_STREAMS} streamers")
        invocation_id = request.invocation_id[:64] or uuid.uuid4().hex[:16]
        session = StreamSession(self.source, request.name, _restore_ints(MessageToDict(request.config)), request.flow[:253], _restore_ints(MessageToDict(request.state)), invocation_id)
        self.streams += 1
        try:
            await asyncio.to_thread(session.start)
            yield WorkerStreamResponse(started=True, invocation_id=invocation_id)
            idle = 0.0
            while True:
                message = await asyncio.to_thread(session.receive, 0.5)
                if message is None:
                    idle += 0.5
                    if idle >= STREAM_HEARTBEAT_SECONDS:
                        idle = 0.0
                        yield WorkerStreamResponse(invocation_id=invocation_id)
                    continue
                idle = 0.0
                if message["type"] == "end":
                    return
                if message["type"] == "error":
                    location = f" (line {message['errorLine']})" if message.get("errorLine") else ""
                    raise ConnectError(Code.ABORTED, f"streamer {request.name}: {message['error']}{location}")
                response = WorkerStreamResponse(frames=_source_frames(message.get("frames", [])), invocation_id=invocation_id)
                if "state" in message:
                    response.state.update(message["state"] or {})
                yield response
                session.credit()
        finally:
            self.streams -= 1
            # Never await here: the caller may already be cancelled.
            threading.Thread(target=session.stop, daemon=True).start()

def create_app():
    source = base64.b64decode(os.environ["GOFI_PYTHON_SOURCE"], validate=True).decode("utf-8")
    lazy = os.getenv("GOFI_LAZY_LOAD") == "1"
    logs.system("worker starting" + (" (source loads per invocation)" if lazy else ""))
    try:
        worker = Worker(source, os.environ["GOFI_SANDBOX_TOKEN"], min(30, max(1, int(os.getenv("GOFI_TIMEOUT_SECONDS", "5")))), lazy=lazy)
    except BaseException as exc:
        logs.system(f"library failed to load: {type(exc).__name__}: {exc}"[:1024], level="error")
        raise
    if not lazy:
        logs.system("worker ready: " + ", ".join(f"{f.kind} {f.name}" for f in worker.registry))
    rpc = WorkerServiceASGIApplication(worker, read_max_bytes=2 * 1048576)
    async def app(scope, receive, send):
        if scope["type"] == "http" and scope.get("path") == "/healthz":
            await send({"type": "http.response.start", "status": 200, "headers": []})
            await send({"type": "http.response.body", "body": b"ready"})
            return
        await rpc(scope, receive, send)
    return app
