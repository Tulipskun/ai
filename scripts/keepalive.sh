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
# Blue-green probation shadow files (REQ-043, CHANGE-053): while green proves
# itself, the green standby writes ai.pid.green + discord.heartbeat.green and
# tracks the handover in update.bluegreen.json. The keeper must not restart
# either daemon during probation; the updater owns the handover end to end.
GREENPIDFILE="$STATE/ai.pid.green"
GREENHEARTBEATFILE="$STATE/discord.heartbeat.green"
PHASEFILE="$STATE/update.bluegreen.json"
# A handover older than this is expired: the updater already rolled back or
# finished, so normal supervision resumes.
PHASE_MAX_AGE="900"
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

# bluegreen_handover_active mirrors isBlueGreenHandoverActive in
# cmd/ai/update_bluegreen.go: phase file exists, cutover not marked done, and
# not expired. While it holds, the updater owns the handover and the keeper
# must skip all restart branches (never touch blue or green).
bluegreen_handover_active() {
  [[ -f "$PHASEFILE" ]] || return 1
  grep -q '"cutover_done"[[:space:]]*:[[:space:]]*true' "$PHASEFILE" 2>/dev/null && return 1
  local now mtime age
  now="$(date +%s)"
  mtime="$(stat -c %Y "$PHASEFILE" 2>/dev/null || echo 0)"
  [[ "$mtime" -gt 0 ]] || return 1
  age=$((now - mtime))
  [[ "$age" -le "$PHASE_MAX_AGE" ]]
}

# green_standby_alive watches the probation pid during handover: green must
# stay up while the updater runs its health gates. A dead green here is only
# supervisory signal for the updater's rollback path; the keeper still must
# not restart anything itself while the handover is active.
green_standby_alive() {
  local pid
  pid="$(cat "$GREENPIDFILE" 2>/dev/null || true)"
  [[ -n "$pid" ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  is_ai_daemon "$pid"
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
  if bluegreen_handover_active; then
    if green_standby_alive; then
      log "blue-green handover in progress, green probation alive; skipping restart"
    else
      log "blue-green handover in progress, green standby not alive; leaving rollback to updater"
    fi
  elif ! daemon_alive; then
    restart_daemon "missing"
  elif ! discord_online; then
    restart_daemon "alive but Discord heartbeat stale"
  fi
  sleep "$INTERVAL"
done
