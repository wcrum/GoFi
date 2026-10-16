import base64
import asyncio
import unittest
from types import SimpleNamespace
import gofi
from connectrpc.errors import ConnectError
from connectrpc.code import Code
from gofi.server import Worker
from google.protobuf.struct_pb2 import Struct
from gofi.v1.sandbox_pb2 import DescribePythonEnvironmentRequest, WorkerInvokeRequest, WorkerListFunctionsRequest

SOURCE = '''import json
import time
import gofi
@gofi.parser("json")
def decode(raw): return json.loads(raw)
@gofi.transformer("normalize")
def normalize(fields): return {**fields, "app": str(fields["app"]).lower()}
@gofi.decoder
def strip(raw): return raw.removeprefix(b"prefix:")
@gofi.decoder("empty")
def empty(raw): return b""
@gofi.decoder("wrong")
def wrong(raw): return {"not": "bytes"}
@gofi.encoder("tag")
def tag(data, fields): return data + b"|" + fields["app"].encode()
@gofi.framer("pipes")
def pipes(buffer, final):
    *frames, rest = buffer.split(b"|")
    if final and rest:
        frames, rest = frames + [rest], b""
    return frames, rest
@gofi.sink("debug")
def debug(fields, meta): pass
@gofi.parser("slow")
def slow(raw):
    time.sleep(10)
    return {}
@gofi.transformer("threshold")
def threshold(fields, meta, limit: int = 30, label: str = "hot"):
    """Keep hot readings.

    limit: minimum temperature
    """
    print("checking", fields["temperature"], "against", limit)
    return {**fields, "label": label} if fields["temperature"] >= limit else None
CALLS = []
@gofi.transformer("count")
def count(fields):
    CALLS.append(1)
    return {"calls": len(CALLS)}
@gofi.transformer("explode")
def explode(fields):
    print("about to fail")
    return fields["missing"]
'''

class WorkerTest(unittest.TestCase):
    def setUp(self):
        self.worker = Worker(SOURCE, "test-secret", 1)
        self.addCleanup(self.worker.executor.stop)
        self.ctx = SimpleNamespace(request_headers={"authorization": "Bearer test-secret"})

    def test_registry_and_authentication(self):
        result = asyncio.run(self.worker.list_functions(WorkerListFunctionsRequest(), self.ctx))
        self.assertEqual({f.kind for f in result.functions}, {"parser", "transformer", "decoder", "encoder", "framer", "sink"})
        with self.assertRaises(ConnectError) as caught:
            asyncio.run(self.worker.list_functions(WorkerListFunctionsRequest(), SimpleNamespace(request_headers={})))
        self.assertEqual(caught.exception.code, Code.UNAUTHENTICATED)

    def test_parser_and_codec_steps(self):
        parsed = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="json", kind="parser", raw=b'{"app":"GoFi"}'), self.ctx))
        self.assertEqual(parsed.fields["app"], "GoFi")
        decoded = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="strip", kind="decoder", raw=b"prefix:hello"), self.ctx))
        self.assertEqual(decoded.raw, b"hello")
        self.assertTrue(decoded.HasField("raw"))
        self.assertFalse(decoded.HasField("fields"))
        empty = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="empty", kind="decoder", raw=b"data"), self.ctx))
        self.assertTrue(empty.HasField("raw"))
        self.assertEqual(empty.raw, b"")
        wrong = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="wrong", kind="decoder", raw=b"data"), self.ctx))
        self.assertIn("must return bytes", wrong.error)
        fields = Struct(); fields.update({"app": "gofi"})
        encoded = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="tag", kind="encoder", raw=b"payload", fields=fields), self.ctx))
        self.assertEqual(encoded.raw, b"payload|gofi")

    def test_framer_splits_and_flushes(self):
        meta = Struct(); meta.update({"final": "false"})
        partial = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="pipes", kind="framer", raw=b"a|b|tail", meta=meta), self.ctx))
        frames = [base64.b64decode(f) for f in partial.fields["frames"]]
        self.assertEqual((frames, base64.b64decode(partial.fields["rest"])), ([b"a", b"b"], b"tail"))
        meta.update({"final": "true"})
        flushed = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="pipes", kind="framer", raw=b"tail", meta=meta), self.ctx))
        self.assertEqual([base64.b64decode(f) for f in flushed.fields["frames"]], [b"tail"])

    def test_deadline_kills_running_function(self):
        with self.assertRaises(ConnectError) as caught:
            asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="slow", kind="parser", raw=b"data"), self.ctx))
        self.assertEqual(caught.exception.code, Code.DEADLINE_EXCEEDED)

    def test_warm_executor_keeps_state_until_a_deadline_resets_it(self):
        calls = lambda: asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="count", kind="transformer"), self.ctx)).fields["calls"]
        self.assertEqual([calls(), calls()], [1, 2])
        with self.assertRaises(ConnectError):
            asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="slow", kind="parser", raw=b"data"), self.ctx))
        self.assertEqual(calls(), 1, "a killed executor restarts with fresh module state")
        self.worker.executor.calls = self.worker.executor.MAX_CALLS
        self.assertEqual(calls(), 1, "the executor is recycled after MAX_CALLS")

    def test_params_are_reported(self):
        listed = {f.name: f for f in asyncio.run(self.worker.list_functions(WorkerListFunctionsRequest(), self.ctx)).functions}
        self.assertEqual([p.name for p in listed["tag"].params], [], "fields is data, not configuration")
        self.assertEqual([p.name for p in listed["pipes"].params], [], "final is data, not configuration")
        params = {p.name: p for p in listed["threshold"].params}
        self.assertEqual(params["limit"].type, "int")
        self.assertEqual(params["limit"].default.number_value, 30)
        self.assertEqual(params["limit"].description, "minimum temperature")
        self.assertFalse(params["limit"].required)
        self.assertEqual(params["label"].default.string_value, "hot")
        self.assertEqual([p.name for p in listed["normalize"].params], [])

    def test_config_stdout_and_duration(self):
        config = Struct()
        config.update({"limit": 20, "label": "warm"})
        fields = Struct()
        fields.update({"temperature": 25})
        result = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="threshold", kind="transformer", fields=fields, config=config), self.ctx))
        self.assertEqual(result.fields["label"], "warm")
        self.assertIn("checking 25.0 against 20\n", result.stdout)  # fields stay JSON numbers; int config is coerced
        self.assertGreater(result.duration_us, 0)
        dropped = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="threshold", kind="transformer", fields=fields), self.ctx))
        self.assertTrue(dropped.drop)
        bad = Struct()
        bad.update({"nope": 1})
        rejected = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="threshold", kind="transformer", fields=fields, config=bad), self.ctx))
        self.assertIn("unknown configuration", rejected.error)

    def test_user_errors_keep_stdout_and_location(self):
        result = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="explode", kind="transformer"), self.ctx))
        self.assertIn("KeyError", result.error)
        self.assertEqual(result.error_location.line, SOURCE.splitlines().index('    return fields["missing"]') + 1)
        self.assertIn("about to fail", result.stdout)

    def test_lazy_worker_reports_load_errors(self):
        lazy = Worker("import gofi\nraise RuntimeError('boom')\n", "test-secret", 2, lazy=True)
        self.assertEqual(len(asyncio.run(lazy.list_functions(WorkerListFunctionsRequest(), self.ctx)).functions), 0)
        result = asyncio.run(lazy.invoke(WorkerInvokeRequest(name="", kind="transformer"), self.ctx))
        self.assertIn("boom", result.error)
        self.assertEqual(result.error_location.line, 2)
        single = Worker("import gofi\n@gofi.transformer\ndef only(fields):\n    return {'ok': True}\n", "test-secret", 2, lazy=True)
        self.assertTrue(asyncio.run(single.invoke(WorkerInvokeRequest(name="", kind="transformer"), self.ctx)).fields["ok"])

    def test_removed_decorators_explain_migration(self):
        lazy = Worker("import gofi\n@gofi.ingress(gofi.TCP(7000))\ndef old(frame):\n    return frame\n", "test-secret", 2, lazy=True)
        result = asyncio.run(lazy.invoke(WorkerInvokeRequest(name="", kind="decoder"), self.ctx))
        self.assertIn("@gofi.framer", result.error)
        with self.assertRaisesRegex(AttributeError, "no attribute 'nope'"):
            gofi.nope

    def test_describe_environment_lists_curated_packages(self):
        env = asyncio.run(self.worker.describe_environment(DescribePythonEnvironmentRequest(), self.ctx))
        self.assertTrue(env.python_version)
        signatures = " ".join(d.name for d in env.decorators)
        for name in ("framer", "decoder", "parser", "transformer", "encoder", "sink"):
            self.assertIn(name, signatures)
        self.assertNotIn("ingress", signatures)
        names = {p.name.lower() for p in env.packages}
        self.assertNotIn("uvicorn", names)
        self.assertNotIn("connectrpc", names)

if __name__ == "__main__":
    unittest.main()
