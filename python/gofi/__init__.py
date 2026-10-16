"""GoFi function decorators for isolated Python libraries.

Parsers/listeners receive bytes. Transformers/sinks receive a fields mapping
and optionally a metadata mapping. Return None from a parser/transformer to
stop that message, bytes from a listener to replace its frame, or a dict to
replace decoded fields. Sinks returning None retain the original message.
"""
from dataclasses import dataclass
from typing import Callable

@dataclass(frozen=True)
class Function:
    name: str
    kind: str
    description: str
    callback: Callable
    port: int | None = None
    network: str | None = None

_functions: dict[tuple[str, str], Function] = {}

def _decorate(kind, name=None, *, port=None, network=None):
    def register(callback):
        key = name or callback.__name__
        if not key or len(key) > 80:
            raise ValueError("function name must contain 1-80 characters")
        if (kind, key) in _functions:
            raise ValueError(f"duplicate {kind} function: {key}")
        _functions[kind, key] = Function(key, kind, (callback.__doc__ or "").strip(), callback, port, network)
        return callback
    if callable(name):
        callback, name = name, None
        return register(callback)
    return register

def parser(name=None):
    return _decorate("parser", name)

def transformer(name=None):
    return _decorate("transformer", name)

def sink(name=None):
    return _decorate("sink", name)

def listener(name=None):
    return _decorate("listener", name)

def port(number: int, network: str = "tcp", *, name=None):
    """Declare a listener hook. GoFi owns the actual TCP/UDP socket."""
    if not isinstance(number, int) or isinstance(number, bool) or not 1024 <= number <= 65535:
        raise ValueError("port must be between 1024 and 65535")
    if network not in ("tcp", "udp"):
        raise ValueError("network must be tcp or udp")
    return _decorate("listener", name, port=number, network=network)

def functions():
    return tuple(sorted(_functions.values(), key=lambda f: (f.kind, f.name)))

def _load(source):
    _functions.clear()
    exec(compile(source, "<gofi-library>", "exec"), {"__name__": "gofi_library"})
    if not _functions:
        raise ValueError("library must declare at least one GoFi function")
