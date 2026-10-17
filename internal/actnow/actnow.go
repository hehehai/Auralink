// Package actnow is Auralink's side of the ActNow family of apps (the
// family's spec v1.0.3, phase 1, through github.com/hehehai/actnowkit):
// what Auralink tells the other apps about itself (its manifest: what it
// does, its global shortcuts, links to its settings), which of its
// settings follow the family's, which of its services' keys are a family
// vendor's, and whom its shortcuts collide with. Pure logic over
// actnowkit and Auralink's settings; the app (package main, family*.go)
// runs it off the main thread, as it reads and writes files.
//
// It is the one internal package that imports actnowkit (archtest).
//
// Imports: settings, hotkey.
package actnow

import (
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/registry"
)

// Auralink in the family's table (actnowkit family.Apps).
const (
	AppID     = "auralink"
	Name      = "Auralink"
	URLScheme = "auralink"
)

// Links are the pages other apps open from a prompt about Auralink (spec
// §6.2, §10.4: "Open Auralink settings").
func Links() map[string]string {
	return map[string]string{
		registry.LinkShortcuts: URLScheme + "://settings/shortcuts",
		registry.LinkNetwork:   URLScheme + "://settings/network",
	}
}

// Self is the running Auralink, as the family knows it.
type Self struct {
	BundleID string // dev.hehehai.auralink, ….dev (mygo dev), ….betatest…
	Path     string // the .app
	Version  string
	TeamID   string // "" when signed ad hoc
	Beta     bool   // updates include betas (the manifest's channel)
	DataDir  string // Auralink's data folder
}

// Variant is the build Self is: release, dev or test (spec §6.1).
func (s Self) Variant() family.Variant {
	_, v, _ := family.ParseBundleID(s.BundleID)
	return v
}

// FindSelf reads the running app's bundle: its path, its identifier and
// its signature's Team ID. ok is false outside an app bundle (go run,
// tests) and for bundle ids that are not Auralink's: the app then stays
// out of the family. It reads files and the code signature: not on the
// main thread.
func FindSelf() (s Self, ok bool) {
	path := registry.BundlePath()
	if path == "" {
		return Self{}, false
	}
	id, err := registry.BundleIDAt(path)
	if err != nil {
		return Self{}, false
	}
	a, _, err := family.ParseBundleID(id)
	if err != nil || a.ID != AppID {
		return Self{}, false
	}
	return Self{BundleID: id, Path: path, TeamID: teamID()}, true
}
