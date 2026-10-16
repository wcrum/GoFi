#!/usr/bin/env python3
"""Seed a running GoFi portal with an example flow and send continuous local TCP demo traffic."""
import argparse
import base64
import json
import math
import socket
import time
import urllib.request


def main():
    args = argparse.ArgumentParser(description=__doc__)
    args.add_argument("--api", default="http://127.0.0.1:9090")
    args.add_argument("--port", default=10001, type=int)
    args.add_argument("--name", default="telemetry-demo", help="flow name to create or replace")
    options = args.parse_args()
    definition = {
        "name": options.name,
        "source": {"listen": {"network": "tcp", "port": options.port}},
        "framing": {"mode": "line"},
        "parser": {"format": "json", "decoders": ["base64"], "errorPolicy": "drop"},
        "stages": [
            {"id": "valid", "kind": "schema", "requiredFields": ["sensor", "temperature"]},
            {"id": "hot", "kind": "filter", "type": "cel", "expression": "fields.temperature >= 24.0"},
            {"id": "tag", "kind": "transform", "transformOp": "set", "field": "route", "valueExpr": "'operations'"},
        ],
        "sinks": [{"type": "stdout"}],
        "deadLetterSink": {"type": "stdout"},
        "testCases": [
            {"id": "pass", "name": "Hot sensor passes", "raw": base64.b64encode(b'{"sensor":"alpha","temperature":28}').decode()},
            {"id": "drop", "name": "Cool sensor is filtered", "raw": base64.b64encode(b'{"sensor":"bravo","temperature":18}').decode()},
        ],
    }
    req = urllib.request.Request(options.api + "/gofi.v1.GoFiService/UpsertFlow", data=json.dumps({"flow": definition}).encode(), headers={"Content-Type": "application/json", "Connect-Protocol-Version": "1"})
    with urllib.request.urlopen(req, timeout=10) as response:
        assert response.status == 200
    print(f"Demo ready: {options.api} | flow {options.name} TCP :{options.port}", flush=True)
    sequence = 0
    while True:
        payload = {"sensor": ["alpha", "bravo", "charlie"][sequence % 3], "temperature": round(24 + 8 * math.sin(sequence / 4), 1), "sequence": sequence}
        try:
            with socket.create_connection(("127.0.0.1", options.port), timeout=2) as conn:
                conn.sendall(base64.b64encode(json.dumps(payload).encode()) + b"\n")
        except OSError as error:
            print(f"Waiting for the demo flow's listen source: {error}", flush=True)
        sequence += 1
        time.sleep(1)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
