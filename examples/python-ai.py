# AI steps for a GoFi Python library: classify and route, enrich, extract,
# or hand each message to the deployment's model as a sink. Nothing here
# names a provider, an endpoint, or a key: an operator configures the AI
# endpoint once under Administration › AI assistant and turns on "Functions
# may use the AI endpoint" there; every library then calls it through
# gofi.llm. See docs/python.md#ai-and-langchain-integrations.
#
#   apiVersion: gofi.wcrum.dev/v1alpha1
#   kind: Sandbox
#   metadata:
#     name: ai-library
#   spec:
#     timeoutSeconds: 30       # above the per-call timeout (20 s by default)
#     sourceCode: |
#       ... this file ...
#
# GoFi supplies the endpoint, the model, and the egress to reach it. The
# model key never enters the worker: calls go through the controller with
# the worker's own token. Every message is one model call; put filters,
# dedup, or the classifier in front of expensive steps.
from typing import Literal
import json

from pydantic import BaseModel

import gofi


@gofi.transformer("ai_classify")
def ai_classify(fields: dict, meta: dict, *, labels: list = ["normal", "suspicious", "critical"], drop: list = ["normal"], instructions: str = "Classify this event by how urgently an operator must look at it.") -> dict | None:
    """Label each message with one of labels; drop it when the label is in drop.

    labels: the allowed labels; the model picks exactly one
    drop: labels whose messages are dropped (everything else continues)
    instructions: what the labels mean
    """
    fields["label"] = gofi.llm.classify(fields, labels, instructions=instructions)
    return None if fields["label"] in drop else fields


@gofi.transformer("ai_enrich")
def ai_enrich(fields: dict, meta: dict, *, prompt: str = "Summarize this event in one sentence: {fields}", output_key: str = "summary") -> dict:
    """Ask the AI about each message and store its answer under output_key.

    prompt: {fields} is the message as JSON; {name} inserts one field
    output_key: field that receives the answer
    """
    fields[output_key] = gofi.llm.chat(prompt.format_map({**fields, "fields": json.dumps(fields)}))
    return fields


class Extracted(BaseModel):
    """The values to pull out of each message; a value the message does not state stays None."""

    vendor: str | None = None
    amount: float | None = None
    currency: str | None = None


@gofi.transformer("ai_extract")
def ai_extract(fields: dict, meta: dict, *, prompt: str = "Extract the vendor, amount, and currency from this event.\n\n{fields}", output_key: str = "extracted") -> dict:
    """Pull the Extracted values out of each message and store them under output_key.

    prompt: what to extract; {fields} is the message as JSON
    output_key: field that receives the values (a JSON object)
    """
    fields[output_key] = gofi.llm.extract(prompt.format_map({**fields, "fields": json.dumps(fields)}), Extracted)
    return fields


class Decision(BaseModel):
    """What the model decides for one message; the code below carries it out."""

    action: Literal["ignore", "notify", "ticket"]
    message: str = ""


@gofi.sink("ai_notify")
def ai_notify(fields: dict, meta: dict, *, prompt: str = "Triage this event for the on-call operator. Decide whether to ignore it, notify the channel, or open a ticket, and write the message to send: {fields}") -> None:
    """Let the AI decide what to do with each delivered message, then carry it out.

    prompt: {fields} is the message as JSON; {name} inserts one field
    """
    # 1. The model decides. Structured output limits it to the actions in
    #    Decision, so the model never gets free-form control of what happens.
    decision = gofi.llm.extract(prompt.format_map({**fields, "fields": json.dumps(fields)}), Decision)

    # 2. Your code acts. This is the agentic boundary: only what is written
    #    here can touch the world. Calls beyond print need an egress rule on
    #    the library's Network tab, and should be safe to repeat, since a flow
    #    can redeliver a message. Replace the prints with your webhook or
    #    ticket API call.
    if decision["action"] == "ignore":
        return
    if decision["action"] == "ticket":
        print(meta.get("name"), "ticket ->", decision["message"][:200])
        return
    print(meta.get("name"), "notify ->", decision["message"][:200])
