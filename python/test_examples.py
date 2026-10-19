"""The shipped example libraries (examples/*.py) load and register what the
docs claim, and the AI example fails as documented without the endpoint.
No model is contacted: the AI environment points at a closed local port."""
import os
from pathlib import Path
import unittest
from unittest import mock

import gofi
from gofi.tooling import validate_source

EXAMPLES = Path(__file__).resolve().parent.parent / "examples"
AI_ENV = {"GOFI_LLM_PROXY_URL": "http://127.0.0.1:9/gofi/llm", "GOFI_LLM_PROVIDER": "openai", "GOFI_LLM_MODEL": "test-model", "GOFI_SANDBOX_TOKEN": "worker-token"}
NO_AI_ENV = {key: "" for key in AI_ENV}


def load(name):
    source = (EXAMPLES / name).read_text()
    gofi._load(source)
    return source, {(f.kind, f.name): f for f in gofi.functions()}


class ExampleLibrariesTest(unittest.TestCase):
    def test_library_example_validates_and_registers_one_function_per_step(self):
        source, functions = load("python-library.py")
        self.assertEqual(validate_source(source), [])
        self.assertEqual({kind for kind, _ in functions}, {"framer", "decoder", "parser", "transformer", "encoder", "sink"})

    def test_ai_example_registers_the_four_steps_with_wizard_parameters(self):
        source, functions = load("python-ai.py")
        self.assertEqual(validate_source(source), [])
        self.assertEqual(set(functions), {("transformer", "ai_classify"), ("transformer", "ai_enrich"), ("transformer", "ai_extract"), ("sink", "ai_notify")})
        params = {name: {p.name: p for p in f.params} for (_, name), f in functions.items()}
        self.assertEqual((params["ai_classify"]["labels"].type, params["ai_classify"]["labels"].default), ("list", ["normal", "suspicious", "critical"]))
        self.assertEqual(params["ai_classify"]["drop"].default, ["normal"])
        self.assertIn("{fields}", params["ai_enrich"]["prompt"].default)
        self.assertEqual(params["ai_enrich"]["output_key"].default, "summary")
        self.assertIn("{fields}", params["ai_extract"]["prompt"].default)
        self.assertEqual(params["ai_extract"]["output_key"].default, "extracted")
        self.assertIn("{fields}", params["ai_notify"]["prompt"].default)
        for name, kinds in params.items():
            for forbidden in ("api_key", "base_url", "provider", "model"):
                self.assertNotIn(forbidden, kinds, f"{name} must not take {forbidden}: GoFi supplies the endpoint")
        for forbidden in ("api_key", "langchain_", "https://"):
            self.assertNotIn(forbidden, source)

    def test_ai_example_raises_llm_error_without_ai_or_without_the_endpoint(self):
        _, functions = load("python-ai.py")
        enrich = functions["transformer", "ai_enrich"].callback
        with mock.patch.dict(os.environ, NO_AI_ENV):
            with self.assertRaises(gofi.llm.LLMError) as caught:
                enrich({"sensor": "a"}, {})
            self.assertIn("not enabled for functions", str(caught.exception))
        gofi.llm._models.clear()
        with mock.patch.dict(os.environ, AI_ENV):
            for call in (
                lambda: enrich({"sensor": "a"}, {}),
                lambda: functions["transformer", "ai_classify"].callback({"sensor": "a"}, {}),
                lambda: functions["transformer", "ai_extract"].callback({"text": "ACME 12.50"}, {}),
                lambda: functions["sink", "ai_notify"].callback({"sensor": "a"}, {"name": "flow"}),
            ):
                with self.assertRaises(gofi.llm.LLMError) as caught:
                    call()
                self.assertIn("ConnectionError", str(caught.exception))
                self.assertNotIn("worker-token", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
