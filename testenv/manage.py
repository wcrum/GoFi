#!/usr/bin/env python3
"""Manage the explicit-request GoFi test environment without deleting its data."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
COMPOSE = ["docker", "compose", "-f", str(ROOT / "compose.test.yaml")]

def command(*args):
    subprocess.run(args, cwd=ROOT, check=True)

def ready():
    endpoint = "http://127.0.0.1:19090/gofi.v1.GoFiService/GetRuntimeStatus"
    for _ in range(120):
        try:
            request = urllib.request.Request(endpoint, b'{"name":"real-events"}', {"Content-Type": "application/json", "Connect-Protocol-Version": "1"})
            with urllib.request.urlopen(request, timeout=2) as response:
                runtime = json.load(response).get("runtime", {})
            if runtime.get("status") == "running":
                for port in (19080, 19081):
                    with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=2):
                        pass
                return
        except (OSError, ValueError, urllib.error.URLError):
            pass
        time.sleep(0.5)
    raise SystemExit("GoFi test stack did not become ready; run docker compose -f compose.test.yaml logs")

def stop():
    # Listener containers are managed by GoFi's Docker driver, not Compose.
    # Stop reconciliation before removing this stack's own listener runtime.
    command(*COMPOSE, "stop", "gofi")
    result = subprocess.run(["docker", "inspect", "gofi-listener-real-events"], capture_output=True, text=True)
    if result.returncode == 0:
        info = json.loads(result.stdout)[0]
        labels = info.get("Config", {}).get("Labels", {})
        networks = info.get("NetworkSettings", {}).get("Networks", {})
        if labels.get("gofi.managed-by") != "true" or labels.get("gofi.listener") != "real-events" or "gofi-test" not in networks:
            raise SystemExit("refusing to remove a listener outside the GoFi test stack")
        command("docker", "rm", "-f", "gofi-listener-real-events")
    command(*COMPOSE, "down")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("up", "verify", "verify-python", "verify-tools", "stop", "status"))
    action = parser.parse_args().action
    if action == "up":
        command(*COMPOSE, "up", "-d")
        ready()
        print("GoFi portal: http://localhost:19090\nSend actual events: http://localhost:19080\nPersisted receiver records: http://localhost:19081\nPrometheus: http://localhost:19094")
    elif action == "verify":
        ready()
        command("python3", str(ROOT / "testenv/verify.py"))
    elif action == "verify-python":
        ready()
        command("python3", str(ROOT / "testenv/verify_python.py"))
    elif action == "verify-tools":
        ready()
        command(os.getenv("GOFI_TEST_PYTHON", "python3"), str(ROOT / "testenv/verify_tools.py"))
    elif action == "stop":
        stop()
    else:
        command(*COMPOSE, "ps")
