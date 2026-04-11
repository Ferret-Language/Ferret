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

run_probe "dial-fail" "connection_refused" \
    "$FERRET_BIN" run "$ROOT/tests/repro/tcp_dial_fail_probe.fer"

python - <<'PY' &
import socket
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 9101))
s.listen(1)
conn, _ = s.accept()
data = conn.recv(4096)
if data:
    conn.sendall(data)
conn.close()
s.close()
PY
server_pid=$!
sleep 0.2
run_probe "client-echo" "tcp-client-ok" \
    "$FERRET_BIN" run "$ROOT/tests/repro/tcp_client_echo_probe.fer"
wait "$server_pid" || true

python - <<'PY' &
import socket
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 9102))
s.listen(1)
conn, _ = s.accept()
conn.close()
s.close()
PY
server_pid=$!
sleep 0.2
run_probe "eof" "tcp-eof-ok" \
    "$FERRET_BIN" run "$ROOT/tests/repro/tcp_eof_probe.fer"
wait "$server_pid" || true

python - <<'PY' &
import socket, time
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 9106))
s.listen(1)
conn, _ = s.accept()
time.sleep(0.5)
conn.close()
s.close()
PY
server_pid=$!
sleep 0.2
run_probe "read-timeout" "timed_out" \
    "$FERRET_BIN" run "$ROOT/tests/repro/tcp_read_timeout_probe.fer"
wait "$server_pid" || true

run_probe "accept-timeout" "timed_out" \
    "$FERRET_BIN" run "$ROOT/tests/repro/tcp_accept_timeout_probe.fer"

python - <<'PY' &
import socket
payload = b"x" * 9000
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 9104))
s.listen(1)
conn, _ = s.accept()
conn.sendall(payload)
conn.close()
s.close()
PY
server_pid=$!
sleep 0.2
run_probe "large-read" "tcp-large-read-ok" \
    "$FERRET_BIN" run "$ROOT/tests/repro/tcp_large_read_probe.fer"
wait "$server_pid" || true

"$FERRET_BIN" run "$ROOT/tests/repro/tcp_listener_echo_probe.fer" >/tmp/ferret_tcp_listener_probe.out &
server_pid=$!
sleep 0.2
client_out="$(python - <<'PY'
import socket
s = socket.socket()
s.connect(("127.0.0.1", 9105))
s.sendall(b"ping")
data = s.recv(4096)
s.close()
print(data.decode("utf-8"))
PY
)"
wait "$server_pid"
listener_out="$(cat /tmp/ferret_tcp_listener_probe.out)"
rm -f /tmp/ferret_tcp_listener_probe.out
if [[ "$client_out" != "pong" ]]; then
    echo "listener-client failed: expected 'pong', got '$client_out'" >&2
    exit 1
fi
if [[ "$listener_out" != "tcp-listener-ok" ]]; then
    echo "listener-server failed: expected 'tcp-listener-ok', got '$listener_out'" >&2
    exit 1
fi
echo "listener ok"

python - <<'PY' &
import socket, time
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 9108))
s.listen(1)
conn, _ = s.accept()
data = b""
while True:
    chunk = conn.recv(4096)
    if not chunk:
        break
    data += chunk
if data == b"ping":
    conn.sendall(b"pong")
    time.sleep(0.3)
conn.close()
s.close()
PY
server_pid=$!
sleep 0.2
run_probe "conn-controls" "tcp-conn-controls-ok" \
    "$FERRET_BIN" run "$ROOT/tests/repro/tcp_conn_controls_probe.fer"
wait "$server_pid" || true

echo "all tcp real probes ok"
