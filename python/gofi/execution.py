"""Per-invocation process lifetime bounds, inside the isolated worker container."""
import asyncio
import base64
import inspect
import json
import multiprocessing
from . import _functions, _load

MAX_RESULT = 1048576

def _execute(pipe, source, name, kind, raw, fields, meta):
    try:
        _load(source)
        entry = _functions.get((kind, name))
        if entry is None:
            raise ValueError(f"unknown {kind} function: {name}")
        callback = entry.callback
        if kind in ("parser", "listener"):
            result = callback(raw)
        else:
            result = callback(fields, meta) if len(inspect.signature(callback).parameters) > 1 else callback(fields)
        if inspect.isawaitable(result):
            result = asyncio.run(result)
        output = {"fields": fields, "drop": False}
        if isinstance(result, bytes):
            if kind != "listener":
                raise TypeError("only listener functions may return bytes")
            output["raw"] = base64.b64encode(result).decode("ascii")
            output.pop("fields", None)
        elif isinstance(result, dict):
            output["fields"] = result
        elif result is None:
            output["drop"] = kind in ("parser", "transformer", "listener")
        else:
            raise TypeError("function must return a dict, listener bytes, or None")
        encoded = json.dumps({"result": output}, allow_nan=False).encode()
        if len(encoded) > MAX_RESULT:
            raise ValueError("function result exceeds 1 MiB")
        pipe.send_bytes(encoded)
    except BaseException as exc:
        pipe.send_bytes(json.dumps({"error": f"{type(exc).__name__}: {exc}"[:1024]}).encode())
    finally:
        pipe.close()

def invoke(source, name, kind, raw, fields, meta, timeout):
    # A process provides a killable deadline; container/Pod isolation is the
    # security boundary. Never execute user code in the Go controller process.
    context = multiprocessing.get_context("spawn")
    reader, writer = context.Pipe(duplex=False)
    process = context.Process(target=_execute, args=(writer, source, name, kind, raw, fields, meta))
    process.start()
    writer.close()
    try:
        if not reader.poll(timeout):
            raise TimeoutError("Python function exceeded its configured deadline")
        try:
            result = json.loads(reader.recv_bytes(MAX_RESULT + 4096))
        except EOFError as exc:
            raise RuntimeError("Python invocation exited without a result") from exc
        if "error" in result:
            raise ValueError(result["error"])
        return result["result"]
    finally:
        reader.close()
        if process.is_alive():
            process.kill()
        process.join(timeout=1)
