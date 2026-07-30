#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ADMIN_DIR="$ROOT_DIR/web/admin"

cd "$ADMIN_DIR"

if [[ "${SKIP_NPM_CI:-0}" != "1" ]]; then
  npm ci
fi

if [[ ! -x node_modules/.bin/esbuild ]]; then
  echo "missing frontend dependencies; run npm ci or omit SKIP_NPM_CI=1" >&2
  exit 1
fi

npm run build

echo "built admin assets: $ADMIN_DIR/dist"
