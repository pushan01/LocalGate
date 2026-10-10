#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

GOARCH_VALUE="${GOARCH:-amd64}"
OUTPUT_VALUE="${OUTPUT:-$ROOT_DIR/bin/localgate.exe}"

echo "building LocalGate for windows/$GOARCH_VALUE"

GOOS=windows \
GOARCH="$GOARCH_VALUE" \
CGO_ENABLED="${CGO_ENABLED:-0}" \
OUTPUT="$OUTPUT_VALUE" \
"$ROOT_DIR/scripts/build.sh"
