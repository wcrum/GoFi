"""gofi.llm: the deployment's AI endpoint as a library sees it. No model is
contacted: clients are stubbed, and the one real client call goes to a
closed local port."""
import os
import sys
import unittest
from types import SimpleNamespace
from unittest import mock

import gofi
from pydantic import BaseModel

# What GoFi supplies to a worker whose library has AI on. The proxy port is
# closed, so a real client call fails fast with a connection error.
AI_ENV = {"GOFI_LLM_PROXY_URL": "http://127.0.0.1:9/gofi/llm", "GOFI_LLM_PROVIDER": "openai", "GOFI_LLM_MODEL": "test-model", "GOFI_SANDBOX_TOKEN": "worker-token-0123"}
NO_AI_ENV = {"GOFI_LLM_PROXY_URL": "", "GOFI_LLM_PROVIDER": "", "GOFI_LLM_MODEL": "", "GOFI_SANDBOX_TOKEN": "worker-token-0123"}


class StubRunnable:
    def __init__(self, result):
        self.result, self.calls = result, []

    def invoke(self, messages):
        self.calls.append(messages)
        value = self.result(messages) if callable(self.result) else self.result
        if isinstance(value, Exception):
            raise value
        return value


class StubModel(StubRunnable):
    """A chat model whose structured output is built per schema."""

    def __init__(self, result=None, structured=None):
        super().__init__(result)
        self.structured, self.schemas = structured, []

    def with_structured_output(self, schema):
        self.schemas.append(schema)
        # Built at invoke time, as the real runnable validates the model's answer then.
        return StubRunnable((lambda messages: self.structured(schema)) if callable(self.structured) else self.structured)


class LLMTest(unittest.TestCase):
    def setUp(self):
        self.llm = gofi.llm
        self.llm._models.clear()
        self.llm._runnables.clear()

    def test_gofi_llm_loads_lazily_and_reports_availability(self):
        self.assertIs(sys.modules["gofi.llm"], gofi.llm)
        with mock.patch.dict(os.environ, NO_AI_ENV):
            self.assertFalse(self.llm.available())
            with self.assertRaises(self.llm.LLMError) as caught:
                self.llm.chat("hi")
            self.assertEqual(str(caught.exception), "the deployment's AI endpoint is not enabled for functions; an administrator turns it on under Administration › AI assistant")
            with self.assertRaises(self.llm.LLMError):
                self.llm.model()
        with mock.patch.dict(os.environ, AI_ENV):
            self.assertTrue(self.llm.available())

    def test_model_points_at_the_proxy_with_the_worker_token_and_is_cached(self):
        with mock.patch.dict(os.environ, AI_ENV):
            client = self.llm.model(timeout=3.5)
            self.assertEqual(type(client).__name__, "ChatOpenAI")
            self.assertEqual(client.openai_api_base, "http://127.0.0.1:9/gofi/llm/v1")
            self.assertEqual(client.openai_api_key.get_secret_value(), "worker-token-0123")
            self.assertEqual((client.request_timeout, client.max_retries, client.model_name), (3.5, 0, "test-model"))
            self.assertIs(self.llm.model(timeout=3.5), client)
            self.assertIsNot(self.llm.model(timeout=4.0), client)
        with mock.patch.dict(os.environ, {**AI_ENV, "GOFI_LLM_PROVIDER": "anthropic"}):
            client = self.llm.model()
            self.assertEqual(type(client).__name__, "ChatAnthropic")
            self.assertEqual(client.anthropic_api_url, "http://127.0.0.1:9/gofi/llm")
            self.assertEqual(client.anthropic_api_key.get_secret_value(), "worker-token-0123")
        with mock.patch.dict(os.environ, {**AI_ENV, "GOFI_LLM_PROVIDER": "other"}):
            with self.assertRaises(self.llm.LLMError):
                self.llm.model()

    def test_a_failed_call_is_an_llm_error_without_the_token(self):
        with mock.patch.dict(os.environ, AI_ENV):
            with self.assertRaises(self.llm.LLMError) as caught:
                self.llm.chat("hi", timeout=0.5)
        self.assertIn("ConnectionError", str(caught.exception))
        self.assertNotIn("worker-token", str(caught.exception))

    def test_chat_sends_system_and_prompt_and_strips_the_answer(self):
        stub = StubModel(SimpleNamespace(text="  The answer. ", content="x"))
        with mock.patch.dict(os.environ, AI_ENV), mock.patch.object(self.llm, "model", return_value=stub):
            self.assertEqual(self.llm.chat("Question?", system="Be brief."), "The answer.")
        messages = stub.calls[0]
        self.assertEqual([type(m).__name__ for m in messages], ["SystemMessage", "HumanMessage"])
        self.assertEqual((messages[0].content, messages[1].content), ("Be brief.", "Question?"))
        with mock.patch.dict(os.environ, AI_ENV), mock.patch.object(self.llm, "model", return_value=StubModel(SimpleNamespace(content="plain"))):
            self.assertEqual(self.llm.chat("q"), "plain")

    def test_classify_constrains_the_answer_to_the_labels(self):
        stub = StubModel(structured=lambda schema: schema(label="critical"))
        with mock.patch.dict(os.environ, AI_ENV), mock.patch.object(self.llm, "model", return_value=stub):
            label = self.llm.classify({"b": 1, "a": "x"}, ["normal", "critical"], instructions="Severity.")
            self.assertEqual(label, "critical")
            self.assertEqual(stub.schemas[0].model_json_schema()["properties"]["label"]["enum"], ["normal", "critical"])
            self.assertEqual(self.llm.classify(b"bytes", ["normal", "critical"]), "critical")
            self.assertEqual(len(stub.schemas), 1, "the structured runnable is cached per label set")
            self.llm.classify("text", ["a", "b", "critical"])
            self.assertEqual(len(stub.schemas), 2)
            with self.assertRaises(self.llm.LLMError):
                self.llm.classify("text", [])
            with self.assertRaises(self.llm.LLMError):  # the stub answers "critical", not allowed here
                self.llm.classify({"x": 1}, ["normal"])
        first = self.llm._runnables[(id(stub), ("classify", ("normal", "critical")))].calls[0]
        self.assertEqual(first[0].content, "Severity. Classify the input. Answer with exactly one of these labels: normal, critical.")
        self.assertEqual(first[1].content, '{"a":"x","b":1}')
        bad = StubModel(structured=lambda schema: SimpleNamespace(label="bogus"))
        with mock.patch.dict(os.environ, AI_ENV), mock.patch.object(self.llm, "model", return_value=bad):
            with self.assertRaises(self.llm.LLMError) as caught:
                self.llm.classify("text", ["normal", "critical"])
            self.assertIn("bogus", str(caught.exception))

    def test_extract_accepts_a_type_dict_or_a_pydantic_model(self):
        stub = StubModel(structured=lambda schema: schema(vendor="ACME", amount=12.5))
        with mock.patch.dict(os.environ, AI_ENV), mock.patch.object(self.llm, "model", return_value=stub):
            self.assertEqual(self.llm.extract("ACME charged 12.50", {"vendor": str, "amount": float, "currency": str}), {"vendor": "ACME", "amount": 12.5, "currency": None})
            self.assertEqual(self.llm.extract({"text": "again"}, {"vendor": str, "amount": float, "currency": str}), {"vendor": "ACME", "amount": 12.5, "currency": None})
            self.assertEqual(len(stub.schemas), 1, "the same type dict reuses its model")

            class Purchase(BaseModel):
                vendor: str
                amount: float = 0

            self.assertEqual(self.llm.extract("x", Purchase, instructions="Purchases."), {"vendor": "ACME", "amount": 12.5})
            self.assertEqual(stub.schemas[-1], Purchase)
            for bad in ({}, {"vendor": "str"}, "vendor", 3):
                with self.assertRaises(self.llm.LLMError):
                    self.llm.extract("x", bad)
        system = self.llm._runnables[(id(stub), ("extract", Purchase))].calls[0][0].content
        self.assertEqual(system, "Purchases.")

    def test_a_503_from_a_standby_is_retried_within_the_deadline(self):
        class Standby(Exception):
            status_code = 503
            response = SimpleNamespace(headers={"retry-after": "0.01"})

        class Broken(Exception):
            status_code = 500
            response = SimpleNamespace(headers={})

        answers = iter([Standby("standby"), Standby("standby"), SimpleNamespace(text="leader")])
        stub = StubModel(lambda messages: next(answers))
        with mock.patch.dict(os.environ, AI_ENV), mock.patch.object(self.llm, "model", return_value=stub):
            self.assertEqual(self.llm.chat("q"), "leader")
            self.assertEqual(len(stub.calls), 3)
            stub = StubModel(Standby("standby"))
            with mock.patch.object(self.llm, "model", return_value=stub), self.assertRaises(self.llm.LLMError):
                self.llm.chat("q")
            self.assertEqual(len(stub.calls), 4, "three retries, then the error")
            stub = StubModel(Broken("boom"))
            with mock.patch.object(self.llm, "model", return_value=stub), self.assertRaises(self.llm.LLMError):
                self.llm.chat("q")
            self.assertEqual(len(stub.calls), 1, "only a 503 is retried")
            stub = StubModel(Standby("standby"))
            with mock.patch.object(self.llm, "model", return_value=stub), self.assertRaises(self.llm.LLMError):
                self.llm.chat("q", timeout=0.05)
            self.assertEqual(len(stub.calls), 1, "no retry that would overrun the deadline")
        self.assertEqual(self.llm._retry_after(Standby("x")), 0.1, "waits are bounded below")
        self.assertIsNone(self.llm._retry_after(Broken("x")))

    def test_extract_without_a_schema_parses_the_json_object_answer(self):
        answers = iter(['Sure:\n```json\n{"host": "db1", "port": 5432}\n```', '["not", "an", "object"]', "no json here", '{"a": '])
        stub = StubModel(lambda messages: SimpleNamespace(text=next(answers)))
        with mock.patch.dict(os.environ, AI_ENV), mock.patch.object(self.llm, "model", return_value=stub):
            self.assertEqual(self.llm.extract("Extract host and port: db1:5432"), {"host": "db1", "port": 5432})
            self.assertIn("JSON object only", stub.calls[0][0].content)
            for _ in range(3):
                with self.assertRaises(self.llm.LLMError):
                    self.llm.extract("x")


if __name__ == "__main__":
    unittest.main()
