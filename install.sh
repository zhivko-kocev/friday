#!/usr/bin/env bash
set -euo pipefail

REPO="zhivko-kocev/friday"
BIN_NAME="friday"
INSTALL_DIR="${FRIDAY_INSTALL_DIR:-/usr/local/bin}"

# Detect OS / arch
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$OS" in
  linux|darwin) ;;
  mingw*|msys*|cygwin*|windows*)
    cat >&2 <<EOF
error: install.sh supports linux/darwin only.

Windows install options:
  PowerShell:   iwr -useb https://raw.githubusercontent.com/${REPO}/master/install.ps1 | iex
  Go toolchain: go install github.com/${REPO}/cmd/friday@latest
  Manual:       https://github.com/${REPO}/releases/latest
EOF
    exit 1
    ;;
  *)
    echo "error: unsupported OS $OS" >&2
    echo "       try: go install github.com/${REPO}/cmd/friday@latest" >&2
    exit 1
    ;;
esac
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "error: unsupported architecture $ARCH" >&2
    exit 1
    ;;
esac

# Resolve latest tag
if command -v curl &>/dev/null; then
  FETCH="curl -fsSL"
elif command -v wget &>/dev/null; then
  FETCH="wget -qO-"
else
  echo "error: curl or wget required" >&2
  exit 1
fi

LATEST=$($FETCH "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')

if [[ -z "$LATEST" ]]; then
  echo "error: could not determine latest release" >&2
  exit 1
fi

TARBALL="${BIN_NAME}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${LATEST}/${TARBALL}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "  downloading friday ${LATEST} (${OS}/${ARCH})..."
$FETCH "$URL" > "${TMP}/${TARBALL}"
tar -xzf "${TMP}/${TARBALL}" -C "$TMP"

if [[ ! -f "${TMP}/${BIN_NAME}" ]]; then
  echo "error: binary not found in archive" >&2
  exit 1
fi

chmod +x "${TMP}/${BIN_NAME}"

if [[ -w "$INSTALL_DIR" ]]; then
  mv "${TMP}/${BIN_NAME}" "${INSTALL_DIR}/${BIN_NAME}"
else
  sudo mv "${TMP}/${BIN_NAME}" "${INSTALL_DIR}/${BIN_NAME}"
fi

echo "  friday ${LATEST} installed to ${INSTALL_DIR}/${BIN_NAME}"
echo ""
echo "  next steps:"
echo "    friday init                                  # scaffold a store, or clone an existing config repo"
echo "    friday push                                  # apply config to ~/.claude, ~/.codex, etc."
