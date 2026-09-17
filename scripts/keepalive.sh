#!/usr/bin/env bash
# Keep the installed ai daemon alive. Restarts it when the process is gone.
# Tunables below are plain shell variables (no environment configuration).
set -u

BIN="/usr/local/bin/ai"
STATE="${HOME}/.local/share/ai"
KEEP_DISPLAY=":1"
INTERVAL="30"
PIDFILE="$STATE/ai.pid"
HEARTBEATFILE="$STATE/discord.heartbeat"
# Maximum age of the bot-connectivity timestamp before the bot counts as
# offline while the daemon is alive (REQ-044). A missing file before the
# first Ready is not a failure: it means the gateway never connected yet.
HEARTBEAT_MAX_AGE="300"
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

# discord_online returns success when the bot-connectivity timestamp is fresh.
# Missing file = gateway never connected yet (not a failure, e.g. CLI-only
# daemon or first boot before Ready); stale file = Discord socket dead while
# the daemon lives, so the daemon must be restarted. The gateway's own
# watchdog tries Close+Open reopen first; keepalive is the outer backstop.
discord_online() {
  [[ -f "$HEARTBEATFILE" ]] || return 0
  local now mtime age
  now="$(date +%s)"
  mtime="$(stat -c %Y "$HEARTBEATFILE" 2>/dev/null || echo 0)"
  [[ "$mtime" -gt 0 ]] || return 0
  age=$((now - mtime))
  [[ "$age" -le "$HEARTBEAT_MAX_AGE" ]]
}

restart_daemon() {
  local reason="$1"
  log "daemon $reason, restarting"
  rm -f "$PIDFILE"
  DISPLAY="$KEEP_DISPLAY" "$BIN" stop >/dev/null 2>&1 || true
  sleep 2
  DISPLAY="$KEEP_DISPLAY" "$BIN" start >>"$STATE/ai.log" 2>&1 || log "start failed, will retry"
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
    restart_daemon "missing"
  elif ! discord_online; then
    restart_daemon "alive but Discord heartbeat stale"
  fi
  sleep "$INTERVAL"
done
