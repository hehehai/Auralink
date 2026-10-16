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

# README name: English snapshot [: Chinese snapshot] (light, at the
# window's default size). The English translate card is the one with
# English text, whose title fits in Linux's wider fonts.
pairs=(
  "home:page-home-ready"
  "overlay-recording:overlay-recording-scene"
  "overlay-live:overlay-live-text:overlay-live-text-zh"
  "overlay-card:overlay-translate-card-en:overlay-translate-card"
  "overlay-rewrite:overlay-rewrite-conversation"
  "app-enhancement:page-app-enhancement-group"
  "model-services:providers-asr"
  "settings-audio:settings-audio"
)
mkdir -p docs/images/zh
for p in "${pairs[@]}"; do
  IFS=: read -r name en zh <<<"$p"
  cp "$SHOTS/$en-light.png" "docs/images/$name.png"
  cp "$SHOTS/${zh:-$en}-light-zh-Hans.png" "docs/images/zh/$name.png"
done
if command -v pngquant >/dev/null; then
  pngquant --force --skip-if-larger --ext .png --quality 70-95 docs/images/*.png docs/images/zh/*.png || true
fi
ls -la docs/images docs/images/zh
