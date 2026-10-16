"""Record-and-replay debugger (gofi.debugger / execution.debug / Worker.debug)."""
import asyncio
import base64
import unittest

from gofi import debugger
from gofi.execution import debug

LIBRARY = '''import gofi

LIMIT = 2
seen = []

def double(value):
    return value * 2

@gofi.transformer("count")
def count(fields, meta):
    total = 0
    for item in fields["items"]:
        if item > LIMIT:
            total += double(item)
        else:
            total += 1
    seen.append(total)
    print("total", total)
    return {"total": total}

@gofi.transformer("boom")
def boom(fields):
    value = fields["n"]
    raise ValueError(f"bad {value}")

@gofi.transformer("spin")
def spin(fields):
    n = 0
    while True:
        n += 1

class Loud:
    def __repr__(self):
        raise RuntimeError("no repr for you")

@gofi.transformer("odd")
def odd(fields):
    thing = Loud()
    big = list(range(200))
    nested = {"a": {"b": {"c": {"d": 1}}}}
    return fields
'''

INLINE = '''import gofi as _gofi
_gofi._inline("enrich", "def transform(fields, meta):\\n    x = fields['n'] + 1\\n    return {'x': x}\\n")
'''


def lines(source, text):
    return source.splitlines().index(text) + 1


class RecordingTests(unittest.TestCase):
    def test_steps_locals_watches_return_and_stdout(self):
        out = debug(LIBRARY, "count", "transformer", b"", {"items": [1, 5]}, {}, 10, watches=["total", "total * 10", "missing"])
        result, recording = out["result"], out["recording"]
        self.assertEqual(result["fields"], {"total": 11})
        steps = recording["steps"]
        self.assertFalse(recording["truncated"])
        self.assertEqual(steps[0]["event"], "call")
        self.assertEqual(steps[0]["line"], lines(LIBRARY, '@gofi.transformer("count")'))
        self.assertEqual(result["entryLine"], lines(LIBRARY, "def count(fields, meta):"))
        body = [s["line"] for s in steps if s["event"] == "line" and s["depth"] == 0]
        self.assertEqual(body[:3], [lines(LIBRARY, "    total = 0"), lines(LIBRARY, '    for item in fields["items"]:'), lines(LIBRARY, "        if item > LIMIT:")])
        # Both branches appear, and the helper call is a nested frame.
        self.assertIn(lines(LIBRARY, "            total += 1"), body)
        self.assertIn(lines(LIBRARY, "            total += double(item)"), body)
        helper = [s for s in steps if s["function"] == "double"]
        self.assertTrue(helper and helper[0]["depth"] == 1 and [f["function"] for f in helper[0]["stack"]] == ["count", "double"])
        # The counter changes and is flagged.
        totals = [(next((v for v in s["locals"] if v["name"] == "total"), None), s) for s in steps if s["function"] == "count"]
        changes = [v["repr"] for v, _ in totals if v and v.get("changed")]
        self.assertEqual(changes, ["0", "1", "11"])
        # Watches evaluate per step, with per-watch errors.
        last_line = [s for s in steps if s["event"] == "line" and s["function"] == "count"][-1]
        watch = {w["expression"]: w for w in last_line["watches"]}
        self.assertEqual(watch["total * 10"]["repr"], "110")
        self.assertIn("NameError", watch["missing"]["error"])
        # The final step returns the value; stdout length grows after print.
        final = steps[-1]
        self.assertEqual((final["event"], final["returnValue"]["repr"]), ("return", "{'total': 11}"))
        self.assertEqual(final["stdoutLength"], len("total 11\n"))
        before_print = [s for s in steps if s["line"] == lines(LIBRARY, '    print("total", total)')][0]
        self.assertEqual(before_print["stdoutLength"], 0)
        # Library globals: initial snapshot, then changes.
        self.assertEqual({g["name"] for g in recording["initialGlobals"]}, {"LIMIT", "seen"})
        changed = [g for s in steps for g in s.get("changedGlobals", [])]
        self.assertEqual([g["repr"] for g in changed if g["name"] == "seen"], ["[11]"])

    def test_truncates_at_max_steps(self):
        out = debug(LIBRARY, "count", "transformer", b"", {"items": list(range(100))}, {}, 10, max_steps=25)
        recording = out["recording"]
        self.assertEqual(len(recording["steps"]), 25)
        self.assertTrue(recording["truncated"])
        self.assertEqual(recording["truncationReason"], "max_steps")
        self.assertEqual(out["result"]["fields"]["total"], 3 + sum(2 * i for i in range(3, 100)))

    def test_raising_function_ends_with_an_exception_event(self):
        out = debug(LIBRARY, "boom", "transformer", b"", {"n": 7}, {}, 10)
        events = [s["event"] for s in out["recording"]["steps"]]
        self.assertIn("exception", events)
        exception = [s for s in out["recording"]["steps"] if s["event"] == "exception"][0]
        self.assertEqual(exception["exception"], "ValueError: bad 7")
        self.assertEqual(exception["line"], lines(LIBRARY, '    raise ValueError(f"bad {value}")'))
        self.assertIn("ValueError", out["result"]["error"])
        self.assertEqual(out["result"]["errorLine"], exception["line"])

    def test_infinite_loop_stops_at_the_budget_with_a_recording(self):
        out = debug(LIBRARY, "spin", "transformer", b"", {}, {}, 2, max_steps=50)
        self.assertTrue(out["recording"]["truncated"])
        self.assertEqual(len(out["recording"]["steps"]), 50)
        self.assertIn("time budget", out["result"]["error"])

    def test_unsafe_reprs_and_large_containers_are_bounded(self):
        out = debug(LIBRARY, "odd", "transformer", b"", {}, {}, 10)
        final = {v["name"]: v for v in out["recording"]["steps"][-1]["locals"]}
        # A raising __repr__ never breaks the recording (reprlib falls back).
        self.assertIn("Loud", final["thing"]["repr"])
        self.assertEqual(len(final["big"]["children"]), debugger.MAX_WIDTH)
        self.assertTrue(final["big"]["truncated"])
        level1 = final["nested"]["children"][0]
        self.assertEqual(level1["children"][0]["name"], "'b'")
        self.assertNotIn("children", level1["children"][0])

    def test_inline_snippet_coordinates(self):
        out = debug(INLINE, "enrich", "transformer", b"", {"n": 1}, {}, 10)
        self.assertEqual(out["result"]["entryLine"], 1)
        self.assertEqual([s["line"] for s in out["recording"]["steps"]], [1, 2, 3, 3])


class WorkerDebugTests(unittest.TestCase):
    def test_worker_returns_a_debug_recording_message(self):
        from gofi.server import Worker
        from gofi.v1.sandbox_pb2 import WorkerDebugRequest
        from google.protobuf.struct_pb2 import Struct

        class Context:
            request_headers = {"authorization": "Bearer t"}

        fields = Struct()
        fields.update({"items": [1, 5]})
        worker = Worker(LIBRARY, "t", 10)
        recording = asyncio.run(worker.debug(WorkerDebugRequest(name="count", kind="transformer", fields=fields, watches=["total"], invocation_id="dbg-1"), Context()))
        self.assertGreater(len(recording.steps), 10)
        self.assertEqual(recording.run_id, "dbg-1")
        self.assertEqual(recording.result.fields["total"], 11)
        # Struct input numbers arrive as floats, as in normal invocations.
        self.assertEqual(recording.steps[-1].return_value.repr, "{'total': 11.0}")
        self.assertEqual(recording.entry_line, lines(LIBRARY, "def count(fields, meta):"))
        self.assertTrue(any(v.changed for s in recording.steps for v in s.locals))
        self.assertEqual(base64.b64encode(b"").decode(), "")


if __name__ == "__main__":
    unittest.main()
