"""Static source tools: requested code is parsed and formatted, never executed."""
import ast
import json
import subprocess
import sys

MAX_SOURCE = 65536
DECORATORS = {"parser", "transformer", "sink", "listener", "port"}


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


def _declares_function(tree):
    modules = {"gofi"}
    imported = set()
    for node in tree.body:
        if isinstance(node, ast.Import):
            modules.update(alias.asname or alias.name for alias in node.names if alias.name == "gofi")
        elif isinstance(node, ast.ImportFrom) and node.module == "gofi":
            imported.update(alias.asname or alias.name for alias in node.names if alias.name in DECORATORS)
    for node in ast.walk(tree):
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        for decorator in node.decorator_list:
            target = decorator.func if isinstance(decorator, ast.Call) else decorator
            if isinstance(target, ast.Attribute) and isinstance(target.value, ast.Name) and target.value.id in modules and target.attr in DECORATORS:
                return True
            if isinstance(target, ast.Name) and target.id in imported:
                return True
    return False


def validate_source(source):
    _source_limit(source)
    try:
        tree = ast.parse(source, filename="<gofi-library>")
    except SyntaxError as error:
        return [{"line": error.lineno or 1, "column": error.offset or 1, "code": "SYNTAX", "message": error.msg}]
    except (ValueError, RecursionError) as error:
        return [{"line": 1, "column": 1, "code": "SYNTAX", "message": str(error)}]
    diagnostics = []
    if not _declares_function(tree):
        diagnostics.append({"line": 1, "column": 1, "code": "GOFI001", "message": "Declare at least one function with a GoFi parser, transformer, sink, listener, or port decorator."})
    result = _ruff(source, ["check", "--select", "E9,F63,F7,F82", "--output-format", "json"])
    if result.returncode not in (0, 1):
        raise RuntimeError("static Python validation failed")
    for diagnostic in json.loads(result.stdout)[:100]:
        location = diagnostic.get("location", {})
        diagnostics.append({"line": location.get("row", 1), "column": location.get("column", 1), "code": diagnostic.get("code") or "RUFF", "message": diagnostic["message"]})
    return diagnostics
