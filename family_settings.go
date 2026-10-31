package main

// The settings Auralink shares with the ActNow family (internal/actnow;
// spec §9): the app's language and the proxy follow the family's
// settings.json unless the user sets them for Auralink alone. Auralink
// keeps the value in effect in its own settings (General.Language,
// Network.Proxy), so what reads them is unchanged: while it follows, the
// family's value is copied in when it changes (adoptShared), and a change
// made here is written to the family too (setShared); the language and
// the proxy then apply as any change of the settings does
// (settingsChanged: applyLocale, the next request's HTTP client).

import (
	"context"
	"log"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/family"
	asettings "github.com/hehehai/actnowkit/settings"
)

// migrate is the first start's merge of Auralink's shared settings into
// the family's (spec §9.4, §15.2 step 4), on the family's goroutine. ok
// is false when it ran before, or failed (logged; it runs again at the
// next start: it is idempotent).
func (fl *familyLink) migrate(s settings.Settings) (out []asettings.Outcome, ok bool) {
	if s.Family.Migrated {
		return nil, false
	}
	out, err := asettings.Migrate(context.Background(), fl.dir, fl.self.BundleID, actnow.Locals(s))
	if err != nil {
		log.Printf("family: merging the shared settings: %v", err)
		return nil, false
	}
	for _, o := range out {
		log.Printf("family: %s: follow %v, written %v, differs %v", o.Key, o.Follow, o.Wrote, o.Conflict)
	}
	return out, true
}

// adoptShared copies the family's values of the settings Auralink follows
// into its own (spec §9.3), after each read of the family.
func (a *app) adoptShared() {
	if a.fam == nil {
		return
	}
	changes := actnow.Adopt(a.prefs, a.famShared)
	if len(changes) == 0 {
		return
	}
	for key, v := range changes {
		log.Printf("family: %s follows the family: %q", key, v)
	}
	a.updateSettings(func(s *settings.Settings) {
		for key, v := range changes {
			actnow.SetOwn(s, key, v)
		}
	})
}

// setShared changes a shared setting: Auralink's own value, and the
// family's when Auralink follows it (unless a newer app made the family's
// file read-only).
func (a *app) setShared(key, value string) {
	a.updateSettings(func(s *settings.Settings) { actnow.SetOwn(s, key, value) })
	if a.fam == nil || a.famReadOnly || !actnow.Follows(a.prefs, key) {
		return
	}
	a.writeShared(key, value)
}

// writeShared writes one key of the family's settings, in the background.
func (a *app) writeShared(key, value string) {
	fl := a.fam
	value = actnow.SharedValue(key, value)
	a.async(func() {
		shared, err := asettings.Set(context.Background(), fl.dir, fl.self.BundleID, key, value)
		if err != nil {
			log.Printf("family: writing %s: %v", key, err)
			return
		}
		a.post(func() { a.famShared = shared })
	})
}

// followShared makes a shared setting follow the family again: Auralink
// takes the family's value, or gives the family its own when the family
// has none.
func (a *app) followShared(key string) {
	v, set := a.famShared.Get(key)
	a.updateSettings(func(s *settings.Settings) {
		actnow.SetFollow(s, key, true)
		actnow.Answered(s, key)
		if set {
			actnow.SetOwn(s, key, v)
		}
	})
	if !set && !a.famReadOnly {
		a.writeShared(key, actnow.Own(a.prefs, key))
	}
}

// ownShared makes a shared setting Auralink's alone, keeping the value in
// effect (spec §9.3: "Use a different setting for this app").
func (a *app) ownShared(key string) {
	a.updateSettings(func(s *settings.Settings) {
		actnow.SetFollow(s, key, false)
		actnow.Answered(s, key)
	})
}

// sharedShown reports whether a shared setting's row speaks of the
// family: only when a release of another ActNow app is installed on this
// Mac (user 10-17). Alone, Auralink still follows or keeps its own value
// and remembers what differs; it just says nothing of apps that are not
// there.
func (a *app) sharedShown(string) bool {
	return a.fam != nil && a.familyOthers()
}

// familyOthers reports whether another ActNow app (a release, spec §6.3)
// is installed on this Mac.
func (a *app) familyOthers() bool { return len(a.familyOtherNames()) > 0 }

// familyOtherNames are the names of the other ActNow apps installed on
// this Mac (releases, spec §6.3), sorted: what the family's notes name in
// place of "other ActNow apps".
func (a *app) familyOtherNames() []string {
	var out []string
	for _, f := range a.famFound {
		if f.Installed && f.Variant == family.Release && f.App.ID != actnow.AppID {
			out = append(out, a.appNames([]string{f.BundleID})...)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// sharedSource names the app the family's shared settings come from: the
// one that wrote them last, when that is another app, else the other
// apps installed.
func (a *app) sharedSource() string {
	if by := a.famShared.UpdatedBy; by != "" && (a.fam == nil || by != a.fam.self.BundleID) {
		if _, _, err := family.ParseBundleID(by); err == nil {
			return strings.Join(a.appNames([]string{by}), ", ")
		}
	}
	return strings.Join(a.familyOtherNames(), ", ")
}

// sharedNote goes under a shared setting's row (spec §9.3, §9.4): whether
// it applies to every ActNow app, the link that changes that, and once
// after the first start's merge, that the other apps use another value.
func (a *app) sharedNote(c *ui.Context, key string) {
	if !a.sharedShown(key) {
		return
	}
	t := c.Theme()
	follows := actnow.Follows(a.prefs, key)
	others := strings.Join(a.familyOtherNames(), ", ")
	if actnow.Differs(a.prefs, key) && !follows {
		text := i18n.T("%s uses a different proxy.", a.sharedSource())
		if key == asettings.KeyLanguage {
			text = i18n.T("%s uses a different app language.", a.sharedSource())
		}
		notice(c, "shared-differs-"+key, noticeInfo, text, "", func() {
			if ui.Button(c.Key("shared-theirs-"+key), i18n.T("Use theirs")).Clicked() {
				a.followShared(key)
			}
		}, func() { a.updateSettings(func(s *settings.Settings) { actnow.Answered(s, key) }) })
		return
	}
	ui.Row(c.Key("shared-" + key)).Wrap().GapX(px(t, sp2)).GapY(px(t, 2)).AlignItems(ui.Center).Children(func() {
		switch {
		case a.famReadOnly:
			caption(c, i18n.T("%s updated the shared format. Update Auralink to change these settings.", a.sharedSource()))
		case follows:
			caption(c, i18n.T("Shared with other ActNow apps"))
			if ui.Link(c.Key("shared-own-"+key), i18n.T("Use a different setting for this app"), "").FontSize(px(t, fsCaption)).Clicked() {
				a.ownShared(key)
			}
		default:
			caption(c, i18n.T("Only for Auralink"))
			if ui.Link(c.Key("shared-follow-"+key), i18n.T("Use the same setting as %s", others), "").FontSize(px(t, fsCaption)).Clicked() {
				a.followShared(key)
			}
		}
	})
}
