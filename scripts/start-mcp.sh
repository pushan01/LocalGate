#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

BIN="${BIN:-$ROOT_DIR/bin/localgate}"
CONFIG="${CONFIG:-$ROOT_DIR/localgate.yaml}"
SKIP_BUILD="${SKIP_BUILD:-0}"
DAEMON="${DAEMON:-0}"

HTTP_TOKEN_FILE="${LOCALGATE_HTTP_BEARER_TOKEN_FILE:-$ROOT_DIR/.localgate/http-bearer-token}"
if [[ -z "${LOCALGATE_HTTP_BEARER_TOKEN:-}" && -f "$HTTP_TOKEN_FILE" ]]; then
  export LOCALGATE_HTTP_BEARER_TOKEN="$(tr -d '\r\n' < "$HTTP_TOKEN_FILE")"
fi

if [[ "$DAEMON" == "1" ]]; then
  echo "daemon mode is not supported for start-mcp.sh because stdio MCP requires an attached client process" >&2
  exit 1
fi

if [[ "$SKIP_BUILD" != "1" ]]; then
  "$ROOT_DIR/scripts/build.sh"
fi

exec "$BIN" -config "$CONFIG" -mcp "$@"
