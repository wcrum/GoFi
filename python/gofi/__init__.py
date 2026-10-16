"""GoFi function decorators for isolated Python libraries.

Each decorator matches one step of a flow:

    framer       (buffer: bytes[, final]) -> (frames: list[bytes], rest: bytes)
    decoder      (data: bytes) -> bytes                  before parsing
    parser       (frame: bytes) -> dict | None           None rejects the frame
    transformer  (fields[, meta]) -> dict | None         None drops the message
    encoder      (data: bytes[, fields[, meta]]) -> bytes  before a sink sends
    sink         (fields[, meta]) -> anything            delivers the message

Keyword parameters after the data arguments are per-node configuration::

    @gofi.transformer("threshold")
    def threshold(fields, meta, limit: float = 30.0):
        return fields if fields["temperature"] >= limit else None
"""
from dataclasses import dataclass, field
import inspect
import linecache
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

    # Backwards-compatible accessors for the deprecated @gofi.port form.



_functions: dict[tuple[str, str], Function] = {}

_TYPE_NAMES = {str: "str", int: "int", float: "float", bool: "bool", list: "list", dict: "dict"}


# Steps that receive bytes as their first argument.
BYTE_KINDS = ("parser", "decoder", "framer", "encoder")


def data_arity(kind, callback):
    """Number of leading positional data arguments the callback receives."""
    positional = [p for p in inspect.signature(callback).parameters.values() if p.kind in (p.POSITIONAL_ONLY, p.POSITIONAL_OR_KEYWORD)]
    if kind in ("parser", "decoder"):
        return 1
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


def _decorate(kind, name=None):
    def register(callback):
        key = name or callback.__name__
        if not key or len(key) > 80:
            raise ValueError("function name must contain 1-80 characters")
        if (kind, key) in _functions:
            raise ValueError(f"duplicate {kind} function: {key}")
        _functions[kind, key] = Function(key, kind, (callback.__doc__ or "").strip(), callback, _params(kind, callback))
        return callback
    if callable(name):
        callback, name = name, None
        return register(callback)
    return register


def parser(name=None):
    """Decode one frame (bytes) into a fields mapping, or None to reject it."""
    return _decorate("parser", name)


def transformer(name=None):
    """Return new fields, or None to drop the message. Usable as a flow stage."""
    return _decorate("transformer", name)


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


# Public decorators, reported by DescribeEnvironment.
DECORATORS = (framer, decoder, parser, transformer, encoder, sink)

_REMOVED = {
    "ingress": "@gofi.ingress was removed: set the port on the Flow's Listen source, and use @gofi.framer to split frames or @gofi.decoder to transform bytes before parsing",
    "listener": "@gofi.listener was removed: use @gofi.framer or @gofi.decoder in the Flow's Frame or Decode step",
    "TCP": "gofi.TCP was removed: set the port on the Flow's Listen source",
    "UDP": "gofi.UDP was removed: set the port on the Flow's Listen source",
}


def __getattr__(name):
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
