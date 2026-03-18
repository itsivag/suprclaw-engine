#!/bin/sh
set -e

REPO="itsivag/suprclaw-engine"
BINARY="suprclaw"
TAG="latest"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  linux)  ;;
  darwin) ;;
  *) echo "Unsupported OS: $OS" && exit 1 ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)                ARCH="amd64" ;;
  aarch64|arm64)         ARCH="arm64" ;;
  armv7*|armv6*|armv5*)  ARCH="arm" ;;
  arm*)                  ARCH="arm" ;;
  riscv64)               ARCH="riscv64" ;;
  mipsel|mips*le*)       ARCH="mipsle" ;;
  loongarch64)           ARCH="loong64" ;;
  *) echo "Unsupported architecture: $ARCH" && exit 1 ;;
esac

FILENAME="${BINARY}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${FILENAME}"

# Pick install dir — prefer /usr/local/bin if writable, fall back to ~/.local/bin
if [ -w /usr/local/bin ]; then
  INSTALL_DIR="/usr/local/bin"
elif [ "$(id -u)" = "0" ]; then
  INSTALL_DIR="/usr/local/bin"
  mkdir -p "$INSTALL_DIR"
else
  INSTALL_DIR="$HOME/.local/bin"
  mkdir -p "$INSTALL_DIR"
fi

echo "Detected: ${OS}/${ARCH}"
echo "Downloading ${FILENAME}..."

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$URL" -o "$TMP/$FILENAME"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$TMP/$FILENAME" "$URL"
else
  echo "Error: curl or wget is required" && exit 1
fi

tar -xzf "$TMP/$FILENAME" -C "$TMP"
install -m 755 "$TMP/${BINARY}-${OS}-${ARCH}" "$INSTALL_DIR/$BINARY"

echo "Installed: $INSTALL_DIR/$BINARY"
"$INSTALL_DIR/$BINARY" --version 2>/dev/null || true
