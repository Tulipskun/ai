#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="${APP:-$ROOT/ai}"
STATE_DIR="${STATE_DIR:-$ROOT/.ai}"
SUPERVISOR_PIDFILE="${SUPERVISOR_PIDFILE:-$STATE_DIR/supervisor.pid}"
APP_PIDFILE="${APP_PIDFILE:-$STATE_DIR/ai.pid}"
LOGFILE="${LOGFILE:-$STATE_DIR/supervisor.log}"
SUPERVISOR="$ROOT/scripts/supervisor.sh"
mkdir -p "$STATE_DIR"

read_pid() { [[ -f "$1" ]] || return 1; cat "$1" 2>/dev/null; }
stop_pid() {
  local pid="${1:-}"
  [[ -n "$pid" ]] || return 0
  kill -0 "$pid" 2>/dev/null || return 0
  kill "$pid" 2>/dev/null || true
  for _ in {1..50}; do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done
  kill -9 "$pid" 2>/dev/null || true
}
stop_app() { local pid; pid="$(read_pid "$APP_PIDFILE" || true)"; stop_pid "$pid"; rm -f "$APP_PIDFILE"; }
start_app() {
  # `ai start` backgrounds the real daemon and writes the daemon PID itself.
  # Do not overwrite ai.pid with the short-lived supervisor child PID.
  "$APP" start >>"$LOGFILE" 2>&1 || true
}
start_supervisor() { nohup "$SUPERVISOR" run >>"$LOGFILE" 2>&1 & echo $! > "$SUPERVISOR_PIDFILE"; }

update_self() {
  cd "$ROOT"
  git pull --ff-only
  go build -o "$APP" ./cmd/ai
  old_supervisor="$(read_pid "$SUPERVISOR_PIDFILE" || true)"
  stop_app
  if [[ -n "$old_supervisor" && "$old_supervisor" != "$$" ]]; then stop_pid "$old_supervisor"; fi
  rm -f "$SUPERVISOR_PIDFILE"
  start_supervisor
  exit 0
}

run_supervisor() {
  echo $$ > "$SUPERVISOR_PIDFILE"
  trap 'rm -f "$SUPERVISOR_PIDFILE"; stop_app' EXIT INT TERM
  while true; do
    if [[ ! -x "$APP" ]]; then go build -o "$APP" ./cmd/ai; fi
    pid="$(read_pid "$APP_PIDFILE" || true)"
    if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$APP_PIDFILE"
      start_app
    fi
    sleep "${SUPERVISOR_INTERVAL:-5}"
  done
}

case "${1:-run}" in
  run) run_supervisor ;;
  update) update_self ;;
  stop) stop_app; supervisor_pid="$(read_pid "$SUPERVISOR_PIDFILE" || true)"; [[ "$supervisor_pid" == "$$" ]] || stop_pid "$supervisor_pid"; rm -f "$SUPERVISOR_PIDFILE" ;;
  *) echo "usage: $0 {run|update|stop}" >&2; exit 2 ;;
esac
