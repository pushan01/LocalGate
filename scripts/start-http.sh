#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

BIN="${BIN:-$ROOT_DIR/bin/localgate}"
CONFIG="${CONFIG:-$ROOT_DIR/localgate.yaml}"
BIND="${BIND:-}"
ADMIN_BIND="${ADMIN_BIND:-}"
PUBLIC_DEPLOYMENTS_BIND="${PUBLIC_DEPLOYMENTS_BIND:-}"
PRIVATE_DEPLOYMENTS_BIND="${PRIVATE_DEPLOYMENTS_BIND:-}"
SKIP_BUILD="${SKIP_BUILD:-0}"
DAEMON="${DAEMON:-0}"
RUN_DIR="${RUN_DIR:-$ROOT_DIR/.localgate/run}"
PID_FILE="${PID_FILE:-$RUN_DIR/localgate-http.pid}"
LOG_FILE="${LOG_FILE:-$RUN_DIR/localgate-http.log}"

HTTP_TOKEN_FILE="${LOCALGATE_HTTP_BEARER_TOKEN_FILE:-$ROOT_DIR/.localgate/http-bearer-token}"
if [[ -z "${LOCALGATE_HTTP_BEARER_TOKEN:-}" && -f "$HTTP_TOKEN_FILE" ]]; then
  export LOCALGATE_HTTP_BEARER_TOKEN="$(tr -d '\r\n' < "$HTTP_TOKEN_FILE")"
fi

if [[ "$SKIP_BUILD" != "1" ]]; then
  "$ROOT_DIR/scripts/build.sh"
fi

ARGS=(-config "$CONFIG")
if [[ -n "$BIND" ]]; then
  ARGS+=(-bind "$BIND")
fi
if [[ -n "$ADMIN_BIND" ]]; then
  ARGS+=(-admin-bind "$ADMIN_BIND")
fi
if [[ -n "$PUBLIC_DEPLOYMENTS_BIND" ]]; then
  ARGS+=(-public-deployments-bind "$PUBLIC_DEPLOYMENTS_BIND")
fi
if [[ -n "$PRIVATE_DEPLOYMENTS_BIND" ]]; then
  ARGS+=(-private-deployments-bind "$PRIVATE_DEPLOYMENTS_BIND")
fi

RUNTIME_ENV=(
  "LOCALGATE_RUNTIME_MODE=http_foreground"
  "LOCALGATE_RUNTIME_BIN=$BIN"
  "LOCALGATE_RUNTIME_CONFIG=$CONFIG"
  "LOCALGATE_RUNTIME_BIND=${BIND:-}"
  "LOCALGATE_RUNTIME_ADMIN_BIND=${ADMIN_BIND:-}"
  "LOCALGATE_RUNTIME_PUBLIC_DEPLOYMENT_BIND=${PUBLIC_DEPLOYMENTS_BIND:-}"
  "LOCALGATE_RUNTIME_PRIVATE_DEPLOYMENT_BIND=${PRIVATE_DEPLOYMENTS_BIND:-}"
  "LOCALGATE_RUNTIME_PID_FILE=$PID_FILE"
  "LOCALGATE_RUNTIME_LOG_FILE=$LOG_FILE"
  "LOCALGATE_RUNTIME_RUN_DIR=$RUN_DIR"
)

if [[ "$DAEMON" == "1" ]]; then
  RUNTIME_ENV[0]="LOCALGATE_RUNTIME_MODE=http_daemon"
  mkdir -p "$RUN_DIR" "$(dirname "$PID_FILE")" "$(dirname "$LOG_FILE")"

  if [[ -f "$PID_FILE" ]]; then
    EXISTING_PID="$(cat "$PID_FILE" 2>/dev/null || true)"
    if [[ -n "$EXISTING_PID" ]] && kill -0 "$EXISTING_PID" 2>/dev/null; then
      echo "localgate http already running: pid=$EXISTING_PID pid_file=$PID_FILE" >&2
      exit 1
    fi
    rm -f "$PID_FILE"
  fi

  env "${RUNTIME_ENV[@]}" nohup "$BIN" "${ARGS[@]}" "$@" >>"$LOG_FILE" 2>&1 < /dev/null &
  PID=$!
  echo "$PID" > "$PID_FILE"

  sleep 1
  if kill -0 "$PID" 2>/dev/null; then
    echo "localgate http started in daemon mode"
    echo "pid=$PID"
    echo "pid_file=$PID_FILE"
    echo "log_file=$LOG_FILE"
    exit 0
  fi

  echo "localgate http failed to start; recent logs:" >&2
  tail -n 50 "$LOG_FILE" >&2 || true
  rm -f "$PID_FILE"
  exit 1
fi

exec env "${RUNTIME_ENV[@]}" "$BIN" "${ARGS[@]}" "$@"
