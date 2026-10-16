"""Fixed-target RPC relay for native controllers outside Docker's VM.

The worker stays exclusively on an internal bridge. This relay publishes a
loopback control port and forwards only its worker's fixed unary RPC methods.
It cannot serve as a general HTTP or CONNECT proxy.
"""
from http.client import HTTPConnection
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import os

TARGET = os.environ["GOFI_WORKER_TARGET"]
ALLOWED = {"/gofi.v1.WorkerService/ListFunctions", "/gofi.v1.WorkerService/Invoke", "/gofi.v1.WorkerService/FormatSource", "/gofi.v1.WorkerService/ValidateSource", "/gofi.v1.WorkerService/DescribeEnvironment", "/gofi.v1.WorkerService/Debug"}
LIMIT = 2 * 1048576

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        if self.path not in ALLOWED:
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 <= length <= LIMIT:
                self.send_error(413)
                return
            body = self.rfile.read(length)
            headers = {key: value for key, value in self.headers.items() if key.lower() not in {"host", "connection", "transfer-encoding"}}
            connection = HTTPConnection(TARGET, timeout=35)
            try:
                connection.request("POST", self.path, body=body, headers=headers)
                response = connection.getresponse()
                data = response.read(LIMIT + 1)
                if len(data) > LIMIT:
                    self.send_error(502)
                    return
                self.send_response(response.status)
                for key, value in response.getheaders():
                    if key.lower() not in {"connection", "transfer-encoding", "content-length"}:
                        self.send_header(key, value)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)
            finally:
                connection.close()
        except (ValueError, OSError):
            self.send_error(502)

if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
