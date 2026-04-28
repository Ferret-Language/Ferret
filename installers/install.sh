#!/usr/bin/env bash
set -euo pipefail

REPO="${FERRET_REPO:-Ferret-Language/Ferret}"
VERSION="${1:-latest}"
INSTALL_DIR="${FERRET_INSTALL_DIR:-$HOME/.local/ferret}"
BIN_DIR="$INSTALL_DIR/core/bin"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required command not found: $1" >&2
    exit 1
  fi
}

detect_os() {
  case "$(uname -s)" in
    Linux)  echo "linux" ;;
    Darwin) echo "macos" ;;
    *)
      echo "error: unsupported OS: $(uname -s)" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)   echo "amd64" ;;
    arm64|aarch64)  echo "arm64" ;;
    *)
      echo "error: unsupported architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

download_to() {
  local url="$1"
  local out="$2"

  if command -v curl >/dev/null 2>&1; then
    curl \
      --fail \
      --progress-bar \
      --show-error \
      --location \
      --retry 3 \
      --retry-delay 1 \
      --connect-timeout 20 \
      --max-time 600 \
      -H "User-Agent: ferret-installer" \
      -o "$out" \
      "$url"
    return
  fi

  if command -v wget >/dev/null 2>&1; then
    wget \
      --show-progress \
      --progress=bar:force \
      --tries=3 \
      --timeout=20 \
      --user-agent="ferret-installer" \
      -O "$out" \
      "$url"
    return
  fi

  echo "error: either curl or wget is required" >&2
  exit 1#!/usr/bin/env bash
  set -euo pipefail
  
  REPO="${FERRET_REPO:-Ferret-Language/Ferret}"
  VERSION="${1:-latest}"
  INSTALL_DIR="${FERRET_INSTALL_DIR:-$HOME/.local/ferret}"
  BIN_DIR="$INSTALL_DIR/core/bin"
  SHOW_PROGRESS="${FERRET_PROGRESS:-1}"
  
  require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
      echo "error: required command not found: $1" >&2
      exit 1
    fi
  }
  
  detect_os() {
    case "$(uname -s)" in
      Linux)  echo "linux" ;;
      Darwin) echo "macos" ;;
      *)
        echo "error: unsupported OS: $(uname -s)" >&2
        exit 1
        ;;
    esac
  }
  
  detect_arch() {
    case "$(uname -m)" in
      x86_64|amd64)   echo "amd64" ;;
      arm64|aarch64)  echo "arm64" ;;
      *)
        echo "error: unsupported architecture: $(uname -m)" >&2
        exit 1
        ;;
    esac
  }
  
  download_to() {
    local url="$1"
    local out="$2"
  
    local CURL_FLAGS=""
    local WGET_FLAGS=""
  
    if [[ "$SHOW_PROGRESS" == "1" ]]; then
      CURL_FLAGS="--progress-bar"
      WGET_FLAGS="--show-progress --progress=bar:force"
    else
      CURL_FLAGS="--silent"
      WGET_FLAGS="-q"
    fi
  
    if command -v curl >/dev/null 2>&1; then
      curl \
        --fail \
        --location \
        $CURL_FLAGS \
        --show-error \
        --retry 3 \
        --retry-delay 1 \
        --connect-timeout 20 \
        --max-time 600 \
        -H "User-Agent: ferret-installer" \
        -o "$out" \
        "$url"
      return
    fi
  
    if command -v wget >/dev/null 2>&1; then
      wget \
        $WGET_FLAGS \
        --tries=3 \
        --timeout=20 \
        --user-agent="ferret-installer" \
        -O "$out" \
        "$url"
      return
    fi
  
    echo "error: either curl or wget is required" >&2
    exit 1
  }
  
  pick_profile_file() {
    local shell_name os_name
    shell_name="$(basename "${SHELL:-}")"
    os_name="$(uname -s)"
  
    case "$shell_name" in
      zsh)
        echo "$HOME/.zshrc"
        ;;
      bash)
        if [[ "$os_name" == "Darwin" ]]; then
          echo "$HOME/.bash_profile"
        else
          echo "$HOME/.bashrc"
        fi
        ;;
      fish)
        echo "$HOME/.config/fish/config.fish"
        ;;
      *)
        echo "$HOME/.profile"
        ;;
    esac
  }
  
  ensure_path_persisted() {
    local profile_file="$1"
  
    mkdir -p "$(dirname "$profile_file")"
    touch "$profile_file"
  
    if [[ "$profile_file" == *"/config.fish" ]]; then
      local fish_line="fish_add_path \"$BIN_DIR\""
      if ! grep -Fq "$fish_line" "$profile_file"; then
        {
          echo ""
          echo "# Added by Ferret installer"
          echo "$fish_line"
        } >> "$profile_file"
      fi
      return
    fi
  
    local line="export PATH=\"$BIN_DIR:\$PATH\""
    if ! grep -Fq "$line" "$profile_file"; then
      {
        echo ""
        echo "# Added by Ferret installer"
        echo "$line"
      } >> "$profile_file"
    fi
  }
  
  build_asset_url() {
    local version="$1"
    local asset_name="$2"
  
    if [[ "$version" == "latest" ]]; then
      echo "https://github.com/${REPO}/releases/latest/download/${asset_name}"
    else
      echo "https://github.com/${REPO}/releases/download/${version}/${asset_name}"
    fi
  }
  
  require_cmd tar
  require_cmd grep
  require_cmd mv
  require_cmd rm
  require_cmd mkdir
  require_cmd mktemp
  
  OS="$(detect_os)"
  ARCH="$(detect_arch)"
  
  CORE_ASSET="ferret-${OS}-${ARCH}.tar.gz"
  TOOLCHAIN_ASSET="ferret-toolchain-${OS}-${ARCH}.tar.gz"
  
  CORE_URL="$(build_asset_url "$VERSION" "$CORE_ASSET")"
  TOOLCHAIN_URL="$(build_asset_url "$VERSION" "$TOOLCHAIN_ASSET")"
  
  TMP_DIR="$(mktemp -d)"
  trap 'rm -rf "$TMP_DIR"' EXIT
  
  CORE_ARCHIVE="$TMP_DIR/$CORE_ASSET"
  TOOLCHAIN_ARCHIVE="$TMP_DIR/$TOOLCHAIN_ASSET"
  STAGE_DIR="$TMP_DIR/install"
  
  mkdir -p "$STAGE_DIR/core" "$STAGE_DIR/toolchain"
  
  echo "Downloading core: $CORE_URL"
  download_to "$CORE_URL" "$CORE_ARCHIVE"
  
  echo "Downloading toolchain: $TOOLCHAIN_URL"
  download_to "$TOOLCHAIN_URL" "$TOOLCHAIN_ARCHIVE"
  
  tar -xzf "$CORE_ARCHIVE" -C "$STAGE_DIR/core"
  tar -xzf "$TOOLCHAIN_ARCHIVE" -C "$STAGE_DIR/toolchain"
  
  if [[ ! -x "$STAGE_DIR/core/bin/ferret" && -x "$STAGE_DIR/core/ferret" ]]; then
    mkdir -p "$STAGE_DIR/core/bin"
    mv "$STAGE_DIR/core/ferret" "$STAGE_DIR/core/bin/ferret"
  fi
  
  if [[ ! -x "$STAGE_DIR/core/bin/ferret" ]]; then
    echo "error: ferret binary was not found at core/bin/ferret after extraction" >&2
    exit 1
  fi
  
  rm -rf "$INSTALL_DIR"
  mkdir -p "$(dirname "$INSTALL_DIR")"
  mv "$STAGE_DIR" "$INSTALL_DIR"
  
  PROFILE_FILE="$(pick_profile_file)"
  ensure_path_persisted "$PROFILE_FILE"
  
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) export PATH="$BIN_DIR:$PATH" ;;
  esac
  
  echo ""
  echo "Ferret installed to: $INSTALL_DIR"
  echo "Binary: $BIN_DIR/ferret"
  echo "PATH persisted in: $PROFILE_FILE"
  echo ""
  echo "Run: ferret --help"
}

pick_profile_file() {
  local shell_name os_name
  shell_name="$(basename "${SHELL:-}")"
  os_name="$(uname -s)"

  case "$shell_name" in
    zsh)
      echo "$HOME/.zshrc"
      ;;
    bash)
      if [[ "$os_name" == "Darwin" ]]; then
        echo "$HOME/.bash_profile"
      else
        echo "$HOME/.bashrc"
      fi
      ;;
    fish)
      echo "$HOME/.config/fish/config.fish"
      ;;
    *)
      echo "$HOME/.profile"
      ;;
  esac
}

ensure_path_persisted() {
  local profile_file="$1"

  mkdir -p "$(dirname "$profile_file")"
  touch "$profile_file"

  if [[ "$profile_file" == *"/config.fish" ]]; then
    local fish_line="fish_add_path \"$BIN_DIR\""
    if ! grep -Fq "$fish_line" "$profile_file"; then
      {
        echo ""
        echo "# Added by Ferret installer"
        echo "$fish_line"
      } >> "$profile_file"
    fi
    return
  fi

  local line="export PATH=\"$BIN_DIR:\$PATH\""
  if ! grep -Fq "$line" "$profile_file"; then
    {
      echo ""
      echo "# Added by Ferret installer"
      echo "$line"
    } >> "$profile_file"
  fi
}

build_asset_url() {
  local version="$1"
  local asset_name="$2"

  if [[ "$version" == "latest" ]]; then
    echo "https://github.com/${REPO}/releases/latest/download/${asset_name}"
  else
    echo "https://github.com/${REPO}/releases/download/${version}/${asset_name}"
  fi
}

require_cmd tar
require_cmd grep
require_cmd mv
require_cmd rm
require_cmd mkdir
require_cmd mktemp

OS="$(detect_os)"
ARCH="$(detect_arch)"

CORE_ASSET="ferret-${OS}-${ARCH}.tar.gz"
TOOLCHAIN_ASSET="ferret-toolchain-${OS}-${ARCH}.tar.gz"

CORE_URL="$(build_asset_url "$VERSION" "$CORE_ASSET")"
TOOLCHAIN_URL="$(build_asset_url "$VERSION" "$TOOLCHAIN_ASSET")"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

CORE_ARCHIVE="$TMP_DIR/$CORE_ASSET"
TOOLCHAIN_ARCHIVE="$TMP_DIR/$TOOLCHAIN_ASSET"
STAGE_DIR="$TMP_DIR/install"

mkdir -p "$STAGE_DIR/core" "$STAGE_DIR/toolchain"

echo "Downloading $CORE_URL"
download_to "$CORE_URL" "$CORE_ARCHIVE"

echo "Downloading $TOOLCHAIN_URL"
download_to "$TOOLCHAIN_URL" "$TOOLCHAIN_ARCHIVE"

tar -xzf "$CORE_ARCHIVE" -C "$STAGE_DIR/core"
tar -xzf "$TOOLCHAIN_ARCHIVE" -C "$STAGE_DIR/toolchain"

if [[ ! -x "$STAGE_DIR/core/bin/ferret" && -x "$STAGE_DIR/core/ferret" ]]; then
  mkdir -p "$STAGE_DIR/core/bin"
  mv "$STAGE_DIR/core/ferret" "$STAGE_DIR/core/bin/ferret"
fi

if [[ ! -x "$STAGE_DIR/core/bin/ferret" ]]; then
  echo "error: ferret binary was not found at core/bin/ferret after extraction" >&2
  exit 1
fi

rm -rf "$INSTALL_DIR"
mkdir -p "$(dirname "$INSTALL_DIR")"
mv "$STAGE_DIR" "$INSTALL_DIR"

PROFILE_FILE="$(pick_profile_file)"
ensure_path_persisted "$PROFILE_FILE"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) export PATH="$BIN_DIR:$PATH" ;;
esac

echo "Ferret installed to: $INSTALL_DIR"
echo "Binary: $BIN_DIR/ferret"
echo "PATH persisted in: $PROFILE_FILE"
echo "Run: ferret --help"
