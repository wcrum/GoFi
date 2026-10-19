"""Structured live-log lines on the worker's real stdout/stderr.

Each line is one JSON object, so the controller's WatchWorkerLogs can relay
it as a WorkerLogEntry. User output is echoed as it is written (while also
captured for the invocation result), bounded per invocation.
"""
import datetime
import io
import json
import os

MAX_ECHO_LINES = 1000
MAX_ECHO_BYTES = 65536
MAX_LINE_CHARS = 4000


def _now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def emit(stream, line, *, invocation_id="", function="", kind="", level=""):
    """Write one JSON log line to fd 1 (stdout/system) or fd 2 (stderr).

    os.write keeps each line a single write, so concurrent processes sharing
    the container's output never interleave within a line.
    """
    record = {"ts": _now(), "stream": stream, "line": line[:MAX_LINE_CHARS]}
    if invocation_id:
        record["invocationId"] = invocation_id
    if function:
        record["function"] = function
    if kind:
        record["kind"] = kind
    if level:
        record["level"] = level
    data = (json.dumps(record, ensure_ascii=False) + "\n").encode("utf-8", "replace")
    try:
        os.write(2 if stream == "stderr" else 1, data)
    except OSError:
        pass


def system(line, level="info", **context):
    emit("system", line, level=level, **context)


class Echo:
    """Per-invocation echo budget shared by the stdout and stderr tees."""

    def __init__(self, invocation_id, function, kind):
        self.context = {"invocation_id": invocation_id, "function": function, "kind": kind}
        self.lines = self.size = 0
        self.truncated = False

    def line(self, stream, text):
        if self.truncated:
            return
        if self.lines >= MAX_ECHO_LINES or self.size + len(text) > MAX_ECHO_BYTES:
            self.truncated = True
            system(f"live output truncated after {self.lines} lines; the full capture (64 KiB) is in the run result", level="warn", **self.context)
            return
        self.lines += 1
        self.size += len(text)
        emit(stream, text, **self.context)


class Tee(io.TextIOBase):
    """Captures text for the invocation result and echoes complete lines live."""

    def __init__(self, capture, echo, stream):
        self.capture, self.echo, self.stream, self.pending = capture, echo, stream, ""

    def writable(self):
        return True

    def write(self, text):
        self.capture.write(text)
        self.pending += text
        while "\n" in self.pending:
            line, self.pending = self.pending.split("\n", 1)
            self.echo.line(self.stream, line)
        if len(self.pending) > MAX_LINE_CHARS:
            self.echo.line(self.stream, self.pending)
            self.pending = ""
        return len(text)

    def flush(self):
        """Lines are echoed when complete; partial text waits for finish()."""

    def finish(self):
        if self.pending:
            self.echo.line(self.stream, self.pending)
            self.pending = ""


class RateEcho(Echo):
    """Echo budget for long-lived streamers: MAX_ECHO_LINES / MAX_ECHO_BYTES
    per minute instead of per invocation."""

    WINDOW = 60.0

    def __init__(self, invocation_id, function, kind):
        super().__init__(invocation_id, function, kind)
        import time
        self._clock = time.monotonic
        self._window_start = self._clock()

    def line(self, stream, text):
        if self._clock() - self._window_start >= self.WINDOW:
            self._window_start = self._clock()
            self.lines = self.size = 0
            self.truncated = False
        super().line(stream, text)
