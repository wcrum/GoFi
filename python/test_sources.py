"""Flow source decorators (@gofi.fetcher, @gofi.streamer) and the worker's
Fetch and Stream RPCs, including cancellation and bounded streaming."""
import asyncio
import json
import os
import tempfile
import time
import unittest
from types import SimpleNamespace

import gofi
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from google.protobuf.struct_pb2 import Struct
from gofi.execution import StreamSession, source_frames
from gofi.server import Worker
from gofi.v1.sandbox_pb2 import WorkerListFunctionsRequest, WorkerSourceRequest

SOURCE = '''import json, os, time
import gofi

@gofi.fetcher(every="10s")
def items(ctx, limit: int = 2):
    """Poll a counter."""
    start = ctx.state.get("next", 0)
    ctx.state["next"] = start + limit
    print("polling from", start, "for", ctx.flow)
    return [{"n": start + i} for i in range(limit)]

@gofi.fetcher
def mixed():
    return [b"raw", "text", {"a": 1}, gofi.Frame({"b": 2}, meta={"feed": "x", "n": 3})]

@gofi.fetcher("nothing", every=5)
def nothing(ctx):
    return None

@gofi.fetcher("broken")
def broken():
    return {"x": undefined_name}

@gofi.fetcher("bad_type")
def bad_type():
    return 42

@gofi.fetcher("slow")
def slow():
    time.sleep(5)

@gofi.streamer
def count(ctx, limit: int = 3):
    for i in range(ctx.state.get("from", 0), limit):
        ctx.state["from"] = i + 1
        yield {"i": i}

@gofi.streamer("forever")
def forever(ctx, path: str = ""):
    n = 0
    try:
        while True:
            n += 1
            with open(path + ".count", "w") as f:
                f.write(str(n))
            yield gofi.Frame(str(n), meta={"n": n})
    finally:
        with open(path + ".closed", "w") as f:
            f.write("closed")

@gofi.streamer("asyncgen")
async def asyncgen(ctx):
    for i in range(2):
        await ctx.sleep(0)
        yield [b"a%d" % i, b"b%d" % i]

@gofi.streamer("fails")
def fails():
    yield b"one"
    raise RuntimeError("feed went away")

@gofi.streamer("idle")
def idle(ctx, path: str = ""):
    try:
        while not ctx.wait(30):
            yield None
    finally:
        with open(path + ".closed", "w") as f:
            f.write("closed")

@gofi.transformer("noop")
def noop(fields):
    return fields
'''


def struct(value):
    holder = Struct()
    holder.update(value)
    return holder


def ctx():
    return SimpleNamespace(request_headers={"authorization": "Bearer test-secret"})


async def take(stream, n):
    out = []
    async for message in stream:
        out.append(message)
        if len(out) == n:
            break
    return out


class DecoratorTest(unittest.TestCase):
    def setUp(self):
        gofi._load(SOURCE)

    def test_registry_reports_kinds_and_every(self):
        found = {(f.kind, f.name): f for f in gofi.functions()}
        self.assertEqual(found["fetcher", "items"].every, "10s")
        self.assertEqual(found["fetcher", "nothing"].every, "5s")
        self.assertEqual(found["fetcher", "mixed"].every, "")
        self.assertIn(("streamer", "count"), found)
        # ctx is data, not configuration
        self.assertEqual([p.name for p in found["fetcher", "items"].params], ["limit"])
        self.assertEqual([p.name for p in found["streamer", "count"].params], ["limit"])

    def test_every_is_validated(self):
        for bad in ("soon", "0.5s", 0, "10"):
            with self.assertRaises(ValueError, msg=bad):
                gofi.fetcher(every=bad)
        self.assertEqual(gofi.duration_seconds("1m30s"), 90)
        self.assertEqual(gofi.duration_seconds("500ms"), 0.5)

    def test_streamer_must_be_a_generator(self):
        with self.assertRaises(TypeError):
            gofi.streamer("plain")(lambda ctx: [b"x"])

    def test_source_frames_normalize(self):
        frames = source_frames([b"raw", "text", {"a": 1}, gofi.Frame(b"x", meta={"k": 1})])
        self.assertEqual([f["meta"] for f in frames], [{}, {}, {}, {"k": "1"}])
        self.assertEqual(source_frames(None), [])
        with self.assertRaises(TypeError):
            source_frames([[b"nested"]])

    def test_decorators_are_described(self):
        names = [d.__name__ for d in gofi.DECORATORS]
        self.assertEqual(names[:2], ["fetcher", "streamer"])


class FetchTest(unittest.TestCase):
    def setUp(self):
        self.worker = Worker(SOURCE, "test-secret", 1)

    def tearDown(self):
        self.worker.executor.stop()

    def fetch(self, name, **kw):
        return asyncio.run(self.worker.fetch(WorkerSourceRequest(name=name, **kw), ctx()))

    def test_list_functions_reports_every(self):
        functions = asyncio.run(self.worker.list_functions(WorkerListFunctionsRequest(), ctx())).functions
        every = {f.name: f.every for f in functions if f.kind == "fetcher"}
        self.assertEqual(every["items"], "10s")

    def test_fetch_returns_frames_and_state(self):
        first = self.fetch("items", flow="radar", config=struct({"limit": 3}))
        self.assertEqual(first.error, "")
        self.assertEqual([json.loads(f.data) for f in first.frames], [{"n": 0}, {"n": 1}, {"n": 2}])
        self.assertIn("polling from 0 for radar", first.stdout)
        second = self.fetch("items", flow="radar", config=struct({"limit": 3}), state=first.state)
        # The cursor stays an int across the Struct round trip.
        self.assertEqual([json.loads(f.data) for f in second.frames], [{"n": 3}, {"n": 4}, {"n": 5}])
        self.assertEqual(second.state["next"], 6)

    def test_fetch_shapes(self):
        res = self.fetch("mixed")
        self.assertEqual([f.data for f in res.frames], [b"raw", b"text", b'{"a":1}', b'{"b":2}'])
        self.assertEqual(dict(res.frames[3].meta), {"feed": "x", "n": "3"})
        self.assertEqual(len(self.fetch("nothing").frames), 0)

    def test_user_errors_are_reported(self):
        res = self.fetch("broken")
        self.assertIn("NameError", res.error)
        self.assertGreater(res.error_location.line, 0)
        self.assertIn("must be bytes, str, a dict", self.fetch("bad_type").error)

    def test_deadline(self):
        with self.assertRaises(ConnectError) as raised:
            self.fetch("slow")
        self.assertEqual(raised.exception.code, Code.DEADLINE_EXCEEDED)

    def test_wrong_kind_and_auth(self):
        with self.assertRaises(ConnectError) as raised:
            self.fetch("count")
        self.assertEqual(raised.exception.code, Code.NOT_FOUND)
        with self.assertRaises(ConnectError) as raised:
            asyncio.run(self.worker.fetch(WorkerSourceRequest(name="items"), SimpleNamespace(request_headers={})))
        self.assertEqual(raised.exception.code, Code.UNAUTHENTICATED)


class StreamTest(unittest.TestCase):
    def setUp(self):
        self.worker = Worker(SOURCE, "test-secret", 1)
        self.dir = tempfile.mkdtemp()

    def tearDown(self):
        self.worker.executor.stop()

    def wait_for(self, path, timeout=10):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if os.path.exists(path):
                return True
            time.sleep(0.05)
        return False

    def test_stream_yields_frames_state_then_ends(self):
        async def run():
            return [m async for m in self.worker.stream(WorkerSourceRequest(name="count", flow="f", state=struct({"from": 1})), ctx())]
        messages = asyncio.run(run())
        self.assertTrue(messages[0].started)
        self.assertEqual([json.loads(m.frames[0].data) for m in messages[1:]], [{"i": 1}, {"i": 2}])
        self.assertEqual(messages[-1].state["from"], 3)
        self.assertEqual(self.worker.streams, 0)

    def test_async_streamer_lists(self):
        async def run():
            return [m async for m in self.worker.stream(WorkerSourceRequest(name="asyncgen"), ctx())]
        messages = asyncio.run(run())
        self.assertEqual([[f.data for f in m.frames] for m in messages[1:]], [[b"a0", b"b0"], [b"a1", b"b1"]])

    def test_streamer_failure_ends_the_stream_with_an_error(self):
        async def run():
            got = []
            async for m in self.worker.stream(WorkerSourceRequest(name="fails"), ctx()):
                got.append(m)
            return got
        with self.assertRaises(ConnectError) as raised:
            asyncio.run(run())
        self.assertIn("feed went away", raised.exception.message)

    def test_backpressure_is_bounded_and_cancel_closes_generator(self):
        path = os.path.join(self.dir, "forever")

        async def run():
            stream = self.worker.stream(WorkerSourceRequest(name="forever", config=struct({"path": path})), ctx())
            messages = await take(stream, 3)  # started + 2 frames
            await asyncio.sleep(1.0)          # a slow consumer
            with open(path + ".count") as f:
                produced = int(f.read())
            await stream.aclose()             # the caller goes away
            return messages, produced
        messages, produced = asyncio.run(run())
        self.assertEqual([m.frames[0].data for m in messages[1:]], [b"1", b"2"])
        self.assertEqual(dict(messages[1].frames[0].meta), {"n": "1"})
        # At most the window ahead of what was taken (plus the item in hand).
        self.assertLessEqual(produced, 2 + StreamSession.WINDOW + 1)
        self.assertTrue(self.wait_for(path + ".closed"), "generator finally did not run")
        self.assertEqual(self.worker.streams, 0)

    def test_cooperative_stop_while_idle(self):
        path = os.path.join(self.dir, "idle")

        async def run():
            stream = self.worker.stream(WorkerSourceRequest(name="idle", config=struct({"path": path})), ctx())
            await take(stream, 1)
            await asyncio.sleep(0.5)
            await stream.aclose()
        started = time.monotonic()
        asyncio.run(run())
        # ctx.wait(30) returns at once on stop, so the finally runs well
        # before the 2 s kill grace.
        self.assertTrue(self.wait_for(path + ".closed", timeout=5))
        self.assertLess(time.monotonic() - started, 8)

    def test_stream_limits(self):
        async def run(name):
            return [m async for m in self.worker.stream(WorkerSourceRequest(name=name), ctx())]
        with self.assertRaises(ConnectError) as raised:
            asyncio.run(run("items"))
        self.assertEqual(raised.exception.code, Code.NOT_FOUND)
        self.worker.streams = 4
        with self.assertRaises(ConnectError) as raised:
            asyncio.run(run("count"))
        self.assertEqual(raised.exception.code, Code.RESOURCE_EXHAUSTED)


if __name__ == "__main__":
    unittest.main()
