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

echo "all http real probes ok"
