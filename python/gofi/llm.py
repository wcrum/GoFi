"""gofi.llm: the deployment's AI endpoint, for libraries that turned AI on.

An operator configures one model endpoint under Administration (the same
configuration GoFiBot uses). A library with AI on calls it through the
controller's LLM proxy with the worker's own token, so the model key never
enters the worker. GoFi supplies GOFI_LLM_PROXY_URL, GOFI_LLM_PROVIDER, and
GOFI_LLM_MODEL; the worker's GOFI_SANDBOX_TOKEN authenticates each call.

    label = gofi.llm.classify(fields, ["normal", "suspicious", "critical"])
    summary = gofi.llm.chat("Summarize in one sentence: " + text)
    fields.update(gofi.llm.extract(text, {"vendor": str, "amount": float}))
    chain = gofi.llm.model().with_structured_output(MyModel)   # LangChain

Every helper raises LLMError when AI is off for this library or a call
fails; a transformer that lets it propagate fails closed for that message.
"""
import json
import os
import threading
import time

NOT_ALLOWED = "the deployment's AI endpoint is not enabled for functions; an administrator turns it on under Administration › AI assistant"

_ENV_PROXY, _ENV_PROVIDER, _ENV_MODEL, _ENV_TOKEN = "GOFI_LLM_PROXY_URL", "GOFI_LLM_PROVIDER", "GOFI_LLM_MODEL", "GOFI_SANDBOX_TOKEN"


class LLMError(Exception):
    """AI is unavailable to this library, or a model call failed."""


_lock = threading.Lock()
_models = {}
_runnables = {}


def _settings():
    proxy = os.environ.get(_ENV_PROXY, "").rstrip("/")
    provider = os.environ.get(_ENV_PROVIDER, "")
    token = os.environ.get(_ENV_TOKEN, "")
    if not proxy or not provider or not token:
        raise LLMError(NOT_ALLOWED)
    return proxy, provider, os.environ.get(_ENV_MODEL, ""), token


def available() -> bool:
    """True when this library may use the deployment's AI endpoint."""
    try:
        _settings()
    except LLMError:
        return False
    return True


def model(*, timeout: float = 20.0):
    """The LangChain chat model behind the helpers (a BaseChatModel), for
    prompts, tools, and chains they do not cover: ``invoke``,
    ``with_structured_output``, ``bind_tools``. One client per process; every
    call goes through the controller's proxy. ``timeout`` is per call, in
    seconds; keep it below the library's timeoutSeconds."""
    proxy, provider, name, token = _settings()
    key = (proxy, provider, name, float(timeout))
    with _lock:
        client = _models.get(key)
        if client is None:
            try:
                if provider == "anthropic":
                    from langchain_anthropic import ChatAnthropic

                    client = ChatAnthropic(model=name, api_key=token, base_url=proxy, timeout=timeout, max_retries=0)
                elif provider == "openai":
                    from langchain_openai import ChatOpenAI

                    client = ChatOpenAI(model=name, api_key=token, base_url=proxy + "/v1", timeout=timeout, max_retries=0)
                else:
                    raise LLMError(f"unsupported AI provider {provider!r}; expected anthropic or openai")
            except ImportError as error:
                raise LLMError("LangChain is not installed in this worker image") from error
            _models[key] = client
    return client


def _structured(client, schema, cache_key):
    """A cached structured-output runnable of client for schema."""
    key = (id(client), cache_key)
    with _lock:
        runnable = _runnables.get(key)
        if runnable is None:
            runnable = client.with_structured_output(schema)
            _runnables[key] = runnable
    return runnable


def _text(data) -> str:
    """The input as text: bytes decoded, a dict or list as compact JSON."""
    if isinstance(data, bytes):
        return data.decode("utf-8", errors="replace")
    if isinstance(data, str):
        return data
    return json.dumps(data, separators=(",", ":"), sort_keys=True, default=str)


# A standby controller replica answers 503 with Retry-After behind the
# shared Service; such a call is retried a few times within the deadline.
_RETRIES, _MIN_WAIT, _MAX_WAIT = 3, 0.1, 5.0


def _retry_after(error):
    """Seconds to wait before retrying a 503 from the proxy, else None."""
    if getattr(error, "status_code", None) != 503:
        return None
    headers = getattr(getattr(error, "response", None), "headers", None) or {}
    try:
        wait = float(headers.get("retry-after", 1.0))
    except (TypeError, ValueError):
        wait = 1.0
    return min(max(wait, _MIN_WAIT), _MAX_WAIT)


def _invoke(runnable, messages, timeout=20.0):
    started, attempt = time.monotonic(), 0
    while True:
        try:
            return runnable.invoke(messages)
        except LLMError:
            raise
        except Exception as error:  # the SDKs' errors; never includes the token
            wait = _retry_after(error)
            if wait is None or attempt >= _RETRIES or time.monotonic() - started + wait > timeout:
                raise LLMError(f"{type(error).__name__}: {error}"[:300]) from error
            attempt += 1
            time.sleep(wait)


def _messages(system, user):
    from langchain_core.messages import HumanMessage, SystemMessage

    return ([SystemMessage(system)] if system else []) + [HumanMessage(user)]


def chat(prompt: str, *, system: str | None = None, timeout: float = 20.0) -> str:
    """Send one prompt (with an optional system prompt) and return the
    model's text answer, stripped."""
    reply = _invoke(model(timeout=timeout), _messages(system, str(prompt)), timeout)
    text = reply.text if isinstance(getattr(reply, "text", None), str) else str(getattr(reply, "content", reply))
    return text.strip()


def classify(data, labels: list[str], *, instructions: str = "", timeout: float = 20.0) -> str:
    """Return exactly one of ``labels`` for ``data`` (a dict, str, or bytes),
    using structured output so the model cannot answer anything else."""
    allowed = tuple(str(label) for label in labels)
    if not allowed:
        raise LLMError("labels must name at least one label")
    from typing import Literal

    from pydantic import create_model

    verdict = create_model("Verdict", label=(Literal[allowed], ...))
    runnable = _structured(model(timeout=timeout), verdict, ("classify", allowed))
    system = f"{instructions.strip()} " if instructions.strip() else ""
    system += "Classify the input. Answer with exactly one of these labels: " + ", ".join(allowed) + "."
    result = _invoke(runnable, _messages(system, _text(data)), timeout)
    label = result.get("label") if isinstance(result, dict) else getattr(result, "label", None)
    if label not in allowed:
        raise LLMError(f"the model answered {label!r}, not one of the labels")
    return label


def _json_object(text: str) -> dict:
    """The JSON object in a model's answer (code fences and prose around it
    are tolerated); anything else is an LLMError."""
    start, end = text.find("{"), text.rfind("}")
    if start < 0 or end < start:
        raise LLMError("the model did not answer with a JSON object")
    try:
        value = json.loads(text[start : end + 1])
    except ValueError as error:
        raise LLMError(f"the model's answer is not valid JSON: {error}") from error
    if not isinstance(value, dict):
        raise LLMError("the model did not answer with a JSON object")
    return value


def extract(data, schema=None, *, instructions: str = "", timeout: float = 20.0) -> dict:
    """Pull structured values out of ``data`` (a dict, str, or bytes) and
    return them as a dict.

    Without ``schema`` the model answers a JSON object of whatever the
    prompt asks for (say which keys you want in ``data`` or
    ``instructions``). With ``schema``, a pydantic model class or a dict of
    field names to types (``{"vendor": str, "amount": float}``), structured
    output restricts the answer to those fields; a value the input does not
    state comes back as None."""
    if schema is None:
        system = instructions.strip() or "Answer with a JSON object only: the requested values as keys, null for a value the input does not state. No prose, no code fences."
        return _json_object(chat(_text(data), system=system, timeout=timeout))
    from pydantic import BaseModel, create_model

    if isinstance(schema, dict):
        if not schema or not all(isinstance(name, str) and isinstance(kind, type) for name, kind in schema.items()):
            raise LLMError("schema must map field names to types, e.g. {'vendor': str, 'amount': float}")
        cache_key = ("extract", tuple((name, kind.__name__) for name, kind in schema.items()))
        schema = create_model("Extracted", **{name: (kind | None, None) for name, kind in schema.items()})
    elif isinstance(schema, type) and issubclass(schema, BaseModel):
        cache_key = ("extract", schema)
    else:
        raise LLMError("schema must be a pydantic model class or a dict of field types")
    runnable = _structured(model(timeout=timeout), schema, cache_key)
    system = instructions.strip() or "Extract the requested fields from the input. Leave a field empty when the input does not state it."
    result = _invoke(runnable, _messages(system, _text(data)), timeout)
    if hasattr(result, "model_dump"):
        return result.model_dump()
    if isinstance(result, dict):
        return result
    raise LLMError(f"the model returned {type(result).__name__}, not the requested fields")
