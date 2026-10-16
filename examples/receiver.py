#!/usr/bin/env python3
"""Small local HTTP receiver for the GoFi telemetry walkthrough (Python 3)."""

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Receiver(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/events":
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            self.send_error(400)
            return
        if not 0 <= length <= 2 * 1024 * 1024:
            self.send_error(413)
            return
        payload = self.rfile.read(length)
        try:
            event = json.loads(payload)
        except (UnicodeDecodeError, json.JSONDecodeError):
            self.send_error(400)
            return
        print(json.dumps(event, sort_keys=True), flush=True)
        self.send_response(202)
        self.send_header("Content-Length", "0")
        self.end_headers()


if __name__ == "__main__":
    print("Listening on http://127.0.0.1:8080/events", flush=True)
    ThreadingHTTPServer(("127.0.0.1", 8080), Receiver).serve_forever()
