#!/usr/bin/env bash
# Keep the installed ai daemon alive. Restarts it when the process is gone.
# Tunables below are plain shell variables (no environment configuration).
set -u

BIN="/usr/local/bin/ai"
STATE="${HOME}/.local/share/ai"
KEEP_DISPLAY=":1"
INTERVAL="30"
PIDFILE="$STATE/ai.pid"
LOCKDIR="$STATE/keepalive.lock"

log() { printf '[keepalive] %s\n' "$*"; }

is_ai_daemon() {
  local pid="${1:-}"
  [[ -n "$pid" ]] || return 1
  [[ -f "/proc/$pid/cmdline" ]] || return 1
  tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null | grep -q "ai daemon" || return 1
}

daemon_alive() {
  local pid
  pid="$(cat "$PIDFILE" 2>/dev/null || true)"
  [[ -n "$pid" ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  is_ai_daemon "$pid"
}

if ! mkdir "$LOCKDIR" 2>/dev/null; then
  log "another keeper is already running"
  exit 0
fi
trap 'rmdir "$LOCKDIR" 2>/dev/null || true' EXIT
mkdir -p "$STATE"

log "watching $PIDFILE every ${INTERVAL}s"
while true; do
  if ! daemon_alive; then
    log "daemon missing, starting"
    rm -f "$PIDFILE"
    DISPLAY="$KEEP_DISPLAY" "$BIN" start >>"$STATE/ai.log" 2>&1 || log "start failed, will retry"
  fi
  sleep "$INTERVAL"
done
