#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WEBSITE_DIR="${FERRET_WEBSITE_DIR:-$ROOT_DIR/../website}"
WASM_CMD="${FERRET_WASM_CMD:-./cmd/ferret-wasm}"

OUT_WASM="$WEBSITE_DIR/public/ferret2.wasm"
OUT_WASM_EXEC="$WEBSITE_DIR/public/wasm_exec.js"

if ! command -v go >/dev/null 2>&1; then
  echo "go is required" >&2
  exit 1
fi

if [ ! -d "$WEBSITE_DIR/public" ]; then
  echo "website public dir not found: $WEBSITE_DIR/public" >&2
  exit 1
fi

GOROOT="$(go env GOROOT)"
WASM_EXEC_SRC=""
if [ -f "$GOROOT/lib/wasm/wasm_exec.js" ]; then
  WASM_EXEC_SRC="$GOROOT/lib/wasm/wasm_exec.js"
elif [ -f "$GOROOT/misc/wasm/wasm_exec.js" ]; then
  WASM_EXEC_SRC="$GOROOT/misc/wasm/wasm_exec.js"
else
  echo "could not locate wasm_exec.js in GOROOT=$GOROOT" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

TMP_WASM="$TMP_DIR/ferret2.wasm"
TMP_WASM_EXEC="$TMP_DIR/wasm_exec.js"

pushd "$ROOT_DIR" >/dev/null
GOOS=js GOARCH=wasm go build -trimpath -ldflags "-s -w" -o "$TMP_WASM" "$WASM_CMD"
popd >/dev/null

cp "$WASM_EXEC_SRC" "$TMP_WASM_EXEC"

if command -v node >/dev/null 2>&1; then
  cat > "$TMP_DIR/validate.mjs" <<'EOF'
import fs from "node:fs/promises";
import "./wasm_exec.js";

const go = new Go();
const bytes = await fs.readFile("./ferret2.wasm");
const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
void go.run(instance);
await new Promise((r) => setTimeout(r, 200));

const ok = typeof globalThis.ferretCompile === "function";
const version = globalThis.ferretWasmVersion ?? "unknown";
if (!ok) {
  console.error("validation failed: global ferretCompile not found");
  process.exit(2);
}
console.log(`validated wasm bridge (version: ${String(version)})`);
EOF

  pushd "$TMP_DIR" >/dev/null
  node ./validate.mjs
  popd >/dev/null
else
  echo "warning: node not found; skipping ferretCompile export validation" >&2
fi

cp "$TMP_WASM" "$OUT_WASM"
cp "$WASM_EXEC_SRC" "$OUT_WASM_EXEC"

echo "built wasm compiler: $OUT_WASM"
echo "updated wasm runtime: $OUT_WASM_EXEC"
echo "note: this entrypoint must export browser globals (ferretCompile/ferretWasmVersion) for playground usage"
