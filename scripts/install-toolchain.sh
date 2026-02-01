#!/usr/bin/env bash
set -euo pipefail

os=$(uname -s)

if [[ "$os" == "Darwin" ]]; then
  if xcode-select -p >/dev/null 2>&1; then
    echo "Xcode Command Line Tools already installed."
    exit 0
  fi
  echo "Installing Xcode Command Line Tools..."
  xcode-select --install
  exit 0
fi

if [[ "$os" != "Linux" ]]; then
  echo "Unsupported OS: $os"
  exit 1
fi

SUDO=""
if [[ "$(id -u)" -ne 0 ]]; then
  if command -v sudo >/dev/null 2>&1; then
    SUDO="sudo"
  elif command -v doas >/dev/null 2>&1; then
    SUDO="doas"
  else
    echo "Need root privileges to install packages (sudo/doas not found)."
    exit 1
  fi
fi

if command -v apt-get >/dev/null 2>&1; then
  $SUDO apt-get update
  $SUDO apt-get install -y build-essential
  exit 0
fi

if command -v dnf >/dev/null 2>&1; then
  $SUDO dnf install -y gcc gcc-c++ binutils make
  exit 0
fi

if command -v yum >/dev/null 2>&1; then
  $SUDO yum install -y gcc gcc-c++ binutils make
  exit 0
fi

if command -v pacman >/dev/null 2>&1; then
  $SUDO pacman -S --needed --noconfirm base-devel
  exit 0
fi

if command -v zypper >/dev/null 2>&1; then
  $SUDO zypper install -y gcc gcc-c++ binutils make
  exit 0
fi

if command -v apk >/dev/null 2>&1; then
  $SUDO apk add build-base binutils
  exit 0
fi

echo "Unsupported Linux distribution: no known package manager found."
exit 1
