package main

import (
	"slices"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/conformance"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/registry"
	"github.com/hehehai/actnowkit/shortcuts"
)

// testBundleID is the Auralink of the family tests: a test variant (spec
// §6.1), which no real installation shares.
const testBundleID = "dev.hehehai.auralink.familytest"

// withFamily puts the apps the test makes in a temporary family
// directory (conformance.New: ACTNOW_FAMILY_DIR), as testBundleID, with
// discovery that knows no installed app but the fixture's.
func withFamily(t *testing.T) *conformance.Fixture {
	t.Helper()
	fx, _ := withFamilyKeys(t)
	return fx
}

// withFamilyKeys is withFamily, with the login Keychain of the family's
// shared keys in memory (fakeSecurity).
func withFamilyKeys(t *testing.T) (*conformance.Fixture, *fakeSecurity) {
	t.Helper()
	fx := conformance.New(t)
	sec := &fakeSecurity{items: map[string]string{}}
	path, err := conformance.FakeApp(t.TempDir(), "Auralink", testBundleID)
	if err != nil {
		t.Fatal(err)
	}
	old := newFamily
	newFamily = func() *familyLink {
		return &familyLink{dir: fx.Family, self: actnow.Self{BundleID: testBundleID, Path: path}, sys: conformance.FixtureSystem{},
			creds: sec.store(fx, testBundleID)}
	}
	t.Cleanup(func() { newFamily = old })
	return fx, sec
}

// joinFamily starts a test app's family link and runs what it posts.
func joinFamily(t *testing.T, a *app) (drain func()) {
	t.Helper()
	drain = queuePosts(a)
	a.version = "0.1.0-beta.2"
	a.connectFamily()
	drain()
	if a.fam == nil {
		t.Fatal("the app did not join the family")
	}
	return drain
}

// TestFamilyStart: the start makes the family directory, writes
// Auralink's manifest (its shortcuts stamped) and finds the other apps.
func TestFamilyStart(t *testing.T) {
	fx := withFamily(t)
	fx.Register(conformance.PeerSmith)
	a, _, _ := newTestApp(t)
	drain := joinFamily(t, a)

	m, err := registry.Read(fx.Family, testBundleID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Variant != family.Test || m.Version != "0.1.0-beta.2" || m.Channel != "beta" || len(m.Shortcuts) != 4 {
		t.Errorf("manifest %+v", m)
	}
	if !slices.ContainsFunc(a.famFound, func(f registry.Found) bool { return f.BundleID == conformance.PeerSmith && f.Installed }) {
		t.Errorf("AudioSmith not found: %+v", a.famFound)
	}
	// The first start stamps every shortcut; the manifest says when.
	drain()
	if len(a.prefs.Family.KeysSince) != 4 {
		t.Fatalf("keysSince %v", a.prefs.Family.KeysSince)
	}
	m, _ = registry.Read(fx.Family, testBundleID)
	if m.Shortcuts[0].EnabledAt == nil {
		t.Error("the manifest's shortcuts have no enabledAt")
	}
}

// TestFamilyManifestFollowsShortcuts: a changed shortcut is written to
// the manifest, with its time; others keep theirs.
func TestFamilyManifestFollowsShortcuts(t *testing.T) {
	fx := withFamily(t)
	a, _, _ := newTestApp(t)
	drain := joinFamily(t, a)
	drain()
	before := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	a.updateSettings(func(s *settings.Settings) {
		for id := range s.Family.KeysSince {
			s.Family.KeysSince[id] = before
		}
	})
	drain()
	a.updateSettings(func(s *settings.Settings) {
		s.Features.Translate.Hotkeys = []settings.Hotkey{{Keys: "RightOption", Trigger: settings.TriggerLong}}
	})
	drain()
	m, err := registry.Read(fx.Family, testBundleID)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(m.Shortcuts, func(s shortcuts.Shortcut) bool { return s.ID == "translate" })
	if i < 0 || m.Shortcuts[i].Keys != "RightOption" || m.Shortcuts[i].Trigger != shortcuts.Hold {
		t.Fatalf("translate in the manifest: %+v", m.Shortcuts)
	}
	if !m.Shortcuts[i].EnabledAt.After(before) {
		t.Errorf("translate's enabledAt %v is not after %v", m.Shortcuts[i].EnabledAt, before)
	}
	if got := a.prefs.Family.KeysSince["transcribe"]; !got.Equal(before) {
		t.Errorf("transcribe's time moved: %v → %v", before, got)
	}
}

// TestFamilyOff: outside a bundle (newFamily finds none) Auralink stays
// out, and nothing is written.
func TestFamilyOff(t *testing.T) {
	fx := conformance.New(t)
	old := newFamily
	newFamily = func() *familyLink { return nil }
	t.Cleanup(func() { newFamily = old })
	a, _, _ := newTestApp(t)
	a.connectFamily()
	a.familyRefresh()
	a.syncManifest()
	if a.fam != nil {
		t.Fatal("in the family")
	}
	if ms, _ := registry.List(fx.Family); len(ms) != 0 {
		t.Errorf("manifests written: %+v", ms)
	}
}
