"""GoFi function decorators for isolated Python libraries.

Each decorator matches one step of a flow:

    framer       (buffer: bytes[, final]) -> (frames: list[bytes], rest: bytes)
    decoder      (data: bytes) -> bytes                  before parsing
    parser       (frame: bytes) -> dict | None           None rejects the frame
    transformer  (fields[, meta]) -> dict | None         None drops the message
    encoder      (data: bytes[, fields[, meta]]) -> bytes  before a sink sends
    sink         (fields[, meta]) -> anything            delivers the message

Flow sources (``source.python``) produce frames instead of receiving them:

    fetcher      ([ctx]) -> bytes | str | dict | Frame | list of these | None
                 called every interval (``@gofi.fetcher(every="10s")``)
    streamer     ([ctx]) yields bytes | str | dict | Frame | list of these
                 a generator (or async generator) kept running

A dict becomes one JSON frame. ``ctx`` (a first parameter named ``ctx`` or
``context``) is a :class:`SourceContext`: ``ctx.flow``, ``ctx.state`` (a dict
kept between polls and across stream reconnects, for cursors), and, for
streamers, ``ctx.stopping()`` / ``ctx.wait(seconds)`` for cooperative stops.

``gofi.llm`` calls the deployment's AI endpoint from a library that turned
AI on (``chat``, ``classify``, ``extract``, ``model``); see ``gofi.llm``.

Keyword parameters after the data arguments are per-node configuration::

    @gofi.transformer("threshold")
    def threshold(fields, meta, limit: float = 30.0):
        return fields if fields["temperature"] >= limit else None
"""
from dataclasses import dataclass, field
import asyncio
import functools
import inspect
import linecache
import re
import threading
import time
from typing import Callable

LIBRARY_FILENAME = "<gofi-library>"

@dataclass(frozen=True)
class Param:
    name: str
    type: str
    default: object
    required: bool
    description: str


@dataclass(frozen=True)
class Function:
    name: str
    kind: str
    description: str
    callback: Callable
    params: tuple[Param, ...] = field(default_factory=tuple)
    # @gofi.fetcher(every=...): default poll interval as a Go duration.
    every: str = ""


@dataclass(frozen=True)
class Frame:
    """One source frame with optional metadata (string values), e.g.
    ``gofi.Frame({"id": 1}, meta={"feed": "radar"})``. ``data`` is bytes,
    str (UTF-8), or a dict (JSON)."""
    data: object
    meta: dict = field(default_factory=dict)


class SourceContext:
    """What a fetcher or streamer receives as ``ctx``.

    ``state`` is a JSON-compatible dict the function may read and update (a
    cursor, a last-seen id). GoFi keeps it between polls and passes it again
    when a streamer reconnects; it is kept in memory by the flow runtime, so
    a runtime restart starts from an empty dict. Keep it under 64 KiB.
    """

    def __init__(self, flow="", state=None, stop=None):
        self.flow = flow
        self.state = dict(state or {})
        self._stop = stop or threading.Event()

    def stopping(self):
        """True once the flow is stopping this streamer."""
        return self._stop.is_set()

    def wait(self, seconds):
        """Sleep up to ``seconds``; returns True early when the source is stopping."""
        return self._stop.wait(max(0.0, float(seconds)))

    async def sleep(self, seconds):
        """Async form of :meth:`wait` for async streamers."""
        deadline = time.monotonic() + max(0.0, float(seconds))
        while not self._stop.is_set():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            await asyncio.sleep(min(0.2, remaining))
        return self._stop.is_set()


# Source kinds produce frames; they are not invoked per message.
SOURCE_KINDS = ("fetcher", "streamer")

_DURATION = re.compile(r"(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|ms|s|m|h)")
_UNIT_SECONDS = {"ns": 1e-9, "us": 1e-6, "µs": 1e-6, "ms": 1e-3, "s": 1.0, "m": 60.0, "h": 3600.0}


def duration_seconds(value):
    """Seconds in a Go duration string ("10s", "1m30s", "500ms") or a number."""
    if isinstance(value, bool):
        raise ValueError("every must be a duration such as \"10s\" or a number of seconds")
    if isinstance(value, (int, float)):
        return float(value)
    text = str(value).strip()
    position, total = 0, 0.0
    for match in _DURATION.finditer(text):
        if match.start() != position:
            break
        total += float(match.group(1)) * _UNIT_SECONDS[match.group(2)]
        position = match.end()
    if not text or position != len(text):
        raise ValueError(f"every must be a duration such as \"10s\" or a number of seconds (got {value!r})")
    return total


def _every(value):
    if value is None:
        return ""
    seconds = duration_seconds(value)
    if seconds < 1:
        raise ValueError("every must be at least 1s")
    return value.strip() if isinstance(value, str) else f"{seconds:g}s"
_functions: dict[tuple[str, str], Function] = {}

_TYPE_NAMES = {str: "str", int: "int", float: "float", bool: "bool", list: "list", dict: "dict"}


# Steps that receive bytes as their first argument.
BYTE_KINDS = ("parser", "decoder", "framer", "encoder")


def data_arity(kind, callback):
    """Number of leading positional data arguments the callback receives."""
    positional = [p for p in inspect.signature(callback).parameters.values() if p.kind in (p.POSITIONAL_ONLY, p.POSITIONAL_OR_KEYWORD)]
    if kind in ("parser", "decoder"):
        return 1
    if kind in SOURCE_KINDS:
        # ([ctx]): a first parameter named ctx/context receives SourceContext.
        return 1 if positional and positional[0].name in ("ctx", "context") else 0
    if kind == "framer":
        # (buffer) or (buffer, final)
        return 2 if len(positional) > 1 and (positional[1].default is inspect.Parameter.empty or positional[1].name == "final") else 1
    if kind == "encoder":
        # (data), (data, fields), or (data, fields, meta)
        names = [p.name for p in positional[1:3]]
        required = [p for p in positional[1:3] if p.default is inspect.Parameter.empty]
        return 1 + max(len(required), sum(1 for n in names if n in ("fields", "meta")))
    # (fields) or (fields, meta): a second argument without a default, or one
    # named "meta", receives metadata; anything else is configuration.
    if len(positional) > 1 and (positional[1].default is inspect.Parameter.empty or positional[1].name == "meta"):
        return 2
    return 1


def _docstring_params(doc):
    described = {}
    for line in (doc or "").splitlines():
        text = line.strip().removeprefix(":param").strip()
        name, separator, description = text.partition(":")
        if separator and name.isidentifier():
            described[name] = description.strip()
    return described


def _params(kind, callback):
    described = _docstring_params(callback.__doc__)
    out = []
    for index, parameter in enumerate(inspect.signature(callback).parameters.values()):
        if index < data_arity(kind, callback) or parameter.kind in (parameter.VAR_POSITIONAL, parameter.VAR_KEYWORD):
            continue
        annotation = parameter.annotation
        type_name = _TYPE_NAMES.get(annotation) or (annotation if isinstance(annotation, str) and annotation in _TYPE_NAMES.values() else "any")
        required = parameter.default is inspect.Parameter.empty
        out.append(Param(parameter.name, type_name, None if required else parameter.default, required, described.get(parameter.name, "")))
    return tuple(out)


ON_INVALID = ("flag", "drop", "raise")


def _validated(kind, key, callback, model, on_invalid):
    """Wraps a parser or transformer so its mapping passes through a pydantic
    model: coercion ("31.5" -> 31.5), aliases, defaults, and constraints live
    in the model, not in every function. ``on_invalid`` decides what a frame
    the model rejects becomes: "flag" keeps the fields and adds ``_invalid``
    (a filter can route it to the dead-letter sink), "drop" rejects it (the
    function returns None), "raise" lets the error propagate (the message
    fails loudly and counts as an error)."""
    if on_invalid not in ON_INVALID:
        raise ValueError(f"{kind} {key}: on_invalid must be one of {', '.join(ON_INVALID)}")
    validate = getattr(model, "model_validate", None)
    if validate is None:
        raise TypeError(f"{kind} {key}: model must be a pydantic model class")

    def apply(result):
        if result is None or (kind == "parser" and result == {}):
            # No fields means no message: a tokenizer that found nothing
            # rejects the frame instead of validating an empty mapping.
            return None
        if not isinstance(result, dict):
            raise TypeError(f"{kind} {key}: expected a dict to validate, got {type(result).__name__}")
        try:
            return validate(result).model_dump(by_alias=False)
        except Exception as error:  # pydantic.ValidationError, without importing pydantic here
            if on_invalid == "raise" or type(error).__name__ != "ValidationError":
                raise
            if on_invalid == "drop":
                return None
            first = error.errors()[0] if hasattr(error, "errors") else {"msg": str(error)}
            where = ".".join(str(part) for part in first.get("loc", ()))
            return {**result, "_invalid": f"{where}: {first.get('msg', 'invalid')}" if where else first.get("msg", "invalid")}

    if inspect.iscoroutinefunction(callback):
        async def wrapped(*args, **kwargs):
            return apply(await callback(*args, **kwargs))
    else:
        def wrapped(*args, **kwargs):
            return apply(callback(*args, **kwargs))
    functools.update_wrapper(wrapped, callback)
    wrapped.__gofi_model__ = model
    return wrapped


_FORMATS = {"keyvalue", "json", "csv", "delimited"}


def _format_callback(kind, key, format, options):
    """The default body for ``@gofi.parser(name, format=...)``: tokenize with
    gofi.text and nothing else (a model does the typing)."""
    if format not in _FORMATS:
        raise ValueError(f"{kind} {key}: format must be one of {', '.join(sorted(_FORMATS))}")
    if kind != "parser":
        raise ValueError(f"{kind} {key}: format= registers a default body for parsers only")
    from . import text as _text_module
    helper = getattr(_text_module, format)

    def parse(data: bytes) -> dict | None:
        fields = helper(data, **options)
        return fields or None
    parse.__name__ = key
    parse.__qualname__ = key
    parse.__doc__ = f"Parse one {format} frame into fields (gofi.text.{format})."
    return parse


def _decorate(kind, name=None, every=None, *, model=None, on_invalid="flag", format=None, **format_options):
    every = _every(every)

    def register(callback):
        key = name or callback.__name__
        if not key or len(key) > 80:
            raise ValueError("function name must contain 1-80 characters")
        if (kind, key) in _functions:
            raise ValueError(f"duplicate {kind} function: {key}")
        if kind == "streamer" and not (inspect.isgeneratorfunction(callback) or inspect.isasyncgenfunction(callback)):
            raise TypeError(f"streamer {key} must be a generator function (use yield)")
        wrapped = _validated(kind, key, callback, model, on_invalid) if model is not None else callback
        _functions[kind, key] = Function(key, kind, (callback.__doc__ or "").strip(), wrapped, _params(kind, callback), every)
        return callback
    if format is not None:
        # No body: ``gofi.parser("key_value", format="keyvalue", model=Fields)``
        # registers the tokenizer as the function and returns it.
        if callable(name) or not isinstance(name, str) or not name:
            raise ValueError(f"{kind}: format= needs the function name as the first argument")
        return register(_format_callback(kind, name, format, format_options))
    if format_options:
        raise TypeError(f"{kind}: unexpected arguments {', '.join(sorted(format_options))} (they belong with format=)")
    if callable(name):
        callback, name = name, None
        return register(callback)
    return register


def KeyValue(data, *, encoding="utf-8"):
    """``host=a1 temperature=31.5 msg="disk full"`` -> ``{"host": "a1", ...}``
    (strings; any whitespace, newlines, or commas between pairs; quoted
    values). Pair it with ``model=`` on the decorator for typing."""
    from . import text
    return text.keyvalue(data, encoding=encoding)


def JSON(data, *, encoding="utf-8"):
    """One JSON object per frame -> its dict; ValueError for anything else."""
    from . import text
    return text.json(data, encoding=encoding)


def CSV(data, columns, *, delimiter=",", encoding="utf-8"):
    """One CSV row per frame -> ``{column: cell}`` for the named columns."""
    from . import text
    return text.csv(data, columns, delimiter=delimiter, encoding=encoding)


def Delimited(data, sep, columns=None, *, encoding="utf-8"):
    """A positional frame split on ``sep`` -> ``{column: cell}`` (or f0, f1, ...)."""
    from . import text
    return text.delimited(data, sep, columns, encoding=encoding)


def parser(name=None, *, model=None, on_invalid="flag", format=None, **format_options):
    """Decode one frame (bytes) into a fields mapping, or None to reject it.

    ``model=`` (a pydantic class) types and checks the mapping the function
    returns; ``on_invalid`` is "flag" (default: keep the fields, add
    ``_invalid``), "drop", or "raise". ``format="keyvalue" | "json" | "csv" |
    "delimited"`` registers a function with no body of your own, tokenizing
    with ``gofi.text`` (extra keyword arguments go to the helper, e.g.
    ``columns=[...]``)."""
    return _decorate("parser", name, model=model, on_invalid=on_invalid, format=format, **format_options)


def transformer(name=None, *, model=None, on_invalid="flag"):
    """Return new fields, or None to drop the message. Usable as a flow stage.
    ``model=`` and ``on_invalid`` work as for ``parser``."""
    return _decorate("transformer", name, model=model, on_invalid=on_invalid)


def sink(name=None):
    """Receive each delivered message. Workers have no network unless the library declares egress."""
    return _decorate("sink", name)


def framer(name=None):
    """Split a byte stream into frames: return (frames, rest) for the buffered bytes.

    Called after every read with everything not yet framed; `final` (optional
    second argument) is True once the stream has ended so a trailing frame can
    be flushed.
    """
    return _decorate("framer", name)


def decoder(name=None):
    """Transform a frame's bytes before parsing (decrypt, unwrap, decompress)."""
    return _decorate("decoder", name)


def encoder(name=None):
    """Transform the bytes a sink sends (after the sink's encoding format)."""
    return _decorate("encoder", name)


def fetcher(name=None, *, every=None):
    """Poll as a flow source: return bytes, str, a dict (JSON), a gofi.Frame,
    a list of these, or None for nothing new.

    Called every ``every`` (a duration such as "10s"; default 30s, overridable
    on the flow's source) under the library's deadline. Calls never overlap.
    Network access follows the library's egress allowlist.
    """
    return _decorate("fetcher", name, every)


def streamer(name=None):
    """Stream as a flow source: a generator (or async generator) that yields
    bytes, str, a dict (JSON), a gofi.Frame, or a list of these.

    Runs until it returns, fails (GoFi restarts it with backoff), or the flow
    stops: then the generator is closed at its next yield (``finally`` runs)
    and the process is stopped shortly after. Use ``ctx.wait(seconds)`` instead
    of ``time.sleep`` to stop promptly.
    """
    return _decorate("streamer", name)


# Public decorators, reported by DescribeEnvironment.
DECORATORS = (fetcher, streamer, framer, decoder, parser, transformer, encoder, sink)

_REMOVED = {
    "ingress": "@gofi.ingress was removed: set the port on the Flow's Listen source, and use @gofi.framer to split frames or @gofi.decoder to transform bytes before parsing",
    "listener": "@gofi.listener was removed: use @gofi.framer or @gofi.decoder in the Flow's Frame or Decode step",
    "TCP": "gofi.TCP was removed: set the port on the Flow's Listen source",
    "UDP": "gofi.UDP was removed: set the port on the Flow's Listen source",
}


def __getattr__(name):
    if name == "llm":
        # gofi.llm (the deployment's AI endpoint) loads on first use, so a
        # library that never calls it pays nothing.
        import importlib

        return importlib.import_module("gofi.llm")
    if name == "text":
        # gofi.text: stdlib-only tokenizers (keyvalue, json, csv, delimited).
        import importlib

        return importlib.import_module("gofi.text")
    if name in _REMOVED:
        raise AttributeError(_REMOVED[name])
    raise AttributeError(f"module 'gofi' has no attribute {name!r}")


def functions():
    return tuple(sorted(_functions.values(), key=lambda f: (f.kind, f.name)))


def _remember(filename, source):
    """Register in-memory source so tracebacks and entry lines can read it."""
    linecache.cache[filename] = (len(source), None, source.splitlines(True), filename)


def _inline(function, source):
    """Register one inline flow-stage snippet as transformer `function`.

    The snippet runs in its own namespace (no name collisions between stages)
    and keeps its own line numbers, under filename <gofi-inline:function>.
    """
    filename = f"<gofi-inline:{function}>"
    _remember(filename, source)
    namespace = {"__name__": f"gofi_inline_{function}"}
    exec(compile(source, filename, "exec"), namespace)
    if not callable(namespace.get("transform")):
        raise ValueError(f"inline stage {function} must define transform(fields, meta)")
    transformer(function)(namespace["transform"])


def _load(source):
    _functions.clear()
    _remember(LIBRARY_FILENAME, source)
    exec(compile(source, LIBRARY_FILENAME, "exec"), {"__name__": "gofi_library"})
    if not _functions:
        raise ValueError("library must declare at least one GoFi function")
