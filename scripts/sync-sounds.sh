#!/bin/sh
# Copies the ActNow family's cue sounds Auralink offers from the family's
# private sound library (hehehai/actnow-sounds) into resources/sounds,
# which ships in the app (Contents/Resources/sounds) and is committed.
# The audio is React Bits Sound Lab's, licensed for use inside the app,
# not for redistribution on its own (NOTICE): keep this repository private.
#
#   scripts/sync-sounds.sh            # ACTNOW_SOUNDS=../actnow-sounds by default
set -eu
cd "$(dirname "$0")/.."
src=${ACTNOW_SOUNDS:-../actnow-sounds}
if [ ! -f "$src/catalog.json" ]; then
	git clone -q --depth 1 https://github.com/hehehai/actnow-sounds "$src"
fi
# The cues of a recording: taps, selections, toggles, feedback, system
# and media sounds, and every sound meant to start or stop something.
go run github.com/hehehai/actnowkit/cmd/actnow-sounds subset -from "$src" -to resources/sounds \
	-groups tap,select,toggle,feedback,system,media -uses record.start,record.stop,start,stop
