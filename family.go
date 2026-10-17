package main

// Auralink in the ActNow family of apps (internal/actnow; the family's
// spec v1.0.3, phase 1): at the start, the family directory, Auralink's
// manifest (what it does, its shortcuts, links to its settings), the
// first start's merge of the shared settings, the other family apps, and
// a watch on the family's files. Every step reads
// or writes files: they run on a goroutine, and what they found comes
// back through a.post. A failure is logged and changes nothing else:
// Auralink works as before without the family.
//
// The shared settings are family_settings.go's.

import (
	"log"
	"slices"
	"sync/atomic"
	"time"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/credentials"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/registry"
	asettings "github.com/hehehai/actnowkit/settings"
	"github.com/hehehai/actnowkit/shortcuts"
)

// familyState is what Auralink knows of the family, on the main thread.
type familyState struct {
	// fam is the family link, nil when Auralink is not in the family:
	// outside an app bundle (go run, tests), in the demo build, or until
	// the start found it.
	fam *familyLink
	// famFound are the family apps found (registry.Discover), Auralink
	// among them; famShared the family's shared settings and famReadOnly
	// whether a newer app's format makes them read-only (spec §5.1).
	famFound    []registry.Found
	famShared   asettings.Settings
	famReadOnly bool
	// famManifest is the manifest last written (its shortcuts and
	// channel tell when to write it again).
	famManifest registry.Manifest
	// famCreds is the family's shared keys (family_keys.go), and famLink
	// a.fam, for goroutines (family_paste.go): nil outside the family.
	famCreds atomic.Pointer[credentials.Store]
	famLink  atomic.Pointer[familyLink]
	// famConflicts are Auralink's shortcuts on another app's keys
	// (family_shortcuts.go).
	famConflicts []shortcuts.Conflict
}

// familyLink is the family directory and Auralink in it. Its fields are
// set once; its methods run off the main thread.
type familyLink struct {
	dir   family.Dir
	self  actnow.Self
	sys   registry.System    // LaunchServices and the running apps
	creds *credentials.Store // the family's shared keys (the login Keychain's "ActNow")
	watch *family.Watcher
}

// newFamily finds the running Auralink's bundle and the family directory
// ($ACTNOW_FAMILY_DIR, else ~/Library/Application Support/ActNow); nil
// keeps Auralink out of the family (outside an app bundle). Tests and the
// demo build replace it. Off the main thread: it reads the bundle.
var newFamily = func() *familyLink {
	self, ok := actnow.FindSelf()
	if !ok {
		log.Printf("family: not running from an Auralink app bundle: the ActNow family is off")
		return nil
	}
	dir := family.Default()
	return &familyLink{dir: dir, self: self, sys: actnow.System, creds: &credentials.Store{Family: dir, BundleID: self.BundleID}}
}

// familyRead is what a read of the family found.
type familyRead struct {
	found    []registry.Found
	shared   asettings.Settings
	readOnly bool
}

// connectFamily joins the family (spec §15.2): the directory, the
// migration of the shared settings (the first time), the manifest, the
// other apps and the watch. It runs once the window is open (main.go),
// after the single-instance lock.
func (a *app) connectFamily() {
	open := newFamily
	if open == nil {
		return
	}
	s, version, beta, data := a.prefs, a.version, a.betaOn(), a.dataDir()
	a.async(func() {
		fl := open()
		if fl == nil {
			return
		}
		fl.self.Version, fl.self.Beta, fl.self.DataDir = version, beta, data
		info, err := fl.dir.Ensure(fl.self.BundleID)
		if err != nil {
			log.Printf("family: %s: %v", fl.dir.Root, err)
			return
		}
		if n := fl.dir.CleanAllTemps(); n > 0 {
			log.Printf("family: removed %d temporary files left by a crash", n)
		}
		readOnly := info.ReadOnly()
		m := actnow.Manifest(fl.self, s)
		var outcomes []asettings.Outcome
		migrated := false
		if !readOnly {
			fl.writeManifest(m)
			outcomes, migrated = fl.migrate(s) // family_settings.go
		}
		r := fl.read()
		r.readOnly = r.readOnly || readOnly
		w, err := fl.dir.Watch(family.TopicApps, family.TopicSettings, family.TopicCredentials)
		if err != nil {
			log.Printf("family: watch: %v", err)
		}
		fl.watch = w
		a.post(func() {
			a.fam, a.famManifest = fl, m
			a.famCreds.Store(fl.creds)
			a.famLink.Store(fl)
			if migrated {
				a.updateSettings(func(s *settings.Settings) { actnow.Migrated(s, outcomes) })
			}
			a.familyRead(r)
			a.stampShortcuts(a.prefs.Features)
		})
		if w != nil {
			go func() {
				for topic := range w.C {
					a.post(func() { a.familyNotified(topic) })
				}
			}()
		}
	})
}

// writeManifest writes Auralink's manifest (spec §6.2); a failure is
// logged.
func (fl *familyLink) writeManifest(m registry.Manifest) {
	if err := registry.Write(fl.dir, m); err != nil {
		log.Printf("family: manifest: %v", err)
	}
}

// read reads the family apps and the shared settings.
func (fl *familyLink) read() familyRead {
	found, errs := registry.Discover(fl.dir, fl.sys)
	for _, err := range errs {
		log.Printf("family: %v", err)
	}
	shared, ro, err := asettings.Read(fl.dir)
	if err != nil {
		log.Printf("family: settings: %v", err)
	}
	return familyRead{found: found, shared: shared, readOnly: ro}
}

// familyRead keeps what a read found, and takes the shared settings
// Auralink follows (family_settings.go).
func (a *app) familyRead(r familyRead) {
	a.famFound, a.famShared, a.famReadOnly = r.found, r.shared, r.readOnly
	a.adoptShared()
	a.checkConflicts() // family_shortcuts.go
}

// familyNotified answers a change notification of the family's files:
// another app wrote its manifest, the shared settings or keys.
func (a *app) familyNotified(topic string) {
	switch topic {
	case family.TopicApps, family.TopicSettings:
		a.familyRefresh()
	case family.TopicCredentials:
		a.sharedKeysChanged() // family_keys.go
	}
}

// familyRefresh reads the family again: on a notification, and when
// Auralink becomes active (notifications are only hints, spec §5.4).
func (a *app) familyRefresh() {
	fl := a.fam
	if fl == nil {
		return
	}
	a.async(func() {
		r := fl.read()
		a.post(func() { a.familyRead(r) })
	})
}

// stampShortcuts keeps when each shortcut's keys were set (the manifest's
// enabledAt, spec §10.4): now for those that changed since old, or that
// have no time yet.
func (a *app) stampShortcuts(old settings.Features) {
	if a.fam == nil {
		return
	}
	since, changed := actnow.Stamp(old, a.prefs.Features, a.prefs.Family.KeysSince, time.Now())
	if changed {
		a.updateSettings(func(s *settings.Settings) { s.Family.KeysSince = since })
	}
	a.syncManifest()
}

// syncManifest writes the manifest again when what it says changed: the
// shortcuts, or the update channel (settingsChanged).
func (a *app) syncManifest() {
	fl := a.fam
	if fl == nil || a.famReadOnly {
		return
	}
	self := fl.self
	self.Beta = a.betaOn()
	m := actnow.Manifest(self, a.prefs)
	old := a.famManifest
	if m.Channel == old.Channel && slices.EqualFunc(m.Shortcuts, old.Shortcuts, sameShortcut) {
		return
	}
	a.famManifest = m
	a.async(func() { fl.writeManifest(m) })
	a.checkConflicts() // family_shortcuts.go
}

// sameShortcut compares two declared shortcuts.
func sameShortcut(a, b shortcuts.Shortcut) bool {
	at, bt := a.EnabledAt, b.EnabledAt
	return a.ID == b.ID && a.Keys == b.Keys && a.Trigger == b.Trigger && a.Enabled == b.Enabled &&
		a.Exclusive == b.Exclusive && (at == nil) == (bt == nil) && (at == nil || at.Equal(*bt))
}

// closeFamily stops the watch and removes Auralink's runtime file
// (family_paste.go); it runs on quit.
func (a *app) closeFamily() {
	fl := a.fam
	if fl == nil {
		return
	}
	a.famLink.Store(nil)
	if fl.watch != nil {
		fl.watch.Close()
	}
	fl.dropRuntime()
}
