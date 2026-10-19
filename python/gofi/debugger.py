"""Record-and-replay step debugging, inside the isolated worker.

One invocation runs under sys.settrace in a spawned process (the same
deadline-kill boundary as normal invocations). Every call/line/return/
exception event in the invoked function's source file is recorded with the
stack, bounded locals, changed library globals, watch values, and the length
of stdout so far. The client replays the recording; nothing is interactive.
Watch expressions are user-supplied Python evaluated here, in the isolated
worker, at the same trust level as the library source.
"""
import inspect
import json
import reprlib
import sys
import time
import types

DEFAULT_MAX_STEPS = 5000
MAX_STEPS = 20000
MAX_RECORDING_BYTES = 4 << 20
MAX_REPR = 200
MAX_DEPTH = 2
MAX_WIDTH = 50
MAX_WATCHES = 20

_repr = reprlib.Repr()
_repr.maxstring = _repr.maxother = _repr.maxlong = MAX_REPR
_repr.maxlist = _repr.maxtuple = _repr.maxdict = _repr.maxset = _repr.maxfrozenset = 10


class DebugAbort(BaseException):
    """Raised from the tracer to stop user code at the debug time budget."""


def safe_repr(value):
    try:
        text = _repr.repr(value)
    except Exception as exc:  # user __repr__ may raise anything
        return f"<repr failed: {type(exc).__name__}>", True
    return (text[:MAX_REPR], True) if len(text) > MAX_REPR else (text, False)


# Names whose values are credentials wherever they appear (the same list the
# controller redacts in flow definitions, plus common SDK spellings). Their
# values are recorded as *** so a recording never carries a key.
SECRET_NAMES = frozenset({
    "api_key", "apikey", "password", "token", "secret", "secret_key", "secretaccesskey", "sessiontoken",
    "dsn", "nkeyseed", "creds", "tlskey", "authorization", "bearer", "access_token", "refresh_token", "client_secret",
})


def is_secret_name(name):
    return str(name).replace("-", "_").lower() in SECRET_NAMES


def variable(name, value, depth=0):
    if is_secret_name(name) and value is not None and value != "":
        return {"name": str(name), "type": type(value).__name__, "repr": "'***'", "redacted": True}
    text, truncated = safe_repr(value)
    out = {"name": str(name), "type": type(value).__name__, "repr": text}
    if truncated:
        out["truncated"] = True
    if depth < MAX_DEPTH:
        items = None
        if isinstance(value, dict):
            items = [(safe_repr(key)[0], item) for key, item in list(value.items())[:MAX_WIDTH + 1]]
        elif isinstance(value, (list, tuple, set, frozenset)):
            items = [(f"[{index}]", item) for index, item in enumerate(list(value)[:MAX_WIDTH + 1])]
        if items:
            if len(items) > MAX_WIDTH:
                items, out["truncated"] = items[:MAX_WIDTH], True
            out["children"] = [variable(key, item, depth + 1) for key, item in items]
    return out


def interesting_globals(namespace):
    """Library globals worth showing: data, not modules/functions/classes/dunders."""
    for name, value in namespace.items():
        if name.startswith("__") or isinstance(value, (types.ModuleType, types.FunctionType, types.BuiltinFunctionType, type)):
            continue
        if name.startswith("_gofi"):
            continue
        yield name, value


class Recorder:
    def __init__(self, filename, namespace, stdout, watches, max_steps, deadline):
        self.filename, self.namespace, self.stdout = filename, namespace, stdout
        self.watches = [w for w in watches if w.strip()][:MAX_WATCHES]
        self.max_steps = max(1, min(max_steps or DEFAULT_MAX_STEPS, MAX_STEPS))
        self.deadline = deadline
        self.steps, self.size = [], 0
        self.truncated, self.reason = False, ""
        self.previous_locals = {}  # id(frame) -> {name: repr}
        self.previous_globals = {name: safe_repr(value)[0] for name, value in interesting_globals(namespace)}

    def initial_globals(self):
        return [variable(name, value) for name, value in interesting_globals(self.namespace)]

    # sys.settrace entry point: decide per new frame whether to trace it.
    def trace(self, frame, event, arg):
        self.check_deadline()
        if frame.f_code.co_filename != self.filename:
            return None
        self.record(frame, event, arg)
        return self.local

    def local(self, frame, event, arg):
        self.check_deadline()
        if event in ("line", "return", "exception"):
            self.record(frame, event, arg)
        return self.local

    def check_deadline(self):
        if time.monotonic() > self.deadline:
            if not self.truncated:
                self.truncated, self.reason = True, "deadline"
            sys.settrace(None)
            raise DebugAbort("debug time budget exhausted")

    def stack(self, frame):
        frames = []
        while frame is not None:
            if frame.f_code.co_filename == self.filename:
                frames.append({"function": frame.f_code.co_qualname, "line": frame.f_lineno})
            frame = frame.f_back
        return frames[::-1]

    def record(self, frame, event, arg):
        if self.truncated:
            return
        if len(self.steps) >= self.max_steps:
            self.truncated, self.reason = True, "max_steps"
            return
        stack = self.stack(frame)
        step = {"index": len(self.steps), "event": event, "line": frame.f_lineno, "function": frame.f_code.co_qualname,
                "depth": max(0, len(stack) - 1), "stack": stack, "stdoutLength": self.stdout.size}
        previous = self.previous_locals.get(id(frame), {})
        current, local_vars = {}, []
        for name, value in list(frame.f_locals.items()):
            entry = variable(name, value)
            current[name] = entry["repr"]
            if previous.get(name, None) != entry["repr"]:
                entry["changed"] = True
            local_vars.append(entry)
        self.previous_locals[id(frame)] = current
        if event == "return":
            self.previous_locals.pop(id(frame), None)
        step["locals"] = local_vars
        changed = []
        for name, value in interesting_globals(frame.f_globals):
            text = safe_repr(value)[0]
            if self.previous_globals.get(name) != text:
                self.previous_globals[name] = text
                entry = variable(name, value)
                entry["changed"] = True
                changed.append(entry)
        if changed:
            step["changedGlobals"] = changed
        if self.watches:
            step["watches"] = [self.watch(expression, frame) for expression in self.watches]
        if event == "return":
            step["returnValue"] = variable("return", arg)
        elif event == "exception":
            kind, value, _ = arg
            step["exception"] = f"{kind.__name__}: {value}"[:1024]
        size = len(json.dumps(step, default=str))
        if self.size + size > MAX_RECORDING_BYTES:
            self.truncated, self.reason = True, "size"
            return
        self.size += size
        self.steps.append(step)

    @staticmethod
    def watch(expression, frame):
        # A watch that simply names a credential is answered with ***; any
        # other expression runs as user code in the worker, so a library is
        # one trust domain and the worker holds no deployment secrets.
        if is_secret_name(expression.strip()):
            return {"expression": expression, "repr": "'***'", "redacted": True}
        try:
            value = eval(expression, frame.f_globals, dict(frame.f_locals))
        except Exception as exc:
            return {"expression": expression, "error": f"{type(exc).__name__}: {exc}"[:300]}
        return {"expression": expression, "repr": safe_repr(value)[0]}


def target(callback):
    """Source file and module namespace of the invoked function."""
    function = inspect.unwrap(callback)
    code = getattr(function, "__code__", None)
    return (code.co_filename if code else "<gofi-library>"), getattr(function, "__globals__", {})
