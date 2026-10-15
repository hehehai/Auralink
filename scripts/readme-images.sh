#!/usr/bin/env bash
# Renders the snapshots (TestSnapshots) and copies the README's images
# from them into docs/images (English) and docs/images/zh (Chinese),
# compressed with pngquant when it is installed.
#
#   scripts/readme-images.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SHOTS="$(mktemp -d)"
trap 'rm -rf "$SHOTS"' EXIT

cd "$ROOT"
AURALINK_SNAPSHOTS="$SHOTS" CGO_ENABLED=0 go test -count=1 -run TestSnapshots . >/dev/null

# README name: snapshot name (light, at the window's default size).
pairs=(
  "home:page-home-ready"
  "overlay-recording:overlay-recording-scene"
  "overlay-card:overlay-translate-card"
  "app-enhancement:page-app-enhancement-group"
)
mkdir -p docs/images/zh
for p in "${pairs[@]}"; do
  name="${p%%:*}" shot="${p#*:}"
  cp "$SHOTS/$shot-light.png" "docs/images/$name.png"
  cp "$SHOTS/$shot-light-zh-Hans.png" "docs/images/zh/$name.png"
done
if command -v pngquant >/dev/null; then
  pngquant --force --skip-if-larger --ext .png --quality 70-95 docs/images/*.png docs/images/zh/*.png || true
fi
ls -la docs/images docs/images/zh
