"""gofi.text: tokenizers for common frame layouts. Each helper knows how the
bytes are laid out and returns plain strings; what the fields mean (types,
required keys, aliases) belongs to a pydantic model given to the decorator
as ``model=``. Nothing here guesses the format: the author names it, as a
flow names ``parser.format``.

    @gofi.parser("key_value", model=Fields)
    def key_value(data: bytes) -> dict | None:
        return gofi.KeyValue(data)

The package exports these as gofi.KeyValue, gofi.JSON, gofi.CSV, and
gofi.Delimited; this module is the implementation.
"""
import csv as _csv
import io
import json as _json
import re

__all__ = ["keyvalue", "json", "csv", "delimited"]

# KEY=VALUE pairs: any whitespace, newlines, or commas between them; values
# may be double- or single-quoted (quotes removed); bare values end at
# whitespace or a comma.
_PAIR = re.compile(r'([A-Za-z_][\w.\-]*)=("([^"]*)"|\'([^\']*)\'|([^\s,]+))')


def _text(data, encoding):
    if isinstance(data, bytes | bytearray | memoryview):
        return bytes(data).decode(encoding, errors="replace")
    return str(data)


def keyvalue(data, *, encoding="utf-8"):
    """``host=a1 temperature=31.5 msg="disk full"`` -> ``{"host": "a1", ...}``;
    every value is a string. A repeated key keeps its last value. Returns an
    empty dict when the frame holds no pairs (a parser then returns None)."""
    return {m.group(1): m.group(3) if m.group(3) is not None else m.group(4) if m.group(4) is not None else m.group(5) for m in _PAIR.finditer(_text(data, encoding))}


def json(data, *, encoding="utf-8"):
    """One JSON object per frame -> its dict (typed as JSON types them).
    Anything else (an array, a scalar, invalid JSON) raises ValueError."""
    try:
        value = _json.loads(_text(data, encoding))
    except _json.JSONDecodeError as error:
        raise ValueError(f"frame is not JSON: {error.msg} at {error.pos}") from None
    if not isinstance(value, dict):
        raise ValueError("frame is JSON but not an object")
    return value


def csv(data, columns, *, delimiter=",", encoding="utf-8"):
    """One CSV row per frame -> ``{column: value}`` for the given column
    names, in order; extra cells are dropped, missing ones are absent."""
    row = next(_csv.reader(io.StringIO(_text(data, encoding).strip("\r\n")), delimiter=delimiter), [])
    return {name: cell for name, cell in zip(columns, row)}


def delimited(data, sep, columns=None, *, encoding="utf-8"):
    """A positional frame split on ``sep`` (``"|"``, ``"\\t"``, ...): with
    ``columns`` -> ``{column: value}``; without -> ``{"f0": ..., "f1": ...}``."""
    cells = _text(data, encoding).strip("\r\n").split(sep)
    names = columns if columns is not None else [f"f{index}" for index in range(len(cells))]
    return {name: cell for name, cell in zip(names, cells)}
