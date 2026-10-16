"""Real request-driven sender and durable HTTP receiver for a GoFi test stack."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import json
import os
import socket
import threading

ROLE = os.environ.get("ROLE", "sender")
RECEIVED = Path(os.environ.get("RECEIVED_FILE", "/received/events.jsonl"))
LOCK = threading.Lock()
LIMIT = 1048576
PAGE = '''<!doctype html><html><head><title>GoFi application test</title><meta charset="utf-8"></head>
<body style="background:#111217;color:#ddd;font:16px system-ui;margin:40px;max-width:900px">
<h1>GoFi real application test</h1><p id="role"></p>
<div id="sender"><textarea id="event" rows="8" style="width:100%;background:#181b1f;color:#ddd">{"app":"my-application","message":"hello from an actual request","status":201}</textarea>
<p><button id="send">Send event through GoFi</button></p></div>
<p><a href="RECEIVER_EVENTS_VALUE" style="color:#ff9f46">View persisted receiver events as JSON</a></p><pre id="result" style="white-space:pre-wrap"></pre>
<script>const role="ROLE_VALUE";document.querySelector('#role').textContent=role==='sender'?'This application sends only when you submit an event.':'This receiver shows events actually delivered by GoFi.';
if(role==='receiver'){document.querySelector('#sender').hidden=true;fetch('/events').then(r=>r.json()).then(v=>document.querySelector('#result').textContent=JSON.stringify(v,null,2));}
document.querySelector('#send').onclick=async()=>{try{const event=JSON.parse(document.querySelector('#event').value);const r=await fetch('/events',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(event)});document.querySelector('#result').textContent=JSON.stringify(await r.json(),null,2);}catch(e){document.querySelector('#result').textContent=String(e);}};</script></body></html>'''

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def respond(self, status, value):
        data = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/healthz":
            return self.respond(200, {"role": ROLE, "ready": True})
        if self.path == "/":
            data = PAGE.replace("ROLE_VALUE", ROLE).replace("RECEIVER_EVENTS_VALUE", "/events" if ROLE == "receiver" else "http://localhost:19081/events").encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(data)
            return
        if self.path == "/events" and ROLE == "receiver":
            with LOCK:
                lines = RECEIVED.read_text().splitlines() if RECEIVED.exists() else []
            records = [json.loads(line) for line in lines[-1000:]]
            return self.respond(200, {"count": len(lines), "events": records})
        self.respond(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/events":
            return self.respond(404, {"error": "not found"})
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= LIMIT:
                return self.respond(413, {"error": "JSON event must contain 1-1048576 bytes"})
            event = json.loads(self.rfile.read(length))
            if not isinstance(event, dict):
                return self.respond(400, {"error": "event must be a JSON object"})
            encoded = json.dumps(event, separators=(",", ":"), allow_nan=False).encode()
            if ROLE == "sender":
                with socket.create_connection((os.environ["SOURCE_HOST"], int(os.environ["SOURCE_PORT"])), timeout=5) as connection:
                    connection.sendall(encoded + b"\n")
                print(json.dumps({"level": "INFO", "msg": "event written to GoFi source", "bytes": len(encoded)}), flush=True)
                return self.respond(202, {"accepted": True, "event": event})
            RECEIVED.parent.mkdir(parents=True, exist_ok=True)
            with LOCK, RECEIVED.open("ab") as output:
                output.write(encoded + b"\n")
                output.flush()
                os.fsync(output.fileno())
            print(json.dumps({"level": "INFO", "msg": "event persisted by receiver", "bytes": len(encoded)}), flush=True)
            self.respond(201, {"stored": True})
        except (ValueError, OSError) as error:
            print(json.dumps({"level": "ERROR", "msg": "event processing failed", "error_type": type(error).__name__}), flush=True)
            self.respond(502, {"error": "upstream unavailable or invalid event"})

if __name__ == "__main__":
    print(json.dumps({"level": "INFO", "msg": "test application listening", "role": ROLE, "port": 8080}), flush=True)
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
