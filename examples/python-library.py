import json
import zlib
import gofi

# One decorator per flow step: framer -> decoder -> parser -> transformer ->
# encoder -> sink. Each appears in the matching step of the flow editor.

@gofi.framer("records")
def records(buffer: bytes, final: bool):
    """Split a stream of ';'-terminated records; flush a trailing record at the end."""
    *frames, rest = buffer.split(b";")
    if final and rest:
        frames, rest = frames + [rest], b""
    return frames, rest

@gofi.decoder("strip-device")
def strip_device(data: bytes):
    """Remove a device prefix before parsing."""
    return data.removeprefix(b"device:")

@gofi.parser("telemetry")
def parse_telemetry(raw: bytes):
    """Decode application-specific telemetry JSON."""
    return json.loads(raw)

@gofi.transformer("normalize")
def normalize(fields, meta):
    """Normalize application names before filtering or delivery."""
    return {**fields, "app": str(fields.get("app", "unknown")).lower()}

@gofi.encoder("compress")
def compress(data: bytes, *, level: int = 6):
    """Compress the outgoing payload; `level` is per-node configuration."""
    return zlib.compress(data, level)

@gofi.sink("debug")
def debug(fields, meta):
    """Write a sandbox-local debug record; no external egress is enabled."""
    print(json.dumps(fields))
