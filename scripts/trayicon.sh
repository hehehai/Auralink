#!/usr/bin/env bash
# Renders the menu bar icons (internal/trayicon) from their SVGs: 36×36
# template PNGs (18 pt at 2×), black with only the alpha drawn, and no faint
# alpha (≤ 8, which macOS tints into specks). Needs rsvg-convert (librsvg)
# and ImageMagick; TestTrayIcons checks the result.
#
#   scripts/trayicon.sh
set -euo pipefail
cd "$(dirname "$0")/../internal/trayicon"
for name in menubar menubar-recording; do
	rsvg-convert -w 36 -h 36 "$name.svg" |
		convert - -channel A -fx 'a<=8/255?0:a' +channel -channel RGB -fx 0 +channel \
			-strip -define png:color-type=6 "$name.png"
done
