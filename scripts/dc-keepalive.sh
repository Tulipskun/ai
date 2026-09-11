#!/usr/bin/env bash
# Keep the pinned Desktop Commander remote session alive.
# Restarts /usr/local/bin/dc-remote.sh when the process disappears, and caps
# its log so a reconnect storm can never fill the disk again.
# Env: DC_LAUNCHER, DC_LOG, DC_ARCHIVE_DIR, KEEPALIVE_INTERVAL.
set -u

LAUNCHER="${DC_LAUNCHER:-/usr/local/bin/dc-remote.sh}"
LOG="${DC_LOG:-/root/dc-startup.log}"
ARCHIVE="${DC_ARCHIVE_DIR:-/root/logs}"
INTERVAL="${KEEPALIVE_INTERVAL:-30}"
LOCKDIR="/root/.local/share/ai/dc-keepalive.lock"
LOG_MAX_BYTES="${DC_LOG_MAX_BYTES:-2097152}"
# Only restart if the launcher itself is sane; never fight a manual stop within
# this many seconds of the last keeper action.
PATTERN="dc-runtime/node_modules/@wonderwhy-er/desktop-commander/dist/index.js remote"

log() { printf '[dc-keepalive] %s\n' "$*"; }

dc_alive() {
  ps -eo args= 2>/dev/null | tr '\0' ' ' | grep -q "$PATTERN"
}

rotate_log() {
  [ -f "$LOG" ] || return 0
  local size
  size=$(wc -c < "$LOG" 2>/dev/null || echo 0)
  [ "$size" -gt "$LOG_MAX_BYTES" ] || return 0
  mkdir -p "$ARCHIVE"
  local stamp archive tail
  stamp=$(date +%Y%m%d-%H%M%S)
  archive="$ARCHIVE/dc-startup.log.$stamp"
  tail -n 2000 "$LOG" > "$archive.partial" 2>/dev/null || : > "$archive.partial"
  mv -f "$archive.partial" "$archive"
  : > "$LOG"
  log "log capped at ${size} bytes -> $archive"
  # keep only the 10 newest archives
  ls -1t "$ARCHIVE"/dc-startup.log.* 2>/dev/null | tail -n +11 | while read -r f; do rm -f "$f"; done
}

if ! mkdir -p "$(dirname "$LOCKDIR")" 2>/dev/null; then :; fi
if ! mkdir "$LOCKDIR" 2>/dev/null; then
  log "another keeper is already running"
  exit 0
fi
trap 'rmdir "$LOCKDIR" 2>/dev/null || true' EXIT

log "watching DC every ${INTERVAL}s"
while true; do
  rotate_log
  if ! dc_alive; then
    if [ -x "$LAUNCHER" ]; then
      log "DC missing, starting"
      setsid nohup "$LAUNCHER" >> "$LOG" 2>&1 < /dev/null &
      sleep 5
    else
      log "launcher $LAUNCHER missing or not executable; skipping"
    fi
  fi
  sleep "$INTERVAL"
done
