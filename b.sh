#!/usr/bin/env bash

name="$1"

if [ -z "$name" ]; then
  echo "Usage: ./b.sh <filename>"
  exit 1
fi

./build.sh
./bin/ferret -backend llvm -backend-out "${name}.ll" "${name}.ferr"
clang "${name}.ll"