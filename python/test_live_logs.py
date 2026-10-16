"""Live worker logs, entry lines, and line traces (gofi.execution / gofi.logs)."""
import contextlib
import json
import os
import tempfile
import unittest

from gofi import logs
from gofi.execution import MAX_TRACE_LINES, invoke

LIBRARY = '''import gofi
import time

def helper(value):
    return value * 2

@gofi.transformer(
    "branch",
)
def branch(fields, meta):
    if fields["n"] > 10:
        fields["size"] = "big"
    else:
        fields["size"] = "small"
    return fields

@gofi.parser("chatty")
async def chatty(raw):
    print("first")
    print("second", flush=True)
    print("partial", end="")
    return {"n": helper(1)}

@gofi.transformer("boom")
def boom(fields):
    raise RuntimeError("bad input")
'''

INLINE = '''import gofi as _gofi
_gofi._inline("enrich", "\\n\\ndef transform(fields, meta):\\n    print('inline says hi')\\n    return {**fields, 'via': 'inline'}\\n")
'''


@contextlib.contextmanager
def captured_fds():
    """Collect what child processes write to fds 1 and 2."""
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        saved = os.dup(1), os.dup(2)
        os.dup2(out.fileno(), 1)
        os.dup2(err.fileno(), 2)
        records = []
        try:
            yield records
        finally:
            os.dup2(saved[0], 1)
            os.dup2(saved[1], 2)
            for handle in saved:
                os.close(handle)
            for stream in (out, err):
                stream.seek(0)
                records.extend(json.loads(line) for line in stream.read().decode().splitlines() if line.startswith("{"))


class EntryLineTests(unittest.TestCase):
    def test_entry_line_is_the_def_after_multiline_decorators(self):
        result = invoke(LIBRARY, "branch", "transformer", b"", {"n": 1}, {}, 10)
        self.assertEqual(result["entryLine"], LIBRARY.splitlines().index("def branch(fields, meta):") + 1)

    def test_entry_line_reported_with_errors(self):
        result = invoke(LIBRARY, "boom", "transformer", b"", {}, {}, 10)
        self.assertIn("RuntimeError", result["error"])
        self.assertEqual(result["entryLine"], LIBRARY.splitlines().index("def boom(fields):") + 1)
        self.assertEqual(result["errorLine"], result["entryLine"] + 1)

    def test_inline_lines_are_relative_to_the_snippet(self):
        result = invoke(INLINE, "enrich", "transformer", b"", {"a": 1}, {}, 10, trace=True)
        self.assertEqual(result["fields"], {"a": 1, "via": "inline"})
        self.assertEqual(result["entryLine"], 3)
        self.assertEqual(result["executedLines"], [4, 5])


class TraceTests(unittest.TestCase):
    def test_trace_is_off_by_default(self):
        self.assertEqual(invoke(LIBRARY, "branch", "transformer", b"", {"n": 1}, {}, 10)["executedLines"], [])

    def test_different_inputs_take_different_paths(self):
        lines = LIBRARY.splitlines()
        big = invoke(LIBRARY, "branch", "transformer", b"", {"n": 50}, {}, 10, trace=True)["executedLines"]
        small = invoke(LIBRARY, "branch", "transformer", b"", {"n": 1}, {}, 10, trace=True)["executedLines"]
        self.assertIn(lines.index('        fields["size"] = "big"') + 1, big)
        self.assertNotIn(lines.index('        fields["size"] = "big"') + 1, small)
        self.assertIn(lines.index('        fields["size"] = "small"') + 1, small)

    def test_trace_follows_helpers_in_the_same_source_and_async(self):
        traced = invoke(LIBRARY, "chatty", "parser", b"x", {}, {}, 10, trace=True)["executedLines"]
        self.assertIn(LIBRARY.splitlines().index("    return value * 2") + 1, traced)
        self.assertLessEqual(len(traced), MAX_TRACE_LINES)


class LiveEchoTests(unittest.TestCase):
    def test_output_is_echoed_live_with_the_invocation_id(self):
        with captured_fds() as records:
            result = invoke(LIBRARY, "chatty", "parser", b"x", {}, {}, 10, invocation_id="run-abc")
        self.assertEqual(result["stdout"], "first\nsecond\npartial")
        user = [r for r in records if r["stream"] == "stdout"]
        self.assertEqual([r["line"] for r in user], ["first", "second", "partial"])
        self.assertTrue(all(r["invocationId"] == "run-abc" and r["function"] == "chatty" and r["kind"] == "parser" for r in user))
        system = [r["line"] for r in records if r["stream"] == "system"]
        self.assertTrue(system[0].startswith("invocation started: parser chatty"))
        self.assertTrue(system[-1].startswith("invocation finished in "))

    def test_failures_and_stderr_are_logged(self):
        source = 'import gofi, sys\n@gofi.transformer("warn")\ndef warn(fields):\n    print("careful", file=sys.stderr)\n    raise ValueError("nope")\n'
        with captured_fds() as records:
            invoke(source, "warn", "transformer", b"", {}, {}, 10, invocation_id="i1")
        self.assertIn({"stream": "stderr", "line": "careful"}, [{"stream": r["stream"], "line": r["line"]} for r in records])
        failure = [r for r in records if r.get("level") == "error"]
        self.assertTrue(failure and "line 5" in failure[0]["line"] and "ValueError" in failure[0]["line"])

    def test_echo_is_bounded_per_invocation(self):
        source = 'import gofi\n@gofi.transformer("flood")\ndef flood(fields):\n    for i in range(%d):\n        print(i)\n    return fields\n' % (logs.MAX_ECHO_LINES + 50)
        with captured_fds() as records:
            invoke(source, "flood", "transformer", b"", {}, {}, 20, invocation_id="f")
        self.assertEqual(len([r for r in records if r["stream"] == "stdout"]), logs.MAX_ECHO_LINES)
        self.assertTrue(any("truncated" in r["line"] for r in records if r["stream"] == "system"))


if __name__ == "__main__":
    unittest.main()
