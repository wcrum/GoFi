import asyncio
import unittest
from types import SimpleNamespace
import gofi
from connectrpc.errors import ConnectError
from connectrpc.code import Code
from gofi.server import Worker
from gofi.v1.sandbox_pb2 import WorkerInvokeRequest, WorkerListFunctionsRequest

SOURCE = '''import json
import time
import gofi
@gofi.parser("json")
def decode(raw): return json.loads(raw)
@gofi.transformer("normalize")
def normalize(fields): return {**fields, "app": str(fields["app"]).lower()}
@gofi.port(7000)
def strip(raw): return raw.removeprefix(b"prefix:")
@gofi.listener("empty")
def empty(raw): return b""
@gofi.sink("debug")
def debug(fields, meta): pass
@gofi.parser("slow")
def slow(raw):
    time.sleep(10)
    return {}
'''

class WorkerTest(unittest.TestCase):
    def setUp(self):
        self.worker = Worker(SOURCE, "test-secret", 1)
        self.ctx = SimpleNamespace(request_headers={"authorization": "Bearer test-secret"})

    def test_registry_and_authentication(self):
        result = asyncio.run(self.worker.list_functions(WorkerListFunctionsRequest(), self.ctx))
        self.assertEqual({f.kind for f in result.functions}, {"parser", "transformer", "listener", "sink"})
        with self.assertRaises(ConnectError) as caught:
            asyncio.run(self.worker.list_functions(WorkerListFunctionsRequest(), SimpleNamespace(request_headers={})))
        self.assertEqual(caught.exception.code, Code.UNAUTHENTICATED)

    def test_custom_parser_and_listener(self):
        parsed = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="json", kind="parser", raw=b'{"app":"GoFi"}'), self.ctx))
        self.assertEqual(parsed.fields["app"], "GoFi")
        framed = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="strip", kind="listener", raw=b"prefix:hello"), self.ctx))
        self.assertEqual(framed.raw, b"hello")
        self.assertTrue(framed.HasField("raw"))
        self.assertFalse(framed.HasField("fields"))
        empty = asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="empty", kind="listener", raw=b"data"), self.ctx))
        self.assertTrue(empty.HasField("raw"))
        self.assertEqual(empty.raw, b"")
        self.assertFalse(empty.HasField("fields"))

    def test_deadline_kills_running_function(self):
        with self.assertRaises(ConnectError) as caught:
            asyncio.run(self.worker.invoke(WorkerInvokeRequest(name="slow", kind="parser", raw=b"data"), self.ctx))
        self.assertEqual(caught.exception.code, Code.DEADLINE_EXCEEDED)

    def test_port_decorator_rejects_reserved_port(self):
        with self.assertRaises(ValueError):
            gofi.port(80)

if __name__ == "__main__":
    unittest.main()
