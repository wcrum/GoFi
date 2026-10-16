import asyncio
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from gofi.server import Worker
from gofi.v1.sandbox_pb2 import WorkerFormatSourceRequest, WorkerValidateSourceRequest

STUB = 'import gofi\n@gofi.parser("stub")\ndef stub(raw): return {}\n'

class ToolingTest(unittest.TestCase):
    def setUp(self):
        self.worker = Worker(STUB, "test-secret", 5)
        self.ctx = SimpleNamespace(request_headers={"authorization": "Bearer test-secret"})

    def test_formats_source_without_executing_it(self):
        with tempfile.TemporaryDirectory() as directory:
            sentinel = Path(directory) / "must-not-exist"
            source = f'import gofi\nopen({str(sentinel)!r}, "w").write("unsafe")\n@gofi.parser("x")\ndef x(raw):return {{"value":1}}\n'
            response = asyncio.run(self.worker.format_source(WorkerFormatSourceRequest(source=source), self.ctx))
            self.assertIn('    return {"value": 1}', response.source)
            self.assertFalse(sentinel.exists())

    def test_validation_reports_syntax_and_undefined_names(self):
        invalid = asyncio.run(self.worker.validate_source(WorkerValidateSourceRequest(source="def broken(:\n"), self.ctx))
        self.assertFalse(invalid.valid)
        self.assertEqual(invalid.diagnostics[0].code, "SYNTAX")
        self.assertGreater(invalid.diagnostics[0].line, 0)
        invalid = asyncio.run(self.worker.validate_source(WorkerValidateSourceRequest(source='import gofi\n@gofi.parser("x")\ndef x(raw): return missing_name\n'), self.ctx))
        self.assertFalse(invalid.valid)
        self.assertTrue(any(item.code == "F821" for item in invalid.diagnostics))

    def test_validates_alias_decorators_without_executing_source(self):
        with tempfile.TemporaryDirectory() as directory:
            sentinel = Path(directory) / "must-not-exist"
            source = f'from gofi import parser as decode\nopen({str(sentinel)!r}, "w").write("unsafe")\n@decode("x")\ndef x(raw): return {{"value": 1}}\n'
            result = asyncio.run(self.worker.validate_source(WorkerValidateSourceRequest(source=source), self.ctx))
            self.assertTrue(result.valid, result.diagnostics)
            self.assertFalse(sentinel.exists())

    def test_accepts_generic_python_and_bounds_source(self):
        result = asyncio.run(self.worker.validate_source(WorkerValidateSourceRequest(source="x = 1\n"), self.ctx))
        self.assertTrue(result.valid)
        from connectrpc.errors import ConnectError
        with self.assertRaises(ConnectError):
            asyncio.run(self.worker.format_source(WorkerFormatSourceRequest(source="x" * 65537), self.ctx))

if __name__ == "__main__":
    unittest.main()
