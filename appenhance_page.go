package main

// The App enhancement page (/app-enhancement, plan §2.2, §3.1): the
// switch, the groups in a list on the left (a drawer when the window is
// narrow), and the group shown on the right: its name, whether it is on,
// the features it applies to, its scene prompt (layered on top of the
// feature's own clean-up), its apps, its website rules, and the key
// pressed after inserting. A preview at the top says which app is in
// front (the last one before Auralink), its address and the group it
// falls in, live. Browsers (built in and the user's) are listed in a
// dialog that says which ones gave an address at the last shortcut.
//
// Every change goes through setRules (appenhance.go): the sessions see it
// at once, the store a moment later.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/appctx"
	"github.com/hehehai/Auralink/internal/compose"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/settings"
)

// Widths (DIPs at 100 %): the group list, and the least the group's
// column keeps beside it (else the list is a drawer): both fit beside
// the sidebar in the default window (800 wide) and beside the rail in the
// smallest (680).
const (
	aeListW    = 200
	aeMainMinW = 380
)

// aePageState is the page's view state.
type aePageState struct {
	sel  string // the group shown
	list panelState
	// The fields of the group editFor, as typed.
	editFor string
	name    string
	prompt  string
	// The website rule being added, and why it is not valid.
	urlIn  string
	urlErr string
	// The key after inserting: the select's label, the typed keys.
	keySel    string
	keyCustom bool
	keyText   string
	keyErr    string
	// The Add apps dialog.
	appsOpen    bool
	appsQuery   string
	apps        []proto.AppInfo
	appsLoading bool
	appsErr     string
	bundleIn    string
	// The Browsers dialog.
	browsersOpen bool
	brID, brName string
	// The Delete dialog.
	delAsk bool
}

// aeShown runs when the page shows: the preview reads the app in front.
func (a *app) aeShown() { a.refreshPreview(true) }

// aeGroup is the group shown, its index (-1: none).
func (a *app) aeGroup() (appctx.Group, int) {
	p := &a.aePage
	for i, g := range a.aeRules.Groups {
		if g.ID == p.sel {
			return g, i
		}
	}
	if len(a.aeRules.Groups) > 0 {
		p.sel = a.aeRules.Groups[0].ID
		return a.aeRules.Groups[0], 0
	}
	return appctx.Group{}, -1
}

// updateGroup changes the group id with fn.
func (a *app) updateGroup(id string, fn func(g *appctx.Group)) {
	r := cloneRules(a.aeRules)
	for i := range r.Groups {
		if r.Groups[i].ID == id {
			fn(&r.Groups[i])
		}
	}
	a.setRules(r)
}

// addGroup adds a group and shows it.
func (a *app) addGroup() {
	r := cloneRules(a.aeRules)
	name := i18n.T("Group %d", len(r.Groups)+1)
	g := appctx.NewGroup(newGroupID(), name)
	r.Groups = append(r.Groups, g)
	a.setRules(r)
	a.aePage.sel = g.ID
	a.aePage.editFor = ""
	a.aePage.list.open = false // the drawer gives way to the new group
}

// deleteGroup deletes the group shown.
func (a *app) deleteGroup(id string) {
	r := cloneRules(a.aeRules)
	r.Groups = slices.DeleteFunc(r.Groups, func(g appctx.Group) bool { return g.ID == id })
	a.setRules(r)
	a.aePage.sel, a.aePage.editFor = "", ""
	a.toast(i18n.T("Group deleted"))
}

func (a *app) appEnhancementPage(c *ui.Context) {
	t := c.Theme()
	p := &a.aePage
	inline, shown := p.list.layout(a, c, px(t, aeListW), px(t, aeMainMinW))
	pageHeader(c, i18n.T("App enhancement"), func() {
		if !inline {
			p.list.toggle(c.Key("ae-list-toggle"), icons.SidebarMinimalistic, shown, i18n.T("Show the groups"), i18n.T("Hide the groups"))
		}
		on := &a.prefs.AppEnhancement.Enabled
		state := i18n.T("Off")
		if *on {
			state = i18n.T("On")
		}
		caption(c, state).SingleLine()
		if ui.Switch(c.Key("ae-on"), on).Label(i18n.T("Use App enhancement")).Tooltip(i18n.T("Use App enhancement")).Changed() {
			v := *on
			a.updateSettings(func(s *settings.Settings) { s.AppEnhancement.Enabled = v })
		}
		if primaryIconButton(c.Key("ae-new"), icons.Add, i18n.T("New group")).Clicked() {
			a.addGroup()
		}
		// The browsers: a setting of the page, not a group, so not in
		// the groups' list.
		if iconButton(c.Key("ae-browsers"), icons.Global, i18n.T("Browsers")).
			Tooltip(i18n.T("Browsers: which apps are browsers, read for their tab's address")).Clicked() {
			a.openBrowsers()
		}
	})
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		p.list.view(c, "ae-groups", i18n.T("Groups"), inline, shown, true, px(t, aeListW), func() { a.aeGroupList(c) })
		ui.Scroll(c.Key("ae-main")).Grow(1).MinWidth(0).Children(func() {
			ui.Column(c).Padding(0, px(t, sp6), px(t, sp6)).Gap(px(t, sp3)).MaxWidth(px(t, 760)).Children(func() {
				a.aePreviewCard(c)
				if !a.prefs.AppEnhancement.Enabled {
					notice(c, "ae-off", noticeInfo, i18n.T("App enhancement is off: every app gets only the features' own rules."), "", nil, nil)
				}
				if a.aeErr != "" {
					notice(c, "ae-err", noticeError, i18n.T("Could not read the groups."), a.aeErr, nil, nil)
				}
				g, i := a.aeGroup()
				if i < 0 {
					a.aeEmpty(c)
					return
				}
				a.aeDetail(c, g)
			})
		})
	})
	a.aeAppsDialog(c)
	a.aeBrowsersDialog(c)
	a.aeDeleteDialog(c)
	a.flushIcons() // appicons.go: the icons the rows asked for
}

// openBrowsers opens the Browsers dialog, with the apps on this Mac.
func (a *app) openBrowsers() {
	p := &a.aePage
	p.browsersOpen = true
	if len(p.apps) == 0 {
		a.loadApps()
	}
}

// aeEmpty is the page without groups.
func (a *app) aeEmpty(c *ui.Context) {
	emptyState(c, appEnhancementIcon, i18n.T("Rules for the app or website in front"),
		i18n.T("Group apps and website addresses, and give each group a scene prompt (a chat app, your mail, code review). It is added on top of the features' own rules when you dictate there."), func() {
			if ghostIconButton(c.Key("ae-empty-new"), icons.Add, i18n.T("New group")).Clicked() {
				a.addGroup()
			}
		})
}

// ---- The list ----

// groupSummary is a group's second line: its apps and sites.
func groupSummary(g appctx.Group) string {
	var parts []string
	if n := len(g.Apps); n > 0 {
		parts = append(parts, i18n.N(n, "%d app", "%d apps", n))
	}
	if n := len(g.URLs); n > 0 {
		parts = append(parts, i18n.N(n, "%d website", "%d websites", n))
	}
	if len(parts) == 0 {
		return i18n.T("Empty")
	}
	return strings.Join(parts, " · ")
}

func (a *app) aeGroupList(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	p := &a.aePage
	_, _ = a.aeGroup()
	match := a.previewMatch()
	ui.Column(c).Padding(px(t, sp2), px(t, sp2), px(t, sp4)).Gap(px(t, 2)).Children(func() {
		for _, g := range a.aeRules.Groups {
			row := ui.ButtonBase(c.Key("ae-group-"+g.ID)).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusControl)).
				Label(g.Name).Gap(px(t, sp2)).AlignItems(ui.Center)
			switch {
			case p.sel == g.ID:
				row.Background(k.Pressed)
			case row.Hovered():
				row.Background(k.Hover)
			}
			if row.Clicked() {
				p.sel = g.ID
				p.list.open = false
			}
			row.Children(func() {
				col := k.Success
				if !g.Enabled {
					col = k.G[5]
				}
				statusDot(c, col)
				ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
					name := g.Name
					if name == "" {
						name = i18n.T("Untitled")
					}
					tx := ui.Text(c, name).SingleLine().Ellipsis("…").MinWidth(0)
					if !g.Enabled {
						tx.TextColor(k.Text2)
					}
					ui.Text(c, groupSummary(g)).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
				})
				if match.GroupID() == g.ID {
					iconMark(c, icons.Target, k.Accent, i18n.T("Matches the app in front"))
				}
			})
		}
	})
}

// ---- The preview ----

// sourceText says how a group matched.
func sourceText(s appctx.Source, pattern string) string {
	switch s {
	case appctx.SourceURL:
		return i18n.T("by the website rule %s", pattern)
	case appctx.SourceBrowserApp:
		return i18n.T("as its browser")
	case appctx.SourceApp:
		return i18n.T("as its app")
	}
	return ""
}

// ruleSourceLabel says how a history entry's group matched.
func ruleSourceLabel(s string) string {
	switch appctx.Source(s) {
	case appctx.SourceURL:
		return i18n.T("website")
	case appctx.SourceBrowserApp:
		return i18n.T("browser")
	case appctx.SourceApp:
		return i18n.T("app")
	}
	return ""
}

func (a *app) aePreviewCard(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	app := a.previewApp()
	pv := a.aePrev
	browser := appctx.IsBrowser(app.BundleID, a.prefs.AppEnhancement.Browsers)
	fillCard(c.Key("ae-preview")).Padding(px(t, sp3), px(t, sp4)).Gap(px(t, sp2)).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			if app.BundleID == "" {
				ui.Icon(c, icons.WindowFrame.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2).Shrink(0)
			} else {
				a.appIcon(c, proto.AppRef{BundleID: app.BundleID}, 20, true)
			}
			name := app.Name
			if name == "" {
				name = app.BundleID
			}
			if name == "" {
				ui.Text(c, i18n.T("Switch to another app to see which group it falls in.")).TextColor(k.Text2).Grow(1).MinWidth(0)
			} else {
				ui.Text(c, i18n.T("In front: %s", name)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				if browser {
					tagChip(c, i18n.T("Browser"))
				}
				ui.Spacer(c)
			}
			if iconButton(c.Key("ae-preview-refresh"), icons.Refresh, i18n.T("Read again")).Disabled(app.BundleID == "").Clicked() {
				a.refreshPreview(true)
			}
		})
		if app.BundleID == "" {
			return
		}
		if browser {
			addr := ""
			switch {
			case pv.app != app || pv.reading:
				addr = i18n.T("Reading the address…")
			case pv.url != "":
				if ad, ok := appctx.ParseAddress(pv.url); ok {
					addr = ad.String()
				} else {
					addr = pv.url
				}
			case !a.helperReady():
				addr = i18n.T("The helper is not running: the address cannot be read.")
			default:
				addr = i18n.T("No address: %s does not show it to Accessibility now. It is read again when you press a shortcut there; if it never shows, this browser can only match as an app.", app.Name)
			}
			truncText(c, addr).FontSize(px(t, fsCaption)).TextColor(k.Text2)
		}
		m := a.previewMatch()
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			if m.Matched() {
				ui.Icon(c, icons.Target.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Accent).Shrink(0)
				ui.Text(c, i18n.T("Matches %s, %s", m.GroupName(), sourceText(m.Source, m.Pattern))).FontSize(px(t, fsCaption)).Grow(1).MinWidth(0)
			} else {
				ui.Text(c, i18n.T("No group matches: only the features' own rules apply.")).FontSize(px(t, fsCaption)).TextColor(k.Text2).Grow(1).MinWidth(0)
			}
		})
		g, i := a.aeGroup()
		if i < 0 {
			return
		}
		addApp := !g.HasApp(app.BundleID)
		ad, okAddr := appctx.ParseAddress(pv.url)
		addSite := okAddr && browser && pv.app == app && !slices.ContainsFunc(g.URLs, func(u string) bool { return appctx.MatchPattern(u, ad) })
		if !addApp && !addSite {
			return
		}
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Wrap().Margin(0, 0, 0, -px(t, 4)).Children(func() {
			if addApp && quietButton(c.Key("ae-preview-add-app"), icons.Add, i18n.T("Add %s to %s", app.Name, g.Name)).Clicked() {
				a.updateGroup(g.ID, func(g *appctx.Group) {
					g.Apps = append(g.Apps, appctx.App{BundleID: app.BundleID, Name: app.Name})
				})
			}
			if addSite && quietButton(c.Key("ae-preview-add-site"), icons.Add, i18n.T("Add %s to %s", ad.Host, g.Name)).Clicked() {
				host := ad.Host
				a.updateGroup(g.ID, func(g *appctx.Group) { g.URLs = append(g.URLs, host) })
			}
		})
	})
}

// ---- The group ----

// aeSyncFields sets the fields from the group when another group shows,
// or the group changed elsewhere and nothing typed waits.
func (a *app) aeSyncFields(g appctx.Group) {
	p := &a.aePage
	if p.editFor == g.ID {
		return
	}
	p.editFor = g.ID
	p.name, p.prompt = g.Name, g.Prompt
	p.urlIn, p.urlErr = "", ""
	p.keyCustom, p.keyErr = false, ""
	p.keyText = g.AutoKey
	p.keySel = autoKeyLabel(g.AutoKey)
}

func (a *app) aeDetail(c *ui.Context, g appctx.Group) {
	t := c.Theme()
	p := &a.aePage
	a.aeSyncFields(g)
	card(c.Key("ae-group")).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("ae-name"), &p.name).Label(i18n.T("Group name")).Placeholder(i18n.T("Group name")).Grow(1).MinWidth(0)
			if in.Changed() {
				name := p.name
				a.updateGroup(g.ID, func(g *appctx.Group) { g.Name = name })
			}
			on := g.Enabled // derived: committed in OnChange, while it holds the new value
			ui.Switch(c.Key("ae-enabled"), &on).Label(i18n.T("Group on")).Tooltip(i18n.T("Group on")).OnChange(func() {
				v := on
				a.updateGroup(g.ID, func(g *appctx.Group) { g.Enabled = v })
			})
			moreMenu(c.Key("ae-more"), "", func(m *ui.Menu) {
				if m.Item(i18n.T("Delete the group…")).Chosen() {
					p.delAsk = true
				}
			})
		})
		// The features it applies to.
		ui.Row(c).Wrap().GapX(px(t, sp4)).GapY(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				caption(c, i18n.T("Applies to"))
				infoTip(c, i18n.T("Transcribe: the scene shapes the clean-up. Rewrite: it helps read your spoken instruction. Translate: off by default, since a scene's tone rarely belongs in a translation."))
			})
			for _, f := range []struct {
				key, label string
				get        func(appctx.Features) bool
				set        func(*appctx.Features, bool)
			}{
				{"ae-f-transcribe", i18n.T("Transcribe"), func(f appctx.Features) bool { return f.Transcribe }, func(f *appctx.Features, v bool) { f.Transcribe = v }},
				{"ae-f-translate", i18n.T("Translate"), func(f appctx.Features) bool { return f.Translate }, func(f *appctx.Features, v bool) { f.Translate = v }},
				{"ae-f-rewrite", i18n.T("Rewrite"), func(f appctx.Features) bool { return f.Rewrite }, func(f *appctx.Features, v bool) { f.Rewrite = v }},
			} {
				v := f.get(g.Features)
				set := f.set
				ui.Checkbox(c.Key(f.key), &v, f.label).Label(i18n.T("Use the group for %s", f.label)).OnChange(func() {
					on := v
					a.updateGroup(g.ID, func(g *appctx.Group) { set(&g.Features, on) })
				})
			}
		})
	})
	a.aeScene(c, g)
	a.aeApps(c, g)
	a.aeURLs(c, g)
	a.aeAutoKey(c, g)
}

// aeScene is the scene prompt.
func (a *app) aeScene(c *ui.Context, g appctx.Group) {
	t := c.Theme()
	k := tok(t)
	p := &a.aePage
	sectionTitleTip(c, i18n.T("Scene prompt"), i18n.T("Added on top of the features' basic clean-up and your rules, never instead of them: say what is special here (tone, format, names)."))
	in := ui.TextArea(c.Key("ae-prompt"), &p.prompt).Label(i18n.T("Scene prompt of the group")).
		Placeholder(i18n.T("For example: this is a team chat; keep it short and casual, no greetings or sign-offs.")).MinHeight(px(t, 88))
	if in.Changed() {
		v := p.prompt
		a.updateGroup(g.ID, func(g *appctx.Group) { g.Prompt = v })
	}
	ui.Row(c.Key("ae-vars")).Wrap().Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
		caption(c, i18n.T("Variables:"))
		for _, name := range compose.VariableNames {
			token := "{{" + name + "}}"
			b := ui.ButtonBase(c.Key("ae-var-"+name)).Padding(px(t, 2), px(t, 6)).Radius(px(t, 4)).Background(k.Hover).
				Label(token).Tooltip(variableInfo(name)).Shrink(0)
			if b.Hovered() {
				b.Background(k.Pressed)
			}
			b.Children(func() {
				ui.Text(c, token).FontSize(px(t, fsCaption)).Font("monospace").SingleLine()
			})
			if b.Clicked() {
				c.Services().WriteClipboard(token)
				a.toast(i18n.T("Copied"))
			}
		}
	})
}

// aeApps are the member apps.
func (a *app) aeApps(c *ui.Context, g appctx.Group) {
	t := c.Theme()
	k := tok(t)
	p := &a.aePage
	if sectionTitleAdd(c, "ae-add-app", i18n.T("Apps"), i18n.T("Dictating in one of these apps uses the group. A browser here covers the websites no website rule of any group matches."), i18n.T("Add apps…")) {
		p.appsOpen, p.appsQuery, p.bundleIn = true, "", ""
		a.loadApps()
	}
	card(c.Key("ae-apps")).Gap(px(t, sp1)).Children(func() {
		if len(g.Apps) == 0 {
			caption(c, i18n.T("No apps yet."))
		}
		for i, m := range g.Apps {
			ui.Row(c.Key("ae-app-" + m.BundleID)).Gap(px(t, sp2)).AlignItems(ui.Center).MinHeight(px(t, 28)).Children(func() {
				a.appIcon(c, proto.AppRef{BundleID: m.BundleID, Path: a.appPath(m.BundleID)}, 20, true)
				name := m.Name
				if name == "" {
					name = m.BundleID
				}
				ui.Text(c, name).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				truncText(c, m.BundleID).FontSize(px(t, fsCaption)).TextColor(k.Text3).Grow(1)
				if appctx.IsBrowser(m.BundleID, a.prefs.AppEnhancement.Browsers) {
					tagChip(c, i18n.T("Browser"))
				}
				if iconButton(c.Key("remove"), icons.Close, i18n.T("Remove %s", name)).Clicked() {
					idx := i
					a.updateGroup(g.ID, func(g *appctx.Group) {
						if idx < len(g.Apps) {
							g.Apps = slices.Delete(g.Apps, idx, idx+1)
						}
					})
				}
			})
		}
	})
}

// aeURLs are the website rules.
func (a *app) aeURLs(c *ui.Context, g appctx.Group) {
	t := c.Theme()
	k := tok(t)
	p := &a.aePage
	sectionTitleTip(c, i18n.T("Websites"), i18n.T("In a browser, the address of the tab in front is matched against these rules: a site (github.com), a part of it (github.com/acme/*), any subdomain (*.notion.so). * stands for anything. A website rule wins over the browser's own group; the most specific rule wins."))
	card(c.Key("ae-urls")).Gap(px(t, sp1)).Children(func() {
		if len(g.URLs) == 0 {
			caption(c, i18n.T("No website rules yet."))
		}
		for i, u := range g.URLs {
			ui.Row(c.Key(fmt.Sprintf("ae-url-%d", i))).Gap(px(t, sp2)).AlignItems(ui.Center).MinHeight(px(t, 28)).Children(func() {
				ui.Icon(c, icons.LinkMinimalistic2.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2).Shrink(0)
				ui.Text(c, u).Font("monospace").FontSize(px(t, fsCaption)).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				canon := appctx.CanonicalPattern(u)
				if err := appctx.CheckPattern(u); err != nil {
					iconMark(c, icons.DangerTriangle, k.Warning, patternErrorText(err))
				} else if canon != strings.ToLower(strings.TrimSpace(u)) {
					truncText(c, i18n.T("matches %s", canon)).FontSize(px(t, fsCaption)).TextColor(k.Text3)
				}
				ui.Spacer(c)
				if iconButton(c.Key("remove"), icons.Close, i18n.T("Remove %s", u)).Clicked() {
					idx := i
					a.updateGroup(g.ID, func(g *appctx.Group) {
						if idx < len(g.URLs) {
							g.URLs = slices.Delete(g.URLs, idx, idx+1)
						}
					})
				}
			})
		}
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("ae-url-in"), &p.urlIn).Label(i18n.T("New website rule")).Placeholder("github.com/acme/*").
				Font("monospace").Grow(1).MinWidth(0).MaxWidth(px(t, 360))
			if in.Changed() {
				p.urlErr = ""
			}
			add := ui.Button(c.Key("ae-url-add"), i18n.T("Add")).Label(i18n.T("Add the website rule")).Disabled(strings.TrimSpace(p.urlIn) == "")
			if add.Clicked() || in.Submitted() {
				a.aeAddURL(g)
			}
		})
		if p.urlErr != "" {
			warnLine(c, p.urlErr)
		}
	})
}

// aeAddURL adds the typed rule, if it is valid and new.
func (a *app) aeAddURL(g appctx.Group) {
	p := &a.aePage
	u := strings.TrimSpace(p.urlIn)
	if u == "" {
		return
	}
	if err := appctx.CheckPattern(u); err != nil {
		p.urlErr = patternErrorText(err)
		return
	}
	if slices.ContainsFunc(g.URLs, func(v string) bool { return appctx.CanonicalPattern(v) == appctx.CanonicalPattern(u) }) {
		p.urlErr = i18n.T("This rule is in the group already.")
		return
	}
	a.updateGroup(g.ID, func(g *appctx.Group) { g.URLs = append(g.URLs, u) })
	p.urlIn, p.urlErr = "", ""
}

// patternErrorText says why a website rule is not valid.
func patternErrorText(err error) string {
	switch err {
	case appctx.ErrPatternEmpty:
		return i18n.T("Type a website, as in github.com.")
	case appctx.ErrPatternSpace:
		return i18n.T("A website rule has no spaces.")
	case appctx.ErrPatternScheme:
		return i18n.T("Only web addresses (http and https).")
	}
	return i18n.T("Start with a site, as in github.com or *.notion.so; * stands for anything.")
}

// autoKeys are the auto key choices besides None and Custom.
var autoKeys = []string{"Return", "Command+Return", "Shift+Return", "Control+Return", "Option+Return"}

// autoKeyLabel is the select's label of keys.
func autoKeyLabel(keys string) string {
	switch c := appctx.CanonicalAutoKey(keys); c {
	case "":
		return i18n.T("None")
	case "Return":
		return i18n.T("Return ↩")
	default:
		return hotkeyText(c)
	}
}

// aeAutoKey is the key after inserting.
func (a *app) aeAutoKey(c *ui.Context, g appctx.Group) {
	t := c.Theme()
	p := &a.aePage
	sectionTitle(c, i18n.T("After inserting"))
	settingRow(c, "ae-key", i18n.T("Press a key"), i18n.T("Pressed right after the text is inserted, as when a chat app sends with Return. Not pressed when the result shows in the answer card."), func() {
		opts := []string{i18n.T("None")}
		byLabel := map[string]string{i18n.T("None"): ""}
		for _, k := range autoKeys {
			l := autoKeyLabel(k)
			opts = append(opts, l)
			byLabel[l] = k
		}
		if cur := appctx.CanonicalAutoKey(g.AutoKey); cur != "" && !slices.Contains(autoKeys, cur) {
			l := autoKeyLabel(cur)
			opts = append(opts, l)
			byLabel[l] = cur
		}
		opts = append(opts, i18n.T("Custom…"))
		if p.keyCustom {
			p.keySel = i18n.T("Custom…")
		} else if !slices.Contains(opts, p.keySel) {
			p.keySel = autoKeyLabel(g.AutoKey)
		}
		if ui.Select(c.Key("ae-key-sel"), &p.keySel, opts).Label(i18n.T("Key after inserting")).Width(px(t, 180)).Changed() {
			if p.keySel == i18n.T("Custom…") {
				p.keyCustom, p.keyText, p.keyErr = true, g.AutoKey, ""
				return
			}
			keys := byLabel[p.keySel]
			p.keyCustom, p.keyErr = false, ""
			a.updateGroup(g.ID, func(g *appctx.Group) { g.AutoKey = keys })
		}
	})
	if p.keyCustom {
		in := ui.TextInput(c.Key("ae-key-custom"), &p.keyText).Placeholder("Command+Return").Label(i18n.T("Custom key after inserting")).Font("monospace").MaxWidth(px(t, 300))
		if in.Submitted() || in.Changed() && !in.Focused() {
			if _, err := appctx.ParseAutoKey(p.keyText); err != nil || strings.TrimSpace(p.keyText) == "" {
				p.keyErr = i18n.T("One key, with modifiers or not: Return, Command+Return, Tab.")
			} else {
				keys := appctx.CanonicalAutoKey(p.keyText)
				p.keyErr, p.keyCustom, p.keySel = "", false, autoKeyLabel(keys)
				a.updateGroup(g.ID, func(g *appctx.Group) { g.AutoKey = keys })
			}
		}
		if p.keyErr != "" {
			warnLine(c, p.keyErr)
		} else {
			caption(c, i18n.T("Type the keys and press Return: Command+Return, Control+Return, Tab."))
		}
	}
}

// ---- Dialogs ----

// loadApps asks the helper for the running and installed apps.
func (a *app) loadApps() {
	p := &a.aePage
	if !a.helperReady() {
		p.appsErr = i18n.T("The helper is not running: add an app by its bundle ID.")
		return
	}
	p.appsLoading, p.appsErr = true, ""
	helperCall(a, proto.MethodAppList, proto.AppListParams{Installed: true}, 10*time.Second, func(res proto.AppListResult, err error) {
		p := &a.aePage
		p.appsLoading = false
		if err != nil {
			p.appsErr = err.Error()
			return
		}
		p.apps = slices.DeleteFunc(res.Apps, func(x proto.AppInfo) bool { return isSelf(proto.App{PID: x.PID, BundleID: x.BundleID}) })
	})
}

func (a *app) aeAppsDialog(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	p := &a.aePage
	if !p.appsOpen {
		return
	}
	g, i := a.aeGroup()
	if i < 0 {
		p.appsOpen = false
		return
	}
	dialogFrame(c, "ae-apps-dlg", &p.appsOpen, i18n.T("Add apps to %s", g.Name), i18n.T("Running apps first, then the ones in Applications."), 480, func() {
		ui.Row(c).Children(func() { searchField(c, "ae-apps-search", &p.appsQuery, i18n.T("Search apps"), 0) })
		switch {
		case p.appsLoading:
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(px(t, 16), px(t, 16))
				caption(c, i18n.T("Looking for apps…"))
			})
		case p.appsErr != "":
			warnLine(c, p.appsErr)
		}
		q := strings.ToLower(strings.TrimSpace(p.appsQuery))
		apps := slices.Clone(p.apps)
		slices.SortStableFunc(apps, func(x, y proto.AppInfo) int {
			switch {
			case x.Running == y.Running:
				return 0
			case x.Running:
				return -1
			}
			return 1
		})
		shown := 0
		ui.Column(c.Key("ae-apps-list")).Gap(px(t, 2)).Children(func() {
			// Icons load as rows show: the first screenful at once, the
			// others as they scroll into view.
			const eager = 12
			for _, app := range apps {
				if q != "" && !strings.Contains(strings.ToLower(app.Name), q) && !strings.Contains(strings.ToLower(app.BundleID), q) {
					continue
				}
				shown++
				member := g.HasApp(app.BundleID)
				row := ui.ButtonBase(c.Key("ae-pick-"+app.BundleID)).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusControl)).
					Label(app.Name).Gap(px(t, sp2)).AlignItems(ui.Center).Checked(member)
				if row.Hovered() {
					row.Background(k.Hover)
				}
				if row.Clicked() {
					info := app
					a.updateGroup(g.ID, func(g *appctx.Group) {
						if g.HasApp(info.BundleID) {
							g.Apps = slices.DeleteFunc(g.Apps, func(m appctx.App) bool { return strings.EqualFold(m.BundleID, info.BundleID) })
						} else {
							g.Apps = append(g.Apps, appctx.App{BundleID: info.BundleID, Name: info.Name})
						}
					})
				}
				visible := shown <= eager || row.InView()
				row.Children(func() {
					a.appIcon(c, proto.AppRef{BundleID: app.BundleID, Path: app.Path}, 20, visible)
					ui.Text(c, app.Name).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
					truncText(c, app.BundleID).FontSize(px(t, fsCaption)).TextColor(k.Text3).Grow(1)
					if app.Running {
						tagChip(c, i18n.T("Running"))
					}
					// In the group: a check at the end (the room kept for
					// the others, so the rows line up).
					mark := ui.Box(c).Size(px(t, 16), px(t, 16)).Center().Shrink(0)
					if member {
						mark.Children(func() {
							ui.Icon(c, icons.CheckCircle.Bold).Size(px(t, 16), px(t, 16)).TextColor(k.Success)
						})
					}
				})
			}
		})
		if shown == 0 && !p.appsLoading && len(p.apps) > 0 {
			caption(c, i18n.T("No app matches."))
		}
		ui.Box(c).Height(1).Background(k.Line).Shrink(0).Margin(px(t, sp1), 0)
		caption(c, i18n.T("Not listed? Add it by its bundle ID:"))
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("ae-bundle-in"), &p.bundleIn).Label(i18n.T("Bundle ID")).Placeholder("com.example.app").Font("monospace").Grow(1).MinWidth(0)
			id := strings.TrimSpace(p.bundleIn)
			if (ui.Button(c.Key("ae-bundle-add"), i18n.T("Add")).Label(i18n.T("Add the bundle ID")).Disabled(id == "" || strings.ContainsAny(id, " /")).Clicked() || in.Submitted()) && id != "" && !strings.ContainsAny(id, " /") {
				a.updateGroup(g.ID, func(g *appctx.Group) {
					if !g.HasApp(id) {
						g.Apps = append(g.Apps, appctx.App{BundleID: id})
					}
				})
				p.bundleIn = ""
			}
		})
	}, func() {
		ui.Spacer(c)
		if ui.Button(c.Key("ae-apps-done"), i18n.T("Done")).Clicked() {
			p.appsOpen = false
		}
	})
}

// appPath is the bundle of bundleID as app.list said, "" when it did not
// list it (the helper finds the app by its id).
func (a *app) appPath(bundleID string) string {
	for _, x := range a.aePage.apps {
		if strings.EqualFold(x.BundleID, bundleID) {
			return x.Path
		}
	}
	return ""
}

// browserState says what the last shortcut in a browser told of its
// address.
func (a *app) browserState(b appctx.Browser) (string, noticeKind) {
	if r, ok := a.urlReads[b.BundleID]; ok {
		if r.ok {
			return i18n.T("Address read"), noticeOK
		}
		return i18n.T("No address: app only"), noticeWarn
	}
	if b.Engine == appctx.Gecko || b.Engine == "" {
		return i18n.T("Not tried; may be app only"), noticeInfo
	}
	return i18n.T("Not tried yet"), noticeInfo
}

func (a *app) aeBrowsersDialog(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	p := &a.aePage
	if !p.browsersOpen {
		return
	}
	custom := a.prefs.AppEnhancement.Browsers
	dialogFrame(c, "ae-browsers-dlg", &p.browsersOpen, i18n.T("Browsers"),
		i18n.T("Read through Accessibility only: no browser permission to grant."), 520, func() {
			// The browsers on this Mac (once the helper listed the apps),
			// the others in a line.
			installed := map[string]bool{}
			for _, x := range p.apps {
				installed[strings.ToLower(x.BundleID)] = true
			}
			var shown []appctx.Browser
			var others []string
			for _, b := range appctx.Browsers(custom) {
				if len(p.apps) == 0 || installed[strings.ToLower(b.BundleID)] || !appctx.IsBuiltin(b.BundleID) {
					shown = append(shown, b)
				} else {
					others = append(others, b.Name)
				}
			}
			caption(c, i18n.T("At each shortcut in a browser, Auralink reads its tab's address. A browser that shows none matches only as an app."))
			ui.Column(c.Key("ae-browser-list")).Gap(px(t, 2)).Children(func() {
				for _, b := range shown {
					ui.Row(c.Key("ae-browser-" + b.BundleID)).Gap(px(t, sp2)).AlignItems(ui.Center).MinHeight(px(t, 30)).Children(func() {
						text, kind := a.browserState(b)
						col := k.Text3
						switch kind {
						case noticeOK:
							col = k.Success
						case noticeWarn:
							col = k.Warning
						}
						a.appIcon(c, proto.AppRef{BundleID: b.BundleID, Path: a.appPath(b.BundleID)}, 24, true)
						ui.Column(c).Grow(1).MinWidth(0).Children(func() {
							ui.Text(c, b.Name).SingleLine().Ellipsis("…").MinWidth(0)
							truncText(c, b.BundleID).FontSize(px(t, fsMicro)).TextColor(k.Text3)
						})
						statusDot(c, col).Tooltip(text)
						truncText(c, text).FontSize(px(t, fsCaption)).TextColor(k.Text2).MaxWidth(px(t, 200))
						if !appctx.IsBuiltin(b.BundleID) {
							if iconButton(c.Key("remove"), icons.Close, i18n.T("Remove %s", b.Name)).Clicked() {
								id := b.BundleID
								a.updateSettings(func(s *settings.Settings) {
									s.AppEnhancement.Browsers = slices.DeleteFunc(slices.Clone(s.AppEnhancement.Browsers), func(x settings.Browser) bool {
										return strings.EqualFold(x.BundleID, id)
									})
								})
							}
						}
					})
				}
			})
			if len(others) > 0 {
				caption(c, i18n.T("Also known when installed: %s.", strings.Join(others, i18n.T(", ")))).TextColor(k.Text3)
			}
			ui.Box(c).Height(1).Background(k.Line).Shrink(0).Margin(px(t, sp1), 0)
			caption(c, i18n.T("Another browser: its bundle ID (and a name)."))
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
				ui.TextInput(c.Key("ae-br-id"), &p.brID).Label(i18n.T("Bundle ID")).Placeholder("com.example.browser").Font("monospace").Grow(1).Basis(px(t, 180)).MinWidth(0)
				ui.TextInput(c.Key("ae-br-name"), &p.brName).Label(i18n.T("Name")).Placeholder(i18n.T("Name")).Width(px(t, 140))
				id := strings.TrimSpace(p.brID)
				if ui.Button(c.Key("ae-br-add"), i18n.T("Add")).Label(i18n.T("Add the browser")).Disabled(id == "" || strings.ContainsAny(id, " /") || appctx.IsBrowser(id, custom)).Clicked() {
					b := settings.Browser{BundleID: id, Name: strings.TrimSpace(p.brName)}
					a.updateSettings(func(s *settings.Settings) {
						s.AppEnhancement.Browsers = append(slices.Clone(s.AppEnhancement.Browsers), b)
					})
					p.brID, p.brName = "", ""
				}
			})
		}, func() {
			ui.Spacer(c)
			if ui.Button(c.Key("ae-browsers-done"), i18n.T("Done")).Clicked() {
				p.browsersOpen = false
			}
		})
}

func (a *app) aeDeleteDialog(c *ui.Context) {
	p := &a.aePage
	if !p.delAsk {
		return
	}
	g, i := a.aeGroup()
	if i < 0 {
		p.delAsk = false
		return
	}
	dialogFrame(c, "ae-del", &p.delAsk, i18n.T("Delete “%s”?", g.Name), "", 420, func() {
		ui.Text(c, i18n.T("Its apps and websites go back to the features' own rules. This cannot be undone."))
	}, func() {
		ui.Spacer(c)
		if ui.Button(c.Key("ae-del-cancel"), i18n.T("Cancel")).AutoFocus().Clicked() {
			p.delAsk = false
		}
		if recordButton(c.Key("ae-del-ok"), i18n.T("Delete")).Clicked() {
			p.delAsk = false
			a.deleteGroup(g.ID)
		}
	})
}
