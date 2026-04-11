#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
FERRET_BIN="${FERRET_BIN:-$ROOT/build/core/bin/ferret}"

if [[ ! -x "$FERRET_BIN" ]]; then
    echo "missing ferret binary: $FERRET_BIN" >&2
    exit 1
fi

run_probe() {
    local name="$1"
    local want="$2"
    shift 2

    local out
    out="$("$@")"
    if [[ "$out" != "$want" ]]; then
        echo "$name failed: expected '$want', got '$out'" >&2
        exit 1
    fi
    echo "$name ok"
}

python - <<'PY' &
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        if self.path != "/hello":
            self.send_response(404)
            self.send_header("Content-Length", "0")
            self.send_header("Connection", "close")
            self.end_headers()
            return
        body = b"hello-http"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        pass

server = HTTPServer(("127.0.0.1", 9110), Handler)
server.handle_request()
server.server_close()
PY
server_pid=$!
sleep 0.2
run_probe "http-get" "http-get-ok" \
    "$FERRET_BIN" run "$ROOT/tests/repro/http_get_probe.fer"
wait "$server_pid" || true

"$FERRET_BIN" run "$ROOT/tests/repro/http_server_once_probe.fer" >/tmp/ferret-http-server-probe.out &
server_pid=$!
sleep 1.0
python - <<'PY'
import socket

with socket.create_connection(("127.0.0.1", 9111), timeout=2) as sock:
    sock.sendall(b"GET /hello HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
    data = b""
    while b"\r\n\r\n" not in data:
        chunk = sock.recv(4096)
        if not chunk:
            break
        data += chunk

    header, _, body = data.partition(b"\r\n\r\n")
    content_length = 0
    for line in header.split(b"\r\n"):
        if line.lower().startswith(b"content-length:"):
            content_length = int(line.split(b":", 1)[1].strip())
            break
    while len(body) < content_length:
        chunk = sock.recv(4096)
        if not chunk:
            break
        body += chunk

    text = header.decode("utf-8") + "\r\n\r\n" + body.decode("utf-8")
    if "HTTP/1.1 200 OK\r\n" not in text or body.decode("utf-8") != "hello-server":
        raise SystemExit(f"unexpected response: {text!r}")
PY
wait "$server_pid"
run_probe "http-server-once" "http-server-once-ok" cat /tmp/ferret-http-server-probe.out

"$FERRET_BIN" run "$ROOT/tests/repro/http_server_routes_probe.fer" >/tmp/ferret-http-server-routes-probe.out &
server_pid=$!
sleep 1.0
python - <<'PY'
import socket

with socket.create_connection(("127.0.0.1", 9112), timeout=2) as sock:
    sock.sendall(b"GET /hello HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
    data = b""
    while b"\r\n\r\n" not in data:
        chunk = sock.recv(4096)
        if not chunk:
            break
        data += chunk

    header, _, body = data.partition(b"\r\n\r\n")
    content_length = 0
    for line in header.split(b"\r\n"):
        if line.lower().startswith(b"content-length:"):
            content_length = int(line.split(b":", 1)[1].strip())
            break
    while len(body) < content_length:
        chunk = sock.recv(4096)
        if not chunk:
            break
        body += chunk

    text = header.decode("utf-8") + "\r\n\r\n" + body.decode("utf-8")
    if "HTTP/1.1 200 OK\r\n" not in text or body.decode("utf-8") != "hello-route":
        raise SystemExit(f"unexpected route response: {text!r}")
PY
wait "$server_pid"
run_probe "http-server-routes" "http-server-routes-ok" cat /tmp/ferret-http-server-routes-probe.out

echo "all http real probes ok"
