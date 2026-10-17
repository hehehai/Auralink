package actnow

import (
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/registry"
	"github.com/hehehai/actnowkit/shortcuts"
)

func found(id, name string, sc ...shortcuts.Shortcut) registry.Found {
	a, v, _ := family.ParseBundleID(id)
	return registry.Found{BundleID: id, App: a, Variant: v, Name: name, Installed: true,
		Manifest: &registry.Manifest{BundleID: id, Name: name, Updated: time.Unix(0, 0), Shortcuts: sc}}
}

// TestConflicts: VoxtNew on fn collides with Auralink's Transcribe; the
// later one prompts; Auralink's own dev build is no conflict.
func TestConflicts(t *testing.T) {
	s := settings.Defaults()
	early := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	since := map[string]time.Time{"transcribe": early, "translate": early, "rewrite": early, "pasteLast": early}
	me := Manifest(Self{BundleID: "dev.hehehai.auralink"}, settings.Settings{Features: s.Features, Family: settings.Family{KeysSince: since}})
	later := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	others := []registry.Found{
		found("dev.hehehai.voxtnew", "VoxtNew", shortcuts.Shortcut{ID: "transcribe", Title: "Transcribe", Keys: "globe", Enabled: true, EnabledAt: &later}),
		found("dev.hehehai.auralink.dev", "Auralink Dev", shortcuts.DefaultsOf("auralink")...),
		found("dev.hehehai.audiosmith", "AudioSmith", shortcuts.DefaultsOf("audiosmith")...),
		found("dev.hehehai.auralink", "Auralink", me.Shortcuts...),
	}
	got := shortcuts.FindConflicts(registry.HolderOf(me), Others(others, me.BundleID))
	if len(got) != 1 || got[0].Other.Name != "VoxtNew" || got[0].Mine.ID != "transcribe" || got[0].Prompt {
		t.Fatalf("conflicts %+v", got)
	}
	// VoxtNew, the later one, prompts.
	voxt := registry.HolderOf(*others[0].Manifest)
	theirs := shortcuts.FindConflicts(voxt, Others(others[3:], voxt.BundleID))
	if len(theirs) != 1 || !theirs[0].Prompt {
		t.Errorf("VoxtNew's %+v", theirs)
	}
}

func TestFamilyKeysAndBinding(t *testing.T) {
	for in, want := range map[string]string{"globe": "Fn", "Fn+Shift": "Fn+Shift", "Command+Control+V": "Control+Command+V", "Option+Return": "Option+Enter"} {
		if got, ok := FamilyKeys(in); !ok || got != want {
			t.Errorf("%q → %q %v", in, got, ok)
		}
	}
	if _, ok := FamilyKeys("Fn+Nope"); ok {
		t.Error("nonsense parsed")
	}
	f := settings.Defaults().Features
	f.Transcribe.Hotkeys = append(f.Transcribe.Hotkeys, settings.Hotkey{Keys: "RightCommand"})
	for id, want := range map[string]string{"transcribe": "Fn", "transcribe.2": "RightCommand", "rewrite": "Fn+Control", "pasteLast": "Control+Command+V"} {
		if h, ok := Binding(f, id); !ok || h.Keys != want {
			t.Errorf("%s: %+v", id, h)
		}
	}
	if _, ok := Binding(f, "dictation"); ok {
		t.Error("dictation")
	}
}
