#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEV_LIBS_DIR="$ROOT_DIR/ferret_libs_dev"
LIBS_DIR="$ROOT_DIR/libs"
BIN_DIR="$ROOT_DIR/bin"

mkdir -p "$LIBS_DIR" "$BIN_DIR"

if [[ ! -d "$DEV_LIBS_DIR" ]]; then
  echo "missing library source directory: $DEV_LIBS_DIR" >&2
  exit 1
fi

cp -R "$DEV_LIBS_DIR"/. "$LIBS_DIR"/

cd "$ROOT_DIR"
go build -o "$BIN_DIR/ferret" ./cmd/langc
