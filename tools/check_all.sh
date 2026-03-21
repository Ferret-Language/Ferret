#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# Ensure user-installed Go tools are discoverable even in non-login shells.
export PATH="$PATH:$HOME/go/bin"
# Keep analyzer caches inside the repo to avoid permission issues in restricted envs.
export GOCACHE="${GOCACHE:-$ROOT_DIR/.tmp/gocache}"
export STATICCHECK_CACHE="${STATICCHECK_CACHE:-$ROOT_DIR/.tmp/staticcheck-cache}"
mkdir -p "$GOCACHE" "$STATICCHECK_CACHE"

MISSING_TOOLS=()

if ! command -v go >/dev/null 2>&1; then
  echo "error: 'go' is not installed or not in PATH"
  exit 1
fi

if ! command -v staticcheck >/dev/null 2>&1; then
  MISSING_TOOLS+=("staticcheck")
fi

if ! command -v gopls >/dev/null 2>&1; then
  MISSING_TOOLS+=("gopls")
fi

if ((${#MISSING_TOOLS[@]} > 0)); then
  echo "warning: missing tools: ${MISSING_TOOLS[*]}"
  echo "install with:"
  for tool in "${MISSING_TOOLS[@]}"; do
    case "$tool" in
      staticcheck)
        echo "  go install honnef.co/go/tools/cmd/staticcheck@latest"
        ;;
      gopls)
        echo "  go install golang.org/x/tools/gopls@latest"
        ;;
    esac
  done
  echo
fi

echo "==> go vet ./..."
go vet ./...

if command -v staticcheck >/dev/null 2>&1; then
  echo "==> staticcheck ./..."
  staticcheck ./...
else
  echo "==> skipping staticcheck (not installed)"
fi

if command -v gopls >/dev/null 2>&1; then
  echo "==> gopls check (all module go files)..."
  mapfile -t GOFILES < <(go list -f '{{range .GoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .TestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}' ./...)
  if ((${#GOFILES[@]} > 0)); then
    gopls check "${GOFILES[@]}"
  fi
else
  echo "==> skipping gopls check (not installed)"
fi

echo "==> go test ./..."
go test ./...

echo "done: all available checks completed"
