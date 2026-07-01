#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

RUN_DIR="${RUN_DIR:-$ROOT_DIR/.localgate/run}"
PID_FILE="${PID_FILE:-$RUN_DIR/localgate-http.pid}"
STOP_TIMEOUT_SECONDS="${STOP_TIMEOUT_SECONDS:-10}"

if [[ ! -f "$PID_FILE" ]]; then
  echo "localgate http pid file not found: $PID_FILE"
  exit 0
fi

PID="$(cat "$PID_FILE" 2>/dev/null || true)"
if [[ -z "$PID" ]]; then
  echo "localgate http pid file is empty: $PID_FILE"
  rm -f "$PID_FILE"
  exit 0
fi

if ! kill -0 "$PID" 2>/dev/null; then
  echo "localgate http is not running, removing stale pid file: pid=$PID pid_file=$PID_FILE"
  rm -f "$PID_FILE"
  exit 0
fi

kill "$PID"

for ((i = 0; i < STOP_TIMEOUT_SECONDS; i++)); do
  if ! kill -0 "$PID" 2>/dev/null; then
    rm -f "$PID_FILE"
    echo "localgate http stopped: pid=$PID"
    exit 0
  fi
  sleep 1
done

echo "localgate http did not stop within ${STOP_TIMEOUT_SECONDS}s, sending SIGKILL: pid=$PID" >&2
kill -9 "$PID" 2>/dev/null || true
rm -f "$PID_FILE"
echo "localgate http stopped: pid=$PID"
