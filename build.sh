#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEV_LIBS_DIR="$ROOT_DIR/ferret_libs_dev"
LIBS_DIR="$ROOT_DIR/libs"
BIN_DIR="$ROOT_DIR/bin"
RUNTIME_DIR="$ROOT_DIR/runtime"

mkdir -p "$LIBS_DIR" "$BIN_DIR"

if [[ ! -d "$DEV_LIBS_DIR" ]]; then
  echo "error: missing library source directory: $DEV_LIBS_DIR" >&2
  exit 1
fi

if [[ ! -d "$RUNTIME_DIR" ]]; then
  echo "error: missing runtime directory: $RUNTIME_DIR" >&2
  exit 1
fi

# Copy ferret standard library sources into libs/.
cp -R "$DEV_LIBS_DIR"/. "$LIBS_DIR"/

# Compile the Ferret C runtime to a static archive.
# The compiler links against libs/ferret_runtime.a at build time;
# it never recompiles the runtime itself.
RUNTIME_SRC="$RUNTIME_DIR/ferret_runtime.c"
RUNTIME_OBJ="$LIBS_DIR/ferret_runtime.o"
RUNTIME_LIB="$LIBS_DIR/ferret_runtime.a"

if [[ ! -f "$RUNTIME_SRC" ]]; then
  echo "error: runtime source not found: $RUNTIME_SRC" >&2
  exit 1
fi

echo "compiling runtime: $RUNTIME_SRC -> $RUNTIME_LIB"
cc -O2 -c "$RUNTIME_SRC" -o "$RUNTIME_OBJ"
ar rcs "$RUNTIME_LIB" "$RUNTIME_OBJ"
rm -f "$RUNTIME_OBJ"

# Build the ferret compiler binary.
cd "$ROOT_DIR"
go build -o "$BIN_DIR/ferret" ./cmd/langc

echo "done: $BIN_DIR/ferret"
