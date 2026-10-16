"""Exercise durable library CRUD, function discovery, and a real Python pipeline."""
import json
import base64
import os
import socket
import time
import urllib.error
import urllib.request
import uuid

BASE = "http://127.0.0.1:19090/"
LIBRARY = "test-python-library"
FLOW = "test-python-events"
SOURCE = '''import json
import gofi

@gofi.parser("decode")
def decode(raw):
    return json.loads(raw)

@gofi.transformer("tag")
def tag(fields, meta):
    return {**fields, "python": "verified"}
'''

def rpc(service, method, value):
    request = urllib.request.Request(BASE + "gofi.v1." + service + "/" + method, json.dumps(value).encode(), {"Content-Type": "application/json", "Connect-Protocol-Version": "1"})
    try:
        with urllib.request.urlopen(request, timeout=55) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"{service}/{method}: {error.code} {error.read().decode()}") from error

library_created = False
flow_created = False
verified = False
try:
    applied = rpc("SandboxService", "ApplySandbox", {"sandbox": {"name": LIBRARY, "sourceCode": SOURCE, "timeoutSeconds": 5, "memoryMib": 128}})
    library_created = True
    if applied.get("status", {}).get("phase") != "Ready":
        raise RuntimeError(f"worker did not become ready: {applied.get('status')}")
    functions = rpc("SandboxService", "ListFunctions", {"library": LIBRARY})["functions"]
    if {(item["name"], item["kind"]) for item in functions} != {("decode", "parser"), ("tag", "transformer")}:
        raise RuntimeError(f"wrong registry: {functions}")
    run_input={"app":"run-function-check","status":201}
    run = rpc("SandboxService","RunFunction",{"library":LIBRARY,"name":"decode","kind":"parser","raw":base64.b64encode(json.dumps(run_input).encode()).decode()})
    if run.get("fields")!=run_input:
        raise RuntimeError(f"RunFunction returned wrong parser output: {run}")
    flow = {"name": FLOW, "source": {"listen": {"network": "tcp", "port": 17001}}, "framing": {"mode": "line", "maxBytes": 1048576}, "parser": {"format": "python", "python": {"library": LIBRARY, "function": "decode"}, "errorPolicy": "drop"}, "stages": [{"id": "python-tag", "kind": "python", "python": {"library": LIBRARY, "function": "tag"}}], "sinks": [{"type": "http", "url": "http://receiver:8080/events", "bodyMode": "wrapped"}]}
    rpc("GoFiService", "UpsertFlow", {"flow": flow})
    flow_created = True
    for attempt in range(80):
        try:
            runtime = rpc("GoFiService", "GetRuntimeStatus", {"name": FLOW}).get("runtime", {})
        except RuntimeError as error:
            if "404" not in str(error):
                raise
            runtime = {}
        if runtime.get("status") == "running":
            break
        time.sleep(0.25)
    else:
        raise RuntimeError("custom Python flow runtime not running")
    event = {"eventId": str(uuid.uuid4()), "app": "python-controlplane-test", "status": 201}
    with socket.create_connection(("127.0.0.1", 17001), timeout=5) as stream:
        stream.sendall(json.dumps(event).encode() + b"\n")
    for attempt in range(300):
        with urllib.request.urlopen("http://127.0.0.1:19081/events", timeout=5) as response:
            records = json.load(response)
        matching = [record for record in records["events"] if record.get("fields", {}).get("eventId") == event["eventId"]]
        if matching:
            expected = {**event, "python": "verified"}
            if matching[-1]["fields"] != expected:
                raise RuntimeError(f"wrong Python output: {matching[-1]}")
            verified = True
            print(json.dumps({"verified": True, "libraryStatus": applied["status"], "registeredFunctions": functions, "runFunction": run, "sent": event, "received": matching[-1], "persistedRecordCount": records["count"]}, indent=2))
            break
        time.sleep(0.1)
    else:
        raise RuntimeError("custom Python parser/transformer event not received")
finally:
    keep = not verified and os.getenv("GOFI_KEEP_FAILED_TEST") == "1"
    if flow_created and not keep:
        rpc("GoFiService", "DeleteFlow", {"name": FLOW})
    if library_created and not keep:
        rpc("SandboxService", "DeleteSandbox", {"name": LIBRARY})
