#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ "${BUILD_ADMIN:-0}" == "1" ]]; then
  "$ROOT_DIR/scripts/build-admin.sh"
fi

VERSION="${VERSION:-dev}"
OUTPUT="${OUTPUT:-$ROOT_DIR/bin/localgate}"
GOOS_VALUE="${GOOS:-}"
GOARCH_VALUE="${GOARCH:-}"

mkdir -p "$(dirname "$OUTPUT")"

BUILD_ARGS=()
if [[ -n "$GOOS_VALUE" ]]; then
  BUILD_ARGS+=("GOOS=$GOOS_VALUE")
fi
if [[ -n "$GOARCH_VALUE" ]]; then
  BUILD_ARGS+=("GOARCH=$GOARCH_VALUE")
fi

LDFLAGS="-X main.Version=$VERSION"

if [[ ${#BUILD_ARGS[@]} -gt 0 ]]; then
  env "${BUILD_ARGS[@]}" go build -ldflags "$LDFLAGS" -o "$OUTPUT" ./cmd/localgate
else
  go build -ldflags "$LDFLAGS" -o "$OUTPUT" ./cmd/localgate
fi

echo "built: $OUTPUT"
