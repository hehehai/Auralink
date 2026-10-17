// Package trayicon holds the menu bar icon: Auralink's symbol (a
// lowercase a: a sound ring and a text cursor), from the ActNow brand kit
// (Family-1, Auralink-A), as template PNGs of 36×36 pixels. MyGo's macOS
// tray sizes any icon to 18 points tall, so on a Retina screen each pixel
// is one device pixel; a 1× screen gets it scaled down by AppKit.
//
// The PNGs are the kit's exports, committed as they are (menubar.svg is
// the symbol's 24-unit vector, for a new export). Recording shows the
// symbol a little smaller, towards the bottom left, with a solid dot at
// its top right in a gap cut around it; macOS tints both like the rest of
// the menu bar.
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
