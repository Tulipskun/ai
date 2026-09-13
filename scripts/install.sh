#!/usr/bin/env bash
set -euo pipefail

REPO="Tulipskun/ai"
REF="latest"
INSTALL_ROOT="${HOME}/.local/share/ai"

log() { printf '[ai] %s\n' "$*"; }
die() { printf '[ai] error: %s\n' "$*" >&2; exit 1; }

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  aarch64|arm64) ARCH="arm64" ;;
  *) die "unsupported architecture: $ARCH (only arm64 is supported)" ;;
esac
case "$OS" in
  linux) ;;
  *) die "unsupported operating system: $OS (only linux is supported)" ;;
esac

if [[ -d /usr/local/bin && -w /usr/local/bin ]]; then
  BIN_DIR="/usr/local/bin"
else
  BIN_DIR="${HOME}/.local/bin"
fi

mkdir -p "$INSTALL_ROOT" "$BIN_DIR"
ASSET="ai-${OS}-${ARCH}"

TMPDIR="$(mktemp -d)"
BINARY_TMP="$TMPDIR/$ASSET"
CHECKSUMS="$TMPDIR/checksums.txt"
cleanup() { rm -rf "$TMPDIR" "${BIN_DIR}/.ai-install."* 2>/dev/null || true; }
trap cleanup EXIT

verify_checksum() {
  local file="$1" sums="$2" name="$3" expected actual
  expected="$(awk -v name="$name" '$2 == name { print $1; exit }' "$sums")"
  [[ -n "$expected" ]] || die "checksum entry not found for ${name}"
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$file" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  else
    die "shasum or sha256sum is required"
  fi
  [[ "$actual" == "$expected" ]] || die "checksum verification failed for ${name}"
}

if command -v gh >/dev/null 2>&1; then
  log "downloading ${ASSET} (release: ${REF}) with gh"
  if [[ -z "$REF" || "$REF" == "latest" ]]; then
    gh release download --repo "$REPO" --pattern "$ASSET" --pattern "checksums.txt" --dir "$TMPDIR" --clobber
  else
    gh release download "$REF" --repo "$REPO" --pattern "$ASSET" --pattern "checksums.txt" --dir "$TMPDIR" --clobber
  fi
else
  command -v curl >/dev/null 2>&1 || die "gh CLI (recommended) or curl is required; install gh from https://cli.github.com and run: gh auth login"
  command -v shasum >/dev/null 2>&1 || command -v sha256sum >/dev/null 2>&1 || die "shasum or sha256sum is required"
  if [[ -z "$REF" || "$REF" == "latest" ]]; then
    BASE_URL="https://github.com/${REPO}/releases/latest/download"
  else
    BASE_URL="https://github.com/${REPO}/releases/download/${REF}"
  fi
  AUTH_ARGS=()
  if [[ -n "${GH_TOKEN:-}" ]]; then
    AUTH_ARGS=(-H "Authorization: Bearer ${GH_TOKEN}")
  elif [[ -n "${GITHUB_TOKEN:-}" ]]; then
    AUTH_ARGS=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
  fi
  if [[ "${REPO}" == "Tulipskun/ai" && ${#AUTH_ARGS[@]} -eq 0 ]]; then
    log "warning: ${REPO} is private; curl download needs GH_TOKEN/GITHUB_TOKEN or install gh + 'gh auth login'"
  fi
  log "downloading ${ASSET} (release: ${REF}) with curl"
  curl -fsSL "${AUTH_ARGS[@]}" "${BASE_URL}/${ASSET}" -o "$BINARY_TMP"
  curl -fsSL "${AUTH_ARGS[@]}" "${BASE_URL}/checksums.txt" -o "$CHECKSUMS"
fi

verify_checksum "$BINARY_TMP" "$CHECKSUMS" "$ASSET"

TMP="$(mktemp "${BIN_DIR}/.ai-install.XXXXXX")"
cp -f "$BINARY_TMP" "$TMP"
chmod 0755 "$TMP"
mv -f "$TMP" "$BIN_DIR/ai"

mkdir -p "$INSTALL_ROOT/config" "$INSTALL_ROOT/data/browser"

log "installed: $BIN_DIR/ai"
if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
  log "add this to your shell profile: export PATH=\"$BIN_DIR:\$PATH\""
fi
log "runtime directory: $INSTALL_ROOT"
log "run: ai start"
log "update: ai update (uses: gh release download --repo $REPO)"
log "pin a release with: ai update <tag> (default: latest)"
log "if your shell previously cached another ai path, run: hash -r"
