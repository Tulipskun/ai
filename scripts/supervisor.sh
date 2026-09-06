#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="${APP:-$ROOT/ai}"
PIDFILE="${PIDFILE:-$ROOT/.ai/supervisor.pid}"
LOGFILE="${LOGFILE:-$ROOT/.ai/supervisor.log}"
SUPERVISOR="$ROOT/scripts/supervisor.sh"

mkdir -p "$(dirname "$PIDFILE")"

stop_app() {
  if [[ -f "$PIDFILE" ]]; then
    pid="$(cat "$PIDFILE" 2>/dev/null || true)"
    if [[ -n "${pid:-}" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      for _ in {1..50}; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.1
      done
      kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$PIDFILE"
  fi
}

start_app() {
  nohup "$APP" start >>"$LOGFILE" 2>&1 &
  echo $! > "$PIDFILE"
}

update_self() {
  cd "$ROOT"
  git pull --ff-only
  go build -o "$APP" ./cmd/ai
  stop_app
  start_app
  nohup "$SUPERVISOR" run >>"$LOGFILE" 2>&1 &
  rm -f "$PIDFILE"
  exit 0
}

run_supervisor() {
  trap 'stop_app' EXIT INT TERM
  while true; do
    if [[ ! -x "$APP" ]]; then
      go build -o "$APP" ./cmd/ai
    fi
    if [[ ! -f "$PIDFILE" ]] || ! kill -0 "$(cat "$PIDFILE" 2>/dev/null)" 2>/dev/null; then
      start_app
    fi
    sleep "${SUPERVISOR_INTERVAL:-5}"
  done
}

case "${1:-run}" in
  run) run_supervisor ;;
  update) update_self ;;
  stop) stop_app ;;
  *) echo "usage: $0 {run|update|stop}" >&2; exit 2 ;;
esac
