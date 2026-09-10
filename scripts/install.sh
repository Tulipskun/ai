#!/usr/bin/env bash
set -euo pipefail

REPO="${AI_REPO:-kyomu53n-group/ai}"
REF="${AI_VERSION:-main}"
INSTALL_ROOT="${AI_INSTALL_ROOT:-${HOME}/.local/share/ai}"

log() { printf '[ai] %s\n' "$*"; }
die() { printf '[ai] error: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v shasum >/dev/null 2>&1 || command -v sha256sum >/dev/null 2>&1 || die "shasum or sha256sum is required"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "unsupported architecture: $ARCH" ;;
esac
case "$OS" in
  linux|darwin) ;;
  *) die "unsupported operating system: $OS" ;;
esac

if [[ -n "${AI_BIN_DIR:-}" ]]; then
  BIN_DIR="$AI_BIN_DIR"
elif [[ -d /usr/local/bin && -w /usr/local/bin ]]; then
  BIN_DIR="/usr/local/bin"
else
  BIN_DIR="${HOME}/.local/bin"
fi

mkdir -p "$INSTALL_ROOT" "$BIN_DIR"
ASSET="ai-${OS}-${ARCH}"
BASE_URL="https://gitlab.com/${REPO}/-/raw/${REF}/bin"

TMP="$(mktemp "${BIN_DIR}/.ai-install.XXXXXX")"
CHECKSUMS="$(mktemp)"
cleanup() { rm -f "$TMP" "$CHECKSUMS"; }
trap cleanup EXIT

log "downloading ${ASSET}"
curl -fsSL "${BASE_URL}/${ASSET}" -o "$TMP"
curl -fsSL "${BASE_URL}/checksums.txt" -o "$CHECKSUMS"
EXPECTED="$(awk -v name="$ASSET" '$2 == name { print $1; exit }' "$CHECKSUMS")"
[[ -n "$EXPECTED" ]] || die "checksum entry not found for ${ASSET}"
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "$TMP" | awk '{print $1}')"
else
  ACTUAL="$(shasum -a 256 "$TMP" | awk '{print $1}')"
fi
[[ "$ACTUAL" == "$EXPECTED" ]] || die "checksum verification failed"

chmod 0755 "$TMP"
mv -f "$TMP" "$BIN_DIR/ai"

mkdir -p "$INSTALL_ROOT/config" "$INSTALL_ROOT/data/browser"

log "installed: $BIN_DIR/ai"
if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
  log "add this to your shell profile: export PATH=\"$BIN_DIR:\$PATH\""
fi
log "runtime directory: $INSTALL_ROOT"
log "run: ai start"
log "update: ai update"
log "if your shell previously cached another ai path, run: hash -r"
