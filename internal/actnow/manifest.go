package actnow

import (
	"strconv"
	"time"

	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/registry"
	"github.com/hehehai/actnowkit/shortcuts"
)

// Capabilities are what Auralink does, for the other apps (spec §6.2):
// voice input into other apps, with remote speech and language models.
var Capabilities = []string{registry.CapDictation, registry.CapASRRemote, registry.CapLLMRemote}

// Manifest is Auralink's apps/<bundle id>.json (spec §6.2): written at
// every start and when its shortcuts, version or update channel change.
// The variant follows from the bundle id (registry.Write).
func Manifest(self Self, s settings.Settings) registry.Manifest {
	channel := "stable"
	if self.Beta {
		channel = "beta"
	}
	name := Name
	if self.Variant() == family.Dev {
		name += " Dev" // mygo dev's bundle name
	}
	return registry.Manifest{
		App: AppID, BundleID: self.BundleID, Name: name, Version: self.Version, Channel: channel,
		Path: self.Path, TeamID: self.TeamID, URLScheme: URLScheme, Links: Links(), DataDir: self.DataDir,
		Capabilities: Capabilities, Shortcuts: Shortcuts(s.Features, s.Family.KeysSince),
	}
}

// Shortcut ids of the manifest (spec §10.2: Auralink's are transcribe,
// translate, rewrite and pasteLast). A feature's second and later
// shortcuts are "<feature>.2"…
const (
	ShortcutTranscribe = "transcribe"
	ShortcutTranslate  = "translate"
	ShortcutRewrite    = "rewrite"
	ShortcutPasteLast  = "pasteLast"
)

// titles are the shortcuts' English names, which other apps show (spec
// §10.3).
var titles = map[string]string{
	hotkey.Transcribe: "Transcribe", hotkey.Translate: "Translate", hotkey.Rewrite: "Rewrite", hotkey.PasteLast: "Paste last",
}

// ShortcutID is the manifest's id of a feature's i-th shortcut (hotkey's
// feature names; hotkey.PasteLast for the paste-last one).
func ShortcutID(feature string, i int) string {
	id := feature
	if feature == hotkey.PasteLast {
		id = ShortcutPasteLast
	}
	if i > 0 {
		id += "." + strconv.Itoa(i+1)
	}
	return id
}

// Shortcuts are Auralink's global shortcuts as the family writes them
// (spec §10.3): every binding of the three features and paste-last, in
// the settings' order; since gives each one's enabledAt. Keys the family's
// grammar cannot spell are left out.
func Shortcuts(f settings.Features, since map[string]time.Time) []shortcuts.Shortcut {
	var out []shortcuts.Shortcut
	add := func(feature string, i int, h settings.Hotkey) {
		c, err := hotkey.Parse(h.Keys)
		if err != nil {
			return
		}
		keys, err := shortcuts.Normalize(Keys(c))
		if err != nil {
			return
		}
		id := ShortcutID(feature, i)
		sc := shortcuts.Shortcut{
			ID: id, Title: titles[feature], Keys: keys, Trigger: Trigger(c, h.Trigger), Enabled: true,
			Mechanism: shortcuts.EventTap, Exclusive: Exclusive(c),
		}
		if t, ok := since[id]; ok {
			sc.EnabledAt = &t
		}
		out = append(out, sc)
	}
	for _, feat := range hotkey.Features {
		for i, h := range hotkey.HotkeysOf(f, feat) {
			add(feat, i, h)
		}
	}
	if f.PasteLast.Keys != "" {
		add(hotkey.PasteLast, 0, settings.Hotkey{Keys: f.PasteLast.Keys, Trigger: settings.TriggerTap})
	}
	return out
}

// Keys spells a chord in the family's grammar (MyGo's accelerator key
// names: Auralink's Delete is Backspace there, ForwardDelete is Delete,
// Return is Enter).
func Keys(c hotkey.Chord) string {
	key := c.Key
	switch key {
	case "Delete":
		key = "Backspace"
	case "ForwardDelete":
		key = "Delete"
	case "Return":
		key = "Enter"
	}
	return hotkey.Chord{Mods: c.Mods, Key: key}.String()
}

// Trigger is the family's trigger of a binding (spec §10.3): a tap of a
// chord with a key is an ordinary press; Auralink's long press is hold.
func Trigger(c hotkey.Chord, trigger string) string {
	switch trigger {
	case settings.TriggerDouble:
		return shortcuts.Double
	case settings.TriggerLong:
		return shortcuts.Hold
	}
	if !c.ModifierOnly() {
		return shortcuts.Press
	}
	return shortcuts.Tap
}

// Exclusive reports whether Auralink swallows the binding's key events:
// a chord with a key always (the helper's event tap), and fn, whose own
// events (and the 🌐 key's after it) are swallowed whenever a binding
// holds it (hotkey.Plan.UsesFn).
func Exclusive(c hotkey.Chord) bool {
	_, fn := c.Mods[proto.ModFn]
	return !c.ModifierOnly() || fn
}

// Stamp is since with now for every shortcut of cur whose keys or trigger
// are not those of old, or that has no time yet, and without the
// shortcuts cur no longer has; changed reports a difference from since.
func Stamp(old, cur settings.Features, since map[string]time.Time, now time.Time) (map[string]time.Time, bool) {
	was := map[string]shortcuts.Shortcut{}
	for _, s := range Shortcuts(old, nil) {
		was[s.ID] = s
	}
	out := map[string]time.Time{}
	changed := false
	now = now.UTC().Truncate(time.Second)
	for _, s := range Shortcuts(cur, nil) {
		t, ok := since[s.ID]
		if w, same := was[s.ID]; !ok || !same || w.Keys != s.Keys || w.Trigger != s.Trigger {
			t, changed = now, true
		}
		out[s.ID] = t
	}
	if len(out) != len(since) {
		changed = true
	}
	return out, changed
}
