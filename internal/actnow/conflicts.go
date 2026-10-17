package actnow

import (
	"slices"

	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/registry"
	"github.com/hehehai/actnowkit/shortcuts"
)

// Others are the shortcuts Auralink's could collide with (spec §10.4):
// those of the installed family apps' manifests, running or not (they may
// open at login), but not of the builds of the app self is (a dev build
// beside the release is no conflict to announce).
func Others(found []registry.Found, self string) []shortcuts.Holder {
	me, _, _ := family.ParseBundleID(self)
	keep := slices.DeleteFunc(slices.Clone(found), func(f registry.Found) bool {
		return f.BundleID == self || me.ID != "" && f.App.ID == me.ID
	})
	return registry.Holders(keep)
}

// FamilyKeys spells keys of Auralink's grammar as the family does, for
// shortcuts.UsedBy; ok is false for keys that do not parse.
func FamilyKeys(keys string) (string, bool) {
	c, err := hotkey.Parse(keys)
	if err != nil {
		return "", false
	}
	k, err := shortcuts.Normalize(Keys(c))
	return k, err == nil
}

// Binding is the binding of f a manifest's shortcut id names
// ("translate", "transcribe.2", "pasteLast").
func Binding(f settings.Features, id string) (settings.Hotkey, bool) {
	if id == ShortcutPasteLast {
		return f.PasteLast, f.PasteLast.Keys != ""
	}
	for _, feat := range hotkey.Features {
		for i, h := range hotkey.HotkeysOf(f, feat) {
			if ShortcutID(feat, i) == id {
				return h, true
			}
		}
	}
	return settings.Hotkey{}, false
}
