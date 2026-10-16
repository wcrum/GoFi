#!/usr/bin/env python3
"""Verify outer ConnectRPC Python tooling against a running local GoFi portal.

Requires python/requirements.txt and the current generated Python bindings.
Creates one uniquely named test library and deletes it before exiting. Source
formatting/validation use GoFi's private isolated tooling worker.
"""
import argparse
import secrets

from connectrpc.code import Code
from connectrpc.errors import ConnectError
from gofi.v1.sandbox_connect import SandboxServiceClientSync
from gofi.v1.sandbox_pb2 import (
    ApplySandboxRequest,
    DeleteSandboxRequest,
    FormatSourceRequest,
    ListLibraryFunctionsRequest,
    ListSandboxesRequest,
    RunFunctionRequest,
    SandboxDefinition,
    ValidateSourceRequest,
)

SOURCE = '''import gofi
@gofi.parser("decode")
def decode(raw):return {"message":raw.decode("utf-8")}
@gofi.parser("reject")
def reject(raw):return None
'''


def verify(address):
    name = "tool-smoke-" + secrets.token_hex(5)
    with SandboxServiceClientSync(address, timeout_ms=70000) as client:
        existing = {s.name for s in client.list_sandboxes(ListSandboxesRequest()).sandboxes}
        formatted = client.format_source(FormatSourceRequest(source=SOURCE))
        assert formatted.source != SOURCE, "formatter did not change unformatted source"
        checked = client.validate_source(ValidateSourceRequest(source=formatted.source))
        assert checked.valid, [d.message for d in checked.diagnostics]
        invalid = client.validate_source(ValidateSourceRequest(source="def broken(\n"))
        assert not invalid.valid and invalid.diagnostics, "syntax diagnostics missing"
        assert invalid.diagnostics[0].line >= 1 and invalid.diagnostics[0].message
        # A top-level raise must be parsed/formatted, never executed by tooling.
        inert = "raise RuntimeError('tooling must not execute source')\n"
        assert client.validate_source(ValidateSourceRequest(source=inert)).valid
        assert client.format_source(FormatSourceRequest(source=inert)).source
        current = {s.name for s in client.list_sandboxes(ListSandboxesRequest()).sandboxes}
        assert not any(n.startswith("gofi-tools-") for n in current - existing), "private tooling became a public Sandbox"
        print("FORMAT_PASS VALIDATE_PASS NO_EXEC_PASS PRIVATE_TOOLS_PASS", flush=True)

        try:
            applied = client.apply_sandbox(ApplySandboxRequest(sandbox=SandboxDefinition(
                name=name, source_code=formatted.source, timeout_seconds=5, memory_mib=128,
            )))
            assert applied.status.phase == "Ready", applied.status.error
            registered = client.list_functions(ListLibraryFunctionsRequest(library=name))
            assert {(f.name, f.kind) for f in registered.functions} == {
                ("decode", "parser"), ("reject", "parser"),
            }
            result = client.run_function(RunFunctionRequest(
                library=name, name="decode", kind="parser", raw=b"GoFi sample",
            ))
            assert result.fields["message"] == "GoFi sample" and not result.drop
            rejected = client.run_function(RunFunctionRequest(
                library=name, name="reject", kind="parser", raw=b"sample",
            ))
            assert rejected.drop, "explicit None result lost its rejection"
            try:
                client.run_function(RunFunctionRequest(
                    library=name, name="decode", kind="sink", raw=b"sample",
                ))
            except ConnectError as error:
                assert error.code == Code.NOT_FOUND, error
            else:
                raise AssertionError("function kind mismatch was accepted")
            print("RUN_PASS REJECTION_PASS KIND_ERROR_PASS", flush=True)
        finally:
            try:
                client.delete_sandbox(DeleteSandboxRequest(name=name))
            except ConnectError as error:
                if error.code != Code.NOT_FOUND:
                    raise
        print("CLEANUP_PASS", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api", default="http://127.0.0.1:9090")
    verify(parser.parse_args().api)


if __name__ == "__main__":
    main()
