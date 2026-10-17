package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/conformance"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/registry"
	asettings "github.com/hehehai/actnowkit/settings"
)

// audioSmith is AudioSmith's release bundle id: the settings' rows speak
// of the family only when a release of another ActNow app is installed
// (spec §6.3). The fixture's manifest points at a fake .app.
const audioSmith = "dev.hehehai.audiosmith"

// setFamily writes a shared setting as another app does.
func setFamily(t *testing.T, fx *conformance.Fixture, key, value string) {
	t.Helper()
	if _, err := asettings.Set(context.Background(), fx.Family, conformance.PeerSmith, key, value); err != nil {
		t.Fatal(err)
	}
}

func familyValue(t *testing.T, fx *conformance.Fixture, key string) string {
	t.Helper()
	s, _, err := asettings.Read(fx.Family)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get(key)
	return v
}

// TestFamilySettingsNewInstall: a new Auralink takes the family's
// language and proxy (spec §9.3, §9.4).
func TestFamilySettingsNewInstall(t *testing.T) {
	fx := withFamily(t)
	setFamily(t, fx, asettings.KeyLanguage, "zh-Hans")
	setFamily(t, fx, asettings.KeyProxy, "socks5://127.0.0.1:7897")
	a, _, _ := newTestApp(t)
	drain := joinFamily(t, a)
	drain()
	s := a.settings.Get()
	if s.General.Language != "zh-Hans" || s.Network.Proxy != "socks5://127.0.0.1:7897" {
		t.Errorf("own %q %q", s.General.Language, s.Network.Proxy)
	}
	if !s.Family.Migrated || len(s.Family.Differs) != 0 || !actnow.Follows(s, asettings.KeyProxy) {
		t.Errorf("family %+v", s.Family)
	}
}

// TestFamilySettingsFirstStart: the user's own proxy goes to the family
// when it has none; a language that differs keeps Auralink's and says so
// once, until the user answers.
func TestFamilySettingsFirstStart(t *testing.T) {
	fx := withFamily(t)
	fx.Register(audioSmith)
	setFamily(t, fx, asettings.KeyLanguage, "zh-Hans")
	a, _, tt := newTestApp(t)
	a.updateSettings(func(s *settings.Settings) {
		s.Network.Proxy = "http://10.0.0.1:3128"
		s.General.Language = "en"
	})
	drain := joinFamily(t, a)
	drain()
	if v := familyValue(t, fx, asettings.KeyProxy); v != "http://10.0.0.1:3128" {
		t.Errorf("the family's proxy %q", v)
	}
	s := a.settings.Get()
	if s.General.Language != "en" || actnow.Follows(s, asettings.KeyLanguage) || !actnow.Differs(s, asettings.KeyLanguage) {
		t.Fatalf("language %q, family %+v", s.General.Language, s.Family)
	}
	// The row says so; "Use theirs" takes the family's language.
	a.navigate("/settings/general")
	frames(tt)
	if !has(tt, "Other ActNow apps use a different app language.") {
		t.Fatalf("no notice: %q", firstTexts(tt))
	}
	click(t, tt, "Use theirs")
	drain()
	s = a.settings.Get()
	if s.General.Language != "zh-Hans" || !actnow.Follows(s, asettings.KeyLanguage) || actnow.Differs(s, asettings.KeyLanguage) {
		t.Errorf("after Use theirs: %q %+v", s.General.Language, s.Family)
	}
	// The second start merges nothing again.
	if out, ok := a.fam.migrate(s); ok || out != nil {
		t.Errorf("migrated twice: %+v", out)
	}
}

// TestFamilySettingsFollow: following, a change made here goes to the
// family and one made by another app comes here (notification); after
// "Use a different setting for this app", neither.
func TestFamilySettingsFollow(t *testing.T) {
	fx := withFamily(t)
	fx.Register(audioSmith)
	a, _, tt := newTestApp(t)
	drain := joinFamily(t, a)
	drain()
	a.navigate("/settings/network")
	frames(tt)
	if !has(tt, "Applies to all ActNow apps") {
		t.Fatalf("no family note: %q", firstTexts(tt))
	}
	a.setShared(asettings.KeyProxy, "127.0.0.1:7890")
	drain()
	if v := familyValue(t, fx, asettings.KeyProxy); v != "http://127.0.0.1:7890" {
		t.Errorf("family proxy %q", v)
	}
	if a.prefs.Network.Proxy != "http://127.0.0.1:7890" {
		t.Errorf("own proxy %q: the family's spelling comes back", a.prefs.Network.Proxy)
	}
	// Another app changes it: the notification reads the family again.
	setFamily(t, fx, asettings.KeyProxy, "direct")
	drain()
	if p := a.settings.Get().Network.Proxy; p != "direct" {
		t.Fatalf("own proxy %q after AudioSmith's change", p)
	}
	click(t, tt, "Use a different setting for this app")
	drain()
	if actnow.Follows(a.prefs, asettings.KeyProxy) || !has(tt, "Only for Auralink") {
		t.Fatalf("still follows: %+v", a.prefs.Family)
	}
	setFamily(t, fx, asettings.KeyProxy, "socks5://127.0.0.1:1080")
	drain()
	a.setShared(asettings.KeyProxy, "http://127.0.0.1:8888")
	drain()
	if p := a.settings.Get().Network.Proxy; p != "http://127.0.0.1:8888" {
		t.Errorf("own proxy %q", p)
	}
	if v := familyValue(t, fx, asettings.KeyProxy); v != "socks5://127.0.0.1:1080" {
		t.Errorf("Auralink's own proxy went to the family: %q", v)
	}
	// Following again takes the family's.
	click(t, tt, "Use the same setting as other ActNow apps")
	drain()
	if p := a.settings.Get().Network.Proxy; p != "socks5://127.0.0.1:1080" || !actnow.Follows(a.prefs, asettings.KeyProxy) {
		t.Errorf("follows again: %q %+v", p, a.prefs.Family)
	}
}

// TestFamilySettingsQuiet: alone on the Mac, following, the rows say
// nothing of the family; a newer app's format is read, never written.
func TestFamilySettingsQuiet(t *testing.T) {
	fx := withFamily(t)
	a, _, tt := newTestApp(t)
	drain := joinFamily(t, a)
	drain()
	a.navigate("/settings/network")
	frames(tt)
	if has(tt, "Applies to all ActNow apps") {
		t.Error("a family note without another ActNow app")
	}
	future, err := conformance.File("settings-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx.Family.SettingsFile(), future, 0o600); err != nil {
		t.Fatal(err)
	}
	a.familyRefresh()
	drain()
	if !a.famReadOnly {
		t.Fatal("a newer format is not read-only")
	}
	a.setShared(asettings.KeyProxy, "direct")
	drain()
	if data, _ := os.ReadFile(fx.Family.SettingsFile()); string(data) != string(future) {
		t.Error("the newer format was written")
	}
}

// TestFamilyConformanceSettings runs actnowkit's scenarios on settings
// (spec §16.1 scenarios 5 and 9) against Auralink.
func TestFamilyConformanceSettings(t *testing.T) {
	for _, sc := range conformance.Scenarios {
		if sc.Name != "SettingsFollowFamily" && sc.Name != "ForwardCompatibleSettings" {
			continue
		}
		t.Run(sc.Name, func(t *testing.T) {
			sc.Run(t, conformance.New(t), newFamilyAdapter)
		})
	}
}

// familyAdapter is Auralink under actnowkit's conformance scenarios: a
// test app in the scenario's family directory, driven as the user would
// (the settings' rows), reading the family again where the app would on
// its activation.
type familyAdapter struct {
	a     *app
	drain func()
	bid   string
}

func newFamilyAdapter(tb testing.TB, fx *conformance.Fixture, bundleID string) any {
	t := tb.(*testing.T)
	path, err := conformance.FakeApp(t.TempDir(), "Auralink", bundleID)
	if err != nil {
		t.Fatal(err)
	}
	old := newFamily
	newFamily = func() *familyLink {
		return &familyLink{dir: fx.Family, self: actnow.Self{BundleID: bundleID, Path: path}, sys: conformance.FixtureSystem{}}
	}
	t.Cleanup(func() { newFamily = old })
	a, _, _ := newTestApp(t)
	drain := joinFamily(t, a)
	drain()
	return &familyAdapter{a: a, drain: drain, bid: bundleID}
}

func (f *familyAdapter) BundleID() string { return f.bid }

func (f *familyAdapter) Set(_ context.Context, key, value string) error {
	if !slices.Contains(actnow.Shared, key) {
		return fmt.Errorf("Auralink has no setting %s", key)
	}
	f.a.setShared(key, value)
	f.drain()
	return nil
}

func (f *familyAdapter) StopFollowing(_ context.Context, key string) error {
	f.a.ownShared(key)
	f.drain()
	return nil
}

func (f *familyAdapter) Effective(_ context.Context, key string) (string, error) {
	f.a.familyRefresh() // as when Auralink becomes active
	f.drain()
	if slices.Contains(actnow.Shared, key) {
		return actnow.Own(f.a.settings.Get(), key), nil
	}
	// A setting Auralink does not have: as an app that follows it.
	return asettings.Effective(f.a.famShared, key, true, "", ""), nil
}

// familyShots are the shared settings' rows in the family (TestSnapshots,
// after the settings sections): following, with AudioSmith installed;
// the language that differed at the first start; a proxy of Auralink's
// own. The family is in memory only: nothing reads or writes its files.
func familyShots(a *app, tt *ui.Tester, save func(string)) {
	a.fam = &familyLink{self: actnow.Self{BundleID: actnow.AppID}}
	a.famFound = []registry.Found{{BundleID: audioSmith, App: family.Apps[0], Variant: family.Release, Name: "AudioSmith", Installed: true}}
	a.navigate("/settings/general")
	save("settings-general-family")
	a.updateSettings(func(s *settings.Settings) {
		actnow.SetFollow(s, asettings.KeyLanguage, false)
		s.Family.Differs = []string{asettings.KeyLanguage}
	})
	save("settings-general-family-differs")
	a.navigate("/settings/network")
	save("settings-network-family")
	a.updateSettings(func(s *settings.Settings) { actnow.SetFollow(s, asettings.KeyProxy, false) })
	save("settings-network-family-own")
	a.updateSettings(func(s *settings.Settings) { s.Family = settings.Family{} })
	a.fam, a.famFound = nil, nil
}
