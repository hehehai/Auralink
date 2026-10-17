// Package trayicon holds the menu bar icon: a speech bubble with a
// waveform of three bars, drawn as template PNGs of 36×36 pixels. MyGo's
// macOS tray sizes any icon to 18 points tall, so on a Retina screen each
// pixel is one device pixel; a 1× screen gets it scaled down by AppKit.
//
// The SVGs are the sources, on a 24-unit grid (1 unit = 1.5 px): 3 px
// strokes (1.5 pt, about the weight of the system's own menu bar icons)
// with every straight edge on a whole pixel, the bubble 33 px wide with the
// bars centred in it. scripts/trayicon.sh renders the PNGs: black, only the
// alpha drawn, no faint alpha that macOS would tint into specks
// (TestTrayIcons checks that, and that the two states read apart). While
// recording the bubble turns solid with the bars cut out of it, a little
// longer than at rest; macOS tints both like the rest of the menu bar.
package trayicon

import _ "embed"

// Size is the icons' width and height in pixels (18 pt at 2×).
const Size = 36

var (
	// Idle is the icon at rest.
	//go:embed menubar.png
	Idle []byte
	// Recording is the icon while a session listens or works.
	//go:embed menubar-recording.png
	Recording []byte
)

// Icon is the icon for the state: recording or not.
func Icon(recording bool) []byte {
	if recording {
		return Recording
	}
	return Idle
}
