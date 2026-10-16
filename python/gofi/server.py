"""Generated ConnectRPC worker service hosted by an ASGI server."""
import asyncio
import base64
import hmac
import os
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from google.protobuf.json_format import MessageToDict
from google.protobuf.struct_pb2 import Struct
from . import _load, functions
from .execution import invoke
from . import tooling
from .v1.sandbox_connect import WorkerServiceASGIApplication
from .v1.sandbox_pb2 import WorkerFunction, WorkerInvokeResponse, WorkerListFunctionsResponse, WorkerFormatSourceResponse, WorkerValidateSourceResponse, SourceDiagnostic

class Worker:
    def __init__(self, source, token, timeout):
        if not token:
            raise ValueError("GOFI_SANDBOX_TOKEN is required")
        self.source, self.token, self.timeout = source, token, timeout
        _load(source)
        self.registry = functions()
        self.lock = asyncio.Semaphore(1)

    def authorize(self, ctx):
        supplied = ctx.request_headers.get("authorization", "").removeprefix("Bearer ")
        if not supplied:
            supplied = ctx.request_headers.get("gofi-sandbox-token", "")
        if not hmac.compare_digest(supplied, self.token):
            raise ConnectError(Code.UNAUTHENTICATED, "worker authentication required")

    async def list_functions(self, request, ctx):
        self.authorize(ctx)
        return WorkerListFunctionsResponse(functions=[WorkerFunction(name=f.name, kind=f.kind, description=f.description) for f in self.registry])

    async def invoke(self, request, ctx):
        self.authorize(ctx)
        if len(request.raw) > 1048576:
            raise ConnectError(Code.RESOURCE_EXHAUSTED, "input exceeds 1 MiB")
        if not any(f.name == request.name and f.kind == request.kind for f in self.registry):
            raise ConnectError(Code.NOT_FOUND, "function not registered")
        try:
            async with self.lock:
                result = await asyncio.to_thread(invoke, self.source, request.name, request.kind, request.raw, MessageToDict(request.fields), MessageToDict(request.meta), self.timeout)
            response = WorkerInvokeResponse(drop=result.get("drop", False))
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

    async def listen(self, request, ctx):
        self.authorize(ctx)
        raise ConnectError(Code.UNIMPLEMENTED, "listener hooks run on frames bound by GoFi; background streamers are not enabled")
        yield

def create_app():
    source = base64.b64decode(os.environ["GOFI_PYTHON_SOURCE"], validate=True).decode("utf-8")
    worker = Worker(source, os.environ["GOFI_SANDBOX_TOKEN"], min(30, max(1, int(os.getenv("GOFI_TIMEOUT_SECONDS", "5")))))
    rpc = WorkerServiceASGIApplication(worker, read_max_bytes=2 * 1048576)
    async def app(scope, receive, send):
        if scope["type"] == "http" and scope.get("path") == "/healthz":
            await send({"type": "http.response.start", "status": 200, "headers": []})
            await send({"type": "http.response.body", "body": b"ready"})
            return
        await rpc(scope, receive, send)
    return app
