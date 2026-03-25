#!/usr/bin/env bash
set -euo pipefail

REPO="${FERRET_REPO:-Ferret-Language/Ferret}"
VERSION="${1:-latest}"
INSTALL_DIR="${FERRET_INSTALL_DIR:-$HOME/.local/ferret}"
BIN_DIR="$INSTALL_DIR/bin"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required command not found: $1" >&2
    exit 1
  fi
}

download_to() {
  local url="$1"
  local out="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$out"
    return
  fi
  if command -v wget >/dev/null 2>&1; then
    wget -qO "$out" "$url"
    return
  fi
  echo "error: either curl or wget is required" >&2
  exit 1
}

detect_os() {
  case "$(uname -s)" in
    Linux) echo "linux" ;;
    Darwin) echo "macos" ;;
    *)
      echo "error: unsupported OS: $(uname -s)" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      echo "error: unsupported architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

pick_profile_file() {
  local shell_name
  shell_name="$(basename "${SHELL:-}")"
  if [[ "$shell_name" == "zsh" ]]; then
    echo "$HOME/.zshrc"
    return
  fi
  echo "$HOME/.bashrc"
}

ensure_path_persisted() {
  local profile_file="$1"
  local line='export PATH="$HOME/.local/ferret/bin:$PATH"'

  mkdir -p "$(dirname "$profile_file")"
  touch "$profile_file"
  if ! grep -Fq "$line" "$profile_file"; then
    {
      echo ""
      echo "# Added by Ferret installer"
      echo "$line"
    } >> "$profile_file"
  fi
}

require_cmd tar
require_cmd grep

OS="$(detect_os)"
ARCH="$(detect_arch)"
CORE_ASSET="ferret-${OS}-${ARCH}.tar.gz"
TOOLCHAIN_ASSET="ferret-toolchain-${OS}-${ARCH}.tar.gz"

if [[ "$VERSION" == "latest" ]]; then
  API_URL="https://api.github.com/repos/${REPO}/releases/latest"
else
  API_URL="https://api.github.com/repos/${REPO}/releases/tags/${VERSION}"
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

RELEASE_JSON="$TMP_DIR/release.json"
download_to "$API_URL" "$RELEASE_JSON"

find_asset_url() {
  local asset_name="$1"
  grep -Eo "https://[^\" ]+/${asset_name}" "$RELEASE_JSON" | head -n1 || true
}

CORE_URL="$(find_asset_url "$CORE_ASSET")"
if [[ -z "$CORE_URL" ]]; then
  echo "error: could not find release asset ${CORE_ASSET} in ${REPO} (${VERSION})" >&2
  exit 1
fi

TOOLCHAIN_URL="$(find_asset_url "$TOOLCHAIN_ASSET")"
if [[ -z "$TOOLCHAIN_URL" ]]; then
  echo "error: could not find release asset ${TOOLCHAIN_ASSET} in ${REPO} (${VERSION})" >&2
  exit 1
fi

CORE_ARCHIVE="$TMP_DIR/$CORE_ASSET"
TOOLCHAIN_ARCHIVE="$TMP_DIR/$TOOLCHAIN_ASSET"
echo "Downloading ${CORE_URL}"
download_to "$CORE_URL" "$CORE_ARCHIVE"
echo "Downloading ${TOOLCHAIN_URL}"
download_to "$TOOLCHAIN_URL" "$TOOLCHAIN_ARCHIVE"

rm -rf "$INSTALL_DIR"
mkdir -p "$INSTALL_DIR"
tar -xzf "$CORE_ARCHIVE" -C "$INSTALL_DIR"
mkdir -p "$INSTALL_DIR/toolchain"
tar -xzf "$TOOLCHAIN_ARCHIVE" -C "$INSTALL_DIR/toolchain"

if [[ ! -x "$BIN_DIR/ferret" && -x "$INSTALL_DIR/ferret" ]]; then
  mkdir -p "$BIN_DIR"
  mv "$INSTALL_DIR/ferret" "$BIN_DIR/ferret"
fi

if [[ ! -x "$BIN_DIR/ferret" ]]; then
  echo "error: ferret binary was not found at $BIN_DIR/ferret after extraction" >&2
  exit 1
fi

PROFILE_FILE="$(pick_profile_file)"
ensure_path_persisted "$PROFILE_FILE"

if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
  export PATH="$BIN_DIR:$PATH"
fi

echo "Ferret installed to: $INSTALL_DIR"
echo "Binary: $BIN_DIR/ferret"
echo "PATH persisted in: $PROFILE_FILE"
echo "Run: ferret --help"
