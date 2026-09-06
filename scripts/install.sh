#!/usr/bin/env bash
set -euo pipefail

REPO="${AI_REPO:-Tulipskun/ai}"
INSTALL_ROOT="${AI_INSTALL_ROOT:-${HOME}/.local/share/ai}"
BIN_DIR="${AI_BIN_DIR:-${HOME}/.local/bin}"
VERSION="${AI_VERSION:-main}"
GO_VERSION="1.25.0"

log() { printf '[ai] %s\n' "$*"; }
die() { printf '[ai] error: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v git >/dev/null 2>&1 || die "git is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) GO_ARCH="amd64" ;;
  aarch64|arm64) GO_ARCH="arm64" ;;
  *) die "unsupported architecture: $ARCH" ;;
esac
case "$OS" in
  linux|darwin) ;;
  *) die "unsupported operating system: $OS" ;;
esac

mkdir -p "$INSTALL_ROOT" "$BIN_DIR"

if [[ ! -d "$INSTALL_ROOT/.git" ]]; then
  if [[ -n "${AI_GIT_URL:-}" ]]; then
    git clone "$AI_GIT_URL" "$INSTALL_ROOT"
  else
    git clone "https://github.com/${REPO}.git" "$INSTALL_ROOT"
  fi
else
  git -C "$INSTALL_ROOT" fetch --tags origin
fi

git -C "$INSTALL_ROOT" checkout --quiet "$VERSION" 2>/dev/null || git -C "$INSTALL_ROOT" checkout --quiet -B "$VERSION" "origin/$VERSION"
git -C "$INSTALL_ROOT" reset --hard --quiet "origin/$VERSION" 2>/dev/null || true

if ! command -v go >/dev/null 2>&1 || [[ "$(go env GOVERSION 2>/dev/null || true)" != go${GO_VERSION}* ]]; then
  GO_ROOT="$INSTALL_ROOT/.toolchain/go"
  GO_BIN="$GO_ROOT/bin/go"
  if [[ ! -x "$GO_BIN" ]]; then
    mkdir -p "$INSTALL_ROOT/.toolchain"
    GO_TARBALL="$INSTALL_ROOT/.toolchain/go.tar.gz"
    log "installing Go ${GO_VERSION} toolchain"
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.${OS}-${GO_ARCH}.tar.gz" -o "$GO_TARBALL"
    rm -rf "$GO_ROOT"
    tar -xzf "$GO_TARBALL" -C "$INSTALL_ROOT/.toolchain"
    mv "$INSTALL_ROOT/.toolchain/go" "$GO_ROOT"
    rm -f "$GO_TARBALL"
  fi
  export PATH="$GO_ROOT/bin:$PATH"
fi

log "building ai"
cd "$INSTALL_ROOT"
go build -trimpath -ldflags "-s -w" -o "$INSTALL_ROOT/ai" ./cmd/ai
chmod 0755 "$INSTALL_ROOT/ai"

ln -sfn "$INSTALL_ROOT/ai" "$BIN_DIR/ai"
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
