"""gofi.text tokenizers and the decorators' model= / on_invalid= / format=."""
import unittest

import gofi
from pydantic import BaseModel, ConfigDict, Field


class Fields(BaseModel):
    model_config = ConfigDict(extra="allow", populate_by_name=True)

    host: str
    temperature: float | None = None
    ok: bool | None = None
    src_ip: str | None = Field(default=None, alias="src-ip")


def _function(kind, name):
    functions = gofi.functions()
    entries = functions.values() if isinstance(functions, dict) else functions
    return next(entry for entry in entries if entry.kind == kind and entry.name == name)


class TextHelpersTest(unittest.TestCase):
    def test_keyvalue_tolerates_newlines_commas_and_quotes(self):
        pairs = gofi.text.keyvalue(b'host=a1 temperature=31.5\nok=true, src-ip="10.0.0.1" msg=\'disk full\' bare=x,')
        self.assertEqual(pairs, {"host": "a1", "temperature": "31.5", "ok": "true", "src-ip": "10.0.0.1", "msg": "disk full", "bare": "x"})
        self.assertEqual(gofi.text.keyvalue(b"nothing here"), {})
        self.assertEqual(gofi.text.keyvalue(b"a=1 a=2")["a"], "2")

    def test_top_level_names_are_the_same_helpers(self):
        self.assertEqual(gofi.KeyValue(b"a=1 b=x"), {"a": "1", "b": "x"})
        self.assertEqual(gofi.JSON(b'{"a": 1}'), {"a": 1})
        self.assertEqual(gofi.CSV(b"1,2", ["x", "y"]), {"x": "1", "y": "2"})
        self.assertEqual(gofi.Delimited(b"1|2", "|"), {"f0": "1", "f1": "2"})

    def test_json_csv_and_delimited(self):
        self.assertEqual(gofi.text.json(b'{"a": 1, "b": [2]}'), {"a": 1, "b": [2]})
        with self.assertRaises(ValueError):
            gofi.text.json(b"[1, 2]")
        with self.assertRaises(ValueError):
            gofi.text.json(b"not json")
        self.assertEqual(gofi.text.csv(b"2026,host-1,31.5\n", ["time", "host", "value"]), {"time": "2026", "host": "host-1", "value": "31.5"})
        self.assertEqual(gofi.text.csv(b"a;b", ["x", "y", "z"], delimiter=";"), {"x": "a", "y": "b"})
        self.assertEqual(gofi.text.delimited(b"a|b|c", "|"), {"f0": "a", "f1": "b", "f2": "c"})
        self.assertEqual(gofi.text.delimited(b"a\tb", "\t", ["left", "right"]), {"left": "a", "right": "b"})


class ModelDecoratorTest(unittest.TestCase):
    def test_model_types_the_mapping_and_flags_invalid_frames(self):
        @gofi.parser("text_test_kv", model=Fields)
        def kv(data: bytes) -> dict | None:
            """Pairs in, typed fields out."""
            return gofi.KeyValue(data)

        entry = _function("parser", "text_test_kv")
        self.assertEqual(entry.callback(b'host=a1 temperature=31.5 ok=true src-ip="10.0.0.1" note=x'), {"host": "a1", "temperature": 31.5, "ok": True, "src_ip": "10.0.0.1", "note": "x"})
        flagged = entry.callback(b"host=a1 temperature=hot")
        self.assertEqual(flagged["host"], "a1")
        self.assertIn("temperature", flagged["_invalid"])
        self.assertIsNone(entry.callback(b"no pairs at all"), "an empty mapping stays None (rejected frame)")
        self.assertEqual(entry.description, "Pairs in, typed fields out.")
        self.assertEqual(entry.params, (), "the data argument is not configuration")

    def test_on_invalid_drop_and_raise(self):
        @gofi.parser("text_test_drop", model=Fields, on_invalid="drop")
        def drop(data: bytes) -> dict | None:
            return gofi.KeyValue(data)

        @gofi.parser("text_test_raise", model=Fields, on_invalid="raise")
        def raise_(data: bytes) -> dict | None:
            return gofi.KeyValue(data)

        self.assertIsNone(_function("parser", "text_test_drop").callback(b"temperature=hot"))
        with self.assertRaises(Exception) as caught:
            _function("parser", "text_test_raise").callback(b"temperature=hot")
        self.assertEqual(type(caught.exception).__name__, "ValidationError")
        with self.assertRaises(ValueError):
            gofi.parser("text_test_bad", model=Fields, on_invalid="ignore")(lambda data: {})

    def test_format_registers_a_default_body(self):
        gofi.parser("text_test_format", format="keyvalue", model=Fields)
        entry = _function("parser", "text_test_format")
        self.assertEqual(entry.callback(b"host=a2 temperature=20"), {"host": "a2", "temperature": 20.0, "ok": None, "src_ip": None})
        self.assertIsNone(entry.callback(b"nothing"))
        gofi.parser("text_test_csv", format="csv", columns=["time", "host"])
        self.assertEqual(_function("parser", "text_test_csv").callback(b"1,h"), {"time": "1", "host": "h"})
        with self.assertRaises(ValueError):
            gofi.parser("text_test_nope", format="yaml")
        with self.assertRaises(ValueError):
            gofi.parser(format="keyvalue")

    def test_transformer_model(self):
        @gofi.transformer("text_test_transform", model=Fields)
        def transform(fields: dict, meta: dict) -> dict | None:
            fields["temperature"] = str(float(fields["temperature"]) + 1)
            return fields

        self.assertEqual(_function("transformer", "text_test_transform").callback({"host": "a", "temperature": "1"}, {}), {"host": "a", "temperature": 2.0, "ok": None, "src_ip": None})


if __name__ == "__main__":
    unittest.main()
