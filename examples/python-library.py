import json
import gofi

@gofi.port(7000, "tcp")
def telemetry_frame(raw: bytes):
    """Strip a device prefix before GoFi parses each TCP frame."""
    return raw.removeprefix(b"device:")

@gofi.parser("telemetry")
def parse_telemetry(raw: bytes):
    """Decode application-specific telemetry JSON."""
    return json.loads(raw)

@gofi.transformer("normalize")
def normalize(fields, meta):
    """Normalize application names before filtering or delivery."""
    return {**fields, "app": str(fields.get("app", "unknown")).lower()}

@gofi.sink("debug")
def debug(fields, meta):
    """Write a sandbox-local debug record; no external egress is enabled."""
    print(json.dumps(fields))
