import json
import zlib
from pydantic import BaseModel, Field, ValidationError
import gofi

# One decorator per flow step: framer -> decoder -> parser -> transformer ->
# encoder -> sink. Each appears in the matching step of the flow editor.
# Annotations are optional; they document what each step receives and returns.


class Telemetry(BaseModel):
    """The shape of one telemetry record. Unknown keys are ignored; types are coerced."""

    app: str = "unknown"
    sensor: str = Field(min_length=1)
    temperature: float = Field(ge=-50, le=150)


@gofi.framer("records")
def records(buffer: bytes, final: bool) -> tuple[list[bytes], bytes]:
    """Split a stream of ';'-terminated records; flush a trailing record at the end."""
    *frames, rest = buffer.split(b";")
    if final and rest:
        frames, rest = frames + [rest], b""
    return frames, rest


@gofi.decoder("strip-device")
def strip_device(data: bytes) -> bytes:
    """Remove a device prefix before parsing."""
    return data.removeprefix(b"device:")


@gofi.parser("telemetry")
def parse_telemetry(raw: bytes) -> dict | None:
    """Decode and validate telemetry JSON; a record that does not match Telemetry is dropped."""
    try:
        return Telemetry.model_validate_json(raw).model_dump()
    except ValidationError as error:
        print("rejected:", error.errors(include_url=False, include_input=False))
        return None


@gofi.transformer("normalize")
def normalize(fields: dict, meta: dict) -> dict:
    """Normalize application names before filtering or delivery."""
    return {**fields, "app": str(fields.get("app", "unknown")).lower()}


@gofi.encoder("compress")
def compress(data: bytes, *, level: int = 6) -> bytes:
    """Compress the outgoing payload; `level` is per-node configuration."""
    return zlib.compress(data, level)


@gofi.sink("debug")
def debug(fields: dict, meta: dict) -> None:
    """Write a sandbox-local debug record; no external egress is enabled."""
    print(json.dumps(fields))
