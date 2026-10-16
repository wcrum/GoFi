"""Static source tools: requested code is parsed and formatted, never executed."""
import ast
import json
import subprocess
import sys

MAX_SOURCE = 65536


def _source_limit(source):
    if len(source.encode("utf-8")) > MAX_SOURCE:
        raise ValueError("source exceeds 65536 bytes")


def _ruff(source, arguments):
    _source_limit(source)
    try:
        return subprocess.run([sys.executable, "-m", "ruff", *arguments, "--isolated", "--no-cache", "--target-version", "py314", "--stdin-filename", "library.py", "-"], input=source, text=True, capture_output=True, timeout=5, check=False)
    except subprocess.TimeoutExpired as error:
        raise TimeoutError("static source tooling exceeded its deadline") from error



def format_source(source):
    result = _ruff(source, ["format"])
    if result.returncode:
        raise ValueError("source cannot be formatted; validate Python syntax first")
    return result.stdout


def validate_source(source):
    _source_limit(source)
    try:
        ast.parse(source, filename="<gofi-library>")
    except SyntaxError as error:
        return [{"line": error.lineno or 1, "column": error.offset or 1, "code": "SYNTAX", "message": error.msg}]
    except (ValueError, RecursionError) as error:
        return [{"line": 1, "column": 1, "code": "SYNTAX", "message": str(error)}]
    diagnostics = []
    result = _ruff(source, ["check", "--select", "E9,F63,F7,F82", "--output-format", "json"])
    if result.returncode not in (0, 1):
        raise RuntimeError("static Python validation failed")
    for diagnostic in json.loads(result.stdout)[:100]:
        location = diagnostic.get("location", {})
        diagnostics.append({"line": location.get("row", 1), "column": location.get("column", 1), "code": diagnostic.get("code") or "RUFF", "message": diagnostic["message"]})
    return diagnostics
