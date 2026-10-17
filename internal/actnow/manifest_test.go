package actnow

import (
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/conformance"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/registry"
	"github.com/hehehai/actnowkit/shortcuts"
)

// TestDefaultShortcuts: Auralink's defaults are the family table's
// (spec §10.2), as the manifest writes them.
func TestDefaultShortcuts(t *testing.T) {
	got := Shortcuts(settings.Defaults().Features, nil)
	want := shortcuts.DefaultsOf(AppID)
	if len(got) != len(want) {
		t.Fatalf("%d shortcuts, the family's table has %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.ID != w.ID || g.Title != w.Title || g.Keys != w.Keys || g.Trigger != w.Trigger || g.Mechanism != w.Mechanism || !g.Enabled {
			t.Errorf("shortcut %d: %+v, want %+v", i, g, w)
		}
	}
	// fn is swallowed whenever a binding holds it, ⌃⌘V as every chord with a key.
	for _, g := range got {
		if !g.Exclusive {
			t.Errorf("%s (%s) is not exclusive: Auralink's event tap swallows it", g.ID, g.Keys)
		}
	}
}

func TestShortcutsSpelling(t *testing.T) {
	f := settings.Features{
		Transcribe: settings.Transcribe{Feature: settings.Feature{Hotkeys: []settings.Hotkey{
			{Keys: "RightCommand", Trigger: settings.TriggerLong}, {Keys: "globe", Trigger: settings.TriggerDouble},
		}}},
		Translate: settings.Translate{Feature: settings.Feature{Hotkeys: []settings.Hotkey{{Keys: "Control+Option+Return"}}}},
		Rewrite:   settings.Rewrite{Feature: settings.Feature{Hotkeys: []settings.Hotkey{{Keys: "Option+ForwardDelete"}, {Keys: "nonsense"}}}},
		PasteLast: settings.Hotkey{Keys: "Shift+Command+Delete"},
	}
	at := time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	got := Shortcuts(f, map[string]time.Time{"transcribe.2": at})
	want := []struct{ id, keys, trigger string }{
		{"transcribe", "RightCommand", shortcuts.Hold},
		{"transcribe.2", "Fn", shortcuts.Double},
		{"translate", "Control+Option+Enter", shortcuts.Press},
		{"rewrite", "Option+Delete", shortcuts.Press},
		{"pasteLast", "Shift+Command+Backspace", shortcuts.Press},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Keys != w.keys || got[i].Trigger != w.trigger {
			t.Errorf("%d: %s %s %s, want %+v", i, got[i].ID, got[i].Keys, got[i].Trigger, w)
		}
	}
	if got[0].Exclusive || !got[1].Exclusive {
		t.Errorf("right ⌘ alone is not swallowed, fn is: %v %v", got[0].Exclusive, got[1].Exclusive)
	}
	if e := got[1].EnabledAt; e == nil || !e.Equal(at) {
		t.Errorf("enabledAt %v", e)
	}
	if got[0].EnabledAt != nil {
		t.Error("an unstamped shortcut has an enabledAt")
	}
}

func TestStamp(t *testing.T) {
	d := settings.Defaults().Features
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 17, 9, 30, 0, 500, time.UTC)
	since, changed := Stamp(d, d, nil, now)
	if !changed || len(since) != 4 || !since["transcribe"].Equal(now.Truncate(time.Second)) {
		t.Fatalf("first stamp: %v %v", since, changed)
	}
	for id := range since {
		since[id] = t0
	}
	if again, changed := Stamp(d, d, since, now); changed || !again["pasteLast"].Equal(t0) {
		t.Errorf("nothing changed: %v %v", again, changed)
	}
	moved := d
	moved.Translate.Hotkeys = []settings.Hotkey{{Keys: "RightOption"}}
	moved.PasteLast = settings.Hotkey{}
	got, changed := Stamp(d, moved, since, now)
	if !changed || !got["translate"].Equal(now.Truncate(time.Second)) || !got["transcribe"].Equal(t0) {
		t.Errorf("translate moved: %v", got)
	}
	if _, ok := got["pasteLast"]; ok {
		t.Error("paste-last is off but keeps a time")
	}
}

// TestManifestWrites: the manifest is valid for the family (registry.Write
// checks it), in the family's spelling, and another app sees Auralink's
// shortcuts.
func TestManifestWrites(t *testing.T) {
	fx := conformance.New(t)
	path, err := conformance.FakeApp(t.TempDir(), "Auralink Dev", "dev.hehehai.auralink.dev")
	if err != nil {
		t.Fatal(err)
	}
	self := Self{BundleID: "dev.hehehai.auralink.dev", Path: path, Version: "0.1.0-beta.2", Beta: true, DataDir: "/data"}
	if err := registry.Write(fx.Family, Manifest(self, settings.Defaults())); err != nil {
		t.Fatal(err)
	}
	m, err := registry.Read(fx.Family, self.BundleID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Variant != family.Dev || m.Name != "Auralink Dev" || m.Channel != "beta" || m.App != AppID || m.URLScheme != "auralink" {
		t.Errorf("manifest %+v", m)
	}
	if m.Links[registry.LinkShortcuts] != "auralink://settings/shortcuts" || m.Links[registry.LinkNetwork] != "auralink://settings/network" {
		t.Errorf("links %v", m.Links)
	}
	if !m.Has(registry.CapDictation) || !m.Has(registry.CapASRRemote) || !m.Has(registry.CapLLMRemote) || m.Has(registry.CapModelsHF) {
		t.Errorf("capabilities %v", m.Capabilities)
	}
	found, _ := registry.Discover(fx.Family, conformance.FixtureSystem{})
	voxt := shortcuts.Holder{BundleID: "dev.hehehai.voxtnew", Name: "VoxtNew", Shortcuts: []shortcuts.Shortcut{{ID: "transcribe", Keys: "globe", Enabled: true}}}
	hits := shortcuts.FindConflicts(voxt, registry.Holders(found))
	if len(hits) != 1 || hits[0].Theirs.ID != "transcribe" || hits[0].Other.Name != "Auralink Dev" {
		t.Errorf("VoxtNew on fn: %+v", hits)
	}
}
