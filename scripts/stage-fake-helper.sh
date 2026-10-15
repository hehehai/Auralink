#!/usr/bin/env bash
# Builds the fake helper (internal/helper/helpertest/cmd/fakehelper) into
# resources/darwin-arm64/helper/auralink-helper, where a packaged Auralink
# looks for the Swift helper, until that exists (helper/README.md). It
# lets go tool mygo build package an app whose supervisor handshakes; it
# records a tone, not the microphone. Runs anywhere Go does (it cross
# compiles for darwin/arm64).
#
#   scripts/stage-fake-helper.sh
#   scripts/mac.sh 'scripts/stage-fake-helper.sh && go tool mygo build'
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
out="$ROOT/resources/darwin-arm64/helper/auralink-helper"
mkdir -p "$(dirname "$out")"
cd "$ROOT"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -o "$out" ./internal/helper/helpertest/cmd/fakehelper
echo "staged the fake helper: $out"
