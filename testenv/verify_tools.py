#!/usr/bin/env python3
"""Run the canonical generated-client tooling verifier against the test portal."""
import importlib.util
import os
from pathlib import Path
import sys

if importlib.util.find_spec("connectrpc") is None:
    raise SystemExit("Install python/requirements.txt in a virtualenv, then use its Python interpreter for testenv/verify_tools.py")
root=Path(__file__).resolve().parents[1]
os.environ["PYTHONPATH"]=str(root/"python")+os.pathsep+os.environ.get("PYTHONPATH", "")
script=root/"scripts/verify_python_tools.py"
os.execv(sys.executable,[sys.executable,str(script),"--api","http://127.0.0.1:19090",*sys.argv[1:]])
