package main

// Shortcuts that collide with another ActNow app's (internal/actnow; spec
// §10.4). The other installed apps' manifests say their global
// shortcuts; one of Auralink's on the same keys is a conflict, whatever
// the trigger (a tap and a hold of fn both fire on fn). Auralink never
// changes a key itself: the shortcut's row says "Also used by VoxtNew
// (Transcribe)" (Settings → Shortcuts, the feature pages, and while keys
// are typed), and the later of the two apps (the one that set its keys
// last) announces it once, with a notification card.

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/uitext"
	"github.com/hehehai/actnowkit/registry"
	"github.com/hehehai/actnowkit/shortcuts"
)

// checkConflicts finds the conflicts of Auralink's shortcuts (in memory:
// the manifest written and the apps found) and announces the new ones.
// It runs after each read of the family and each change of the shortcuts.
func (a *app) checkConflicts() {
	if a.fam == nil {
		return
	}
	me := registry.HolderOf(a.famManifest)
	a.famConflicts = shortcuts.FindConflicts(me, actnow.Others(a.famFound, a.fam.self.BundleID))
	var prompted []string
	for _, c := range a.famConflicts {
		if !c.Prompt || slices.Contains(a.prefs.Family.Prompted, c.Key()) || slices.Contains(prompted, c.Key()) {
			continue
		}
		prompted = append(prompted, c.Key())
		a.notify(core.SeverityWarning, i18n.T("Shortcut also used by %s", c.Other.Name), a.conflictText(c), linkShortcuts)
	}
	if len(prompted) > 0 {
		a.updateSettings(func(s *settings.Settings) { s.Family.Prompted = append(slices.Clone(s.Family.Prompted), prompted...) })
	}
}

// linkShortcuts is a conflict card's action: Settings → Shortcuts.
const linkShortcuts = "/settings/shortcuts"

// conflictText says a conflict (spec §10.4): "fn is also the Transcribe
// shortcut in VoxtNew. Pressing it will trigger both apps."
func (a *app) conflictText(c shortcuts.Conflict) string {
	keys := c.Mine.Keys
	if h, ok := actnow.Binding(a.prefs.Features, c.Mine.ID); ok {
		keys = uitext.HotkeyText(h.Keys)
	}
	return i18n.T("%s is also the %s shortcut in %s. Pressing it will trigger both apps.", keys, functionName(c.Theirs.Title), c.Other.Name)
}

// familyHits are the lines under a binding whose keys another ActNow app
// uses: "Also used by VoxtNew (Transcribe)" (spec §10.4; saving stays
// allowed).
func (a *app) familyHits(keys string) []string {
	if a.fam == nil {
		return nil
	}
	k, ok := actnow.FamilyKeys(keys)
	if !ok {
		return nil
	}
	var out []string
	for _, h := range shortcuts.UsedBy(k, a.fam.self.BundleID, actnow.Others(a.famFound, a.fam.self.BundleID)) {
		line := i18n.T("Also used by %s (%s)", h.App.Name, functionName(h.Shortcut.Title))
		if !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

// functionName is another app's function, in Auralink's language when
// it is one Auralink names too.
func functionName(title string) string {
	switch title {
	case "Transcribe":
		return i18n.T("Transcribe")
	case "Translate":
		return i18n.T("Translate")
	case "Rewrite":
		return i18n.T("Rewrite")
	case "Paste last":
		return i18n.T("Paste the last result")
	case "Dictation":
		return i18n.T("Dictation")
	}
	return title
}

// conflictLinks are under Settings → Shortcuts' table: each app a
// shortcut collides with, and the link to its own shortcut settings
// (its manifest's links.shortcuts).
func (a *app) conflictLinks(c *ui.Context) {
	t := c.Theme()
	var seen []string
	for _, cf := range a.famConflicts {
		if slices.Contains(seen, cf.Other.BundleID) {
			continue
		}
		seen = append(seen, cf.Other.BundleID)
		var lines []string
		for _, x := range a.famConflicts {
			if x.Other.BundleID == cf.Other.BundleID {
				lines = append(lines, a.conflictText(x))
			}
		}
		ui.Column(c.Key("sc-family-" + cf.Other.BundleID)).Gap(px(t, 4)).Children(func() {
			warnLine(c, strings.Join(lines, "\n"))
			if url := cf.Other.Links[registry.LinkShortcuts]; url != "" {
				ui.Link(c.Key("open"), i18n.T("Open %s settings", cf.Other.Name), url).FontSize(px(t, fsCaption)).Margin(0, 0, 0, px(t, 18))
			}
		})
	}
}
