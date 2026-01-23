#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEBSITE_ROOT="$ROOT/../website"
WEBSITE_PUBLIC="$WEBSITE_ROOT/public"

echo "Building Go WASM compiler..."
GOCACHE="$ROOT/.gocache" GOOS=js GOARCH=wasm go build -o "$ROOT/bin/ferret.wasm" "$ROOT/main_wasm.go"

if [ -d "$WEBSITE_PUBLIC" ]; then
  cp "$ROOT/bin/ferret.wasm" "$WEBSITE_PUBLIC/ferret2.wasm"
  echo "Copied wasm compiler to $WEBSITE_PUBLIC/ferret2.wasm"
else
  echo "Website public folder not found: $WEBSITE_PUBLIC"
  echo "Built wasm compiler at $ROOT/bin/ferret.wasm"
fi

if [ -d "$WEBSITE_ROOT" ]; then
  mkdir -p "$WEBSITE_ROOT/src/lib"
  cp "$ROOT/runtime/wasm/runtime.ts" "$WEBSITE_ROOT/src/lib/runtime.ts"
  echo "Copied runtime TS to $WEBSITE_ROOT/src/lib/runtime.ts"
  cp "$ROOT/runtime/wasm/runtime_abi.ts" "$WEBSITE_ROOT/src/lib/runtime_abi.ts"
  echo "Copied runtime ABI TS to $WEBSITE_ROOT/src/lib/runtime_abi.ts"
fi
