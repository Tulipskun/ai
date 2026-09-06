#!/usr/bin/env bash
set -euo pipefail

REPO="${AI_REPO:-Tulipskun/ai}"
INSTALL_ROOT="${AI_INSTALL_ROOT:-${HOME}/.local/share/ai}"
VERSION="${AI_VERSION:-latest}"

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
if [[ "$VERSION" == "latest" ]]; then
  BASE_URL="https://github.com/${REPO}/releases/latest/download"
else
  BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
fi

TMP="$(mktemp)"
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
mv -f "$TMP" "$INSTALL_ROOT/ai"
ln -sfn "$INSTALL_ROOT/ai" "$BIN_DIR/ai"

if [[ -n "${PATH:-}" ]]; then
  IFS=: read -r -a PATH_DIRS <<< "$PATH"
  for dir in "${PATH_DIRS[@]}"; do
    [[ -n "$dir" ]] || continue
    candidate="$dir/ai"
    [[ "$candidate" != "$BIN_DIR/ai" ]] || continue
    [[ -L "$candidate" ]] || continue
    [[ "$(readlink "$candidate")" == "$INSTALL_ROOT/ai" ]] || continue
    rm -f "$candidate"
    log "removed stale link: $candidate"
  done
fi

mkdir -p "$INSTALL_ROOT/.config" "$INSTALL_ROOT/.data" "$INSTALL_ROOT/.ai"
if [[ ! -f "$INSTALL_ROOT/.env" ]]; then
  cat > "$INSTALL_ROOT/.env" <<'EOF'
# Add provider/transport settings here, then run: ai start
# AI_CLI_ENABLED=true
# DISCORD_BOT_TOKEN=
# DISCORD_OWNER_ID=
EOF
fi

log "installed: $BIN_DIR/ai"
if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
  log "add this to your shell profile: export PATH=\"$BIN_DIR:\$PATH\""
fi
log "run: ai start"
log "update: ai update"
log "if your shell previously cached another ai path, run: hash -r"
