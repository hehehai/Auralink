package main

// Settings: the settings service seen from the UI (a snapshot in
// a.prefs, kept by settings.Changed), the language and the theme they
// drive, and the settings dialog (⌘,) with its sections under
// /settings/{section}. The dialog is AudioSmith's, with the sections
// every app of the family has and Auralink's own: Audio
// (settings_audio.go), Overlay style (settings_overlay.go) and the table
// of Shortcuts (settings_shortcuts.go). The features' own settings live
// on their pages (Transcribe, Translate, Rewrite…).

import (
	"context"
	"reflect"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/lang"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
)

// settingsForm is the settings pages' view state: the controls bind to
// these, and their changes go to the service.
type settingsForm struct {
	systemLocale string // mygo.App.Locale(), read when the window opens

	language     string // label
	mainLanguage string // label
	theme        int
	scale        int
	reduceMotion bool
	menuBar      bool
	closeToTray  bool
	openAtLogin  bool
	proxyMode    string // label
	proxy        string // custom proxy
	keepHistory  bool
	keepAudio    bool
	logText      bool
	sounds       bool
	sidebar      int         // automatic, names and icons, icons only (settings_appearance.go)
	ovStyle      overlayForm // settings_overlay.go

	clearHistoryAsk bool // Privacy's Clear the history… dialog
	// loginIs is whether macOS opens Auralink at login, as last read or
	// set: the setting changes it only when they differ.
	loginIs bool
}

// settingsSection is a section of the settings dialog.
type settingsSection struct{ id, title, en string }

func settingsSections() []settingsSection {
	return []settingsSection{
		{"general", i18n.T("General"), "General language menu bar login"},
		{"permissions", i18n.T("Permissions"), "Permissions microphone accessibility privacy"},
		{"audio", i18n.T("Audio"), "Audio microphone input device sounds"},
		{"overlay", i18n.T("Overlay style"), "Overlay style position opacity corners recording pill"},
		{"shortcuts", i18n.T("Shortcuts"), "Shortcuts hotkeys keys fn"},
		{"appearance", i18n.T("Appearance"), "Appearance theme size sidebar"},
		{"network", i18n.T("Network"), "Network proxy"},
		{"privacy", i18n.T("Privacy"), "Privacy history audio logs"},
		{"storage", i18n.T("Storage"), "Storage disk data folder reset"},
		{"about", i18n.T("Diagnostics and about"), "Diagnostics about version bug report"},
	}
}

// settingsSectionIcon is a settings section's icon.
func settingsSectionIcon(id string) icons.Icon {
	switch id {
	case "general":
		return icons.Settings
	case "permissions":
		return permissionsIcon
	case "audio":
		return icons.MicrophoneLarge
	case "overlay":
		return icons.Monitor
	case "shortcuts":
		return icons.Keyboard
	case "appearance":
		return icons.Pallete2
	case "network":
		return icons.WiFiRouterMinimalistic
	case "privacy":
		return icons.ShieldCheck
	case "storage":
		return icons.Database
	}
	return icons.InfoCircle
}

// updateSettings changes the settings: at once in the snapshot, and in
// the service (saved in the background, then announced with Changed).
// fn must only set fields, as it runs on both.
func (a *app) updateSettings(fn func(*settings.Settings)) {
	fn(&a.prefs)
	st := a.settings
	a.async(func() {
		if err := st.Update(context.Background(), fn); err != nil {
			a.post(func() { a.toast(i18n.T("Could not save the settings: %v", err)) })
		}
	})
}

// settingsChanged applies saved settings to the app.
func (a *app) settingsChanged(old, new settings.Settings) {
	a.prefs = new
	if old.General.Language != new.General.Language {
		a.applyLocale()
	}
	if old.Network.Proxy != new.Network.Proxy {
		a.loadProxyInfo()
	}
	if old.General.MenuBar != new.General.MenuBar {
		a.applyTray() // tray.go
	}
	if !reflect.DeepEqual(old.Features, new.Features) {
		a.syncHotkeys()   // sessionlink.go
		a.buildTrayMenu() // the shortcuts it shows
	}
	if old.General.LaunchAtLogin != new.General.LaunchAtLogin && new.General.LaunchAtLogin != a.loginIs && a.hasMenu {
		on := new.General.LaunchAtLogin
		go func() {
			err := mygo.App.SetOpenAtLogin(on)
			a.post(func() {
				if err != nil {
					a.toast(i18n.T("Could not change opening at login: %v", err))
					return
				}
				a.loginIs = on
			})
		}()
	}
	if old.Overlay != new.Overlay {
		a.overlayRestyle() // overlay.go
	}
	a.loadSettingsForm()
}

// applyLocale switches the UI to the settings' language, or the system's,
// and rebuilds the native menu (and the menu bar icon's) in it.
func (a *app) applyLocale() {
	tag := a.prefs.General.Language
	if tag == "" {
		tag = a.systemLocale
	}
	i18n.SetLocale(tag)
	if i18n.Locale() != a.menuLocale {
		a.buildMenu()
		a.buildTrayMenu() // tray.go
	}
	a.loadSettingsForm() // its labels are translated
}

// applyTheme sets the frame's theme from the appearance settings: a
// forced light or dark theme, the UI scale, which grows the text and the
// spacing (sizes of our own scale with px), and the design tokens
// (ui_tokens.go) over MyGo's colours.
func (a *app) applyTheme(c *ui.Context) {
	ap := a.prefs.Appearance
	sys := c.Theme()
	th := *sys
	if (ap.Theme == "light" && sys.Dark) || (ap.Theme == "dark" && !sys.Dark) {
		if ap.Theme == "dark" {
			th = *ui.DarkTheme()
		} else {
			th = *ui.LightTheme()
		}
		if s := c.Preferences().TextScale; s > 0 {
			th.FontSize *= s
		}
	}
	if s := float32(ap.Scale); s > 0 && s != 1 {
		th.FontSize *= s
		th.Spacing = th.Space(1) * s
	}
	styleTheme(&th)
	c.SetTheme(&th)
}

// The options of the selects, and what they mean.
func languageOptions() []string {
	opts := []string{i18n.T("Follow the system")}
	for _, tag := range i18n.Supported() {
		opts = append(opts, i18n.Name(tag))
	}
	return opts
}

// mainLanguages are the languages the main language select offers (lang
// codes), most spoken by Auralink's users first; M3 adds a search.
var mainLanguages = []string{"zh", "en", "ja", "ko", "yue", "fr", "de", "es", "pt", "ru", "it", "vi", "th", "id", "ar"}

func mainLanguageOptions() []string {
	opts := []string{i18n.T("Same as the app")}
	for _, code := range mainLanguages {
		opts = append(opts, langLabel(code))
	}
	return opts
}

// langLabel names a language in the UI's language, with its own name
// when that differs: "Japanese · 日本語", "日语 · 日本語".
func langLabel(code string) string {
	l, ok := lang.Lookup(code)
	if !ok {
		return code
	}
	name := l.NameIn(i18n.Locale())
	if l.Native != "" && l.Native != name {
		return name + " · " + l.Native
	}
	return name
}

func proxyOptions() []string {
	return []string{i18n.T("Automatic (system proxy)"), i18n.T("None"), i18n.T("Custom…")}
}

var scaleLabels = []string{"90%", "100%", "110%", "125%"}

// loadSettingsForm sets the controls from a.prefs.
func (a *app) loadSettingsForm() {
	s := a.prefs
	f := &a.settingsForm
	f.language = languageOptions()[0]
	for i, tag := range i18n.Supported() {
		if tag == s.General.Language {
			f.language = languageOptions()[i+1]
		}
	}
	f.mainLanguage = mainLanguageOptions()[0]
	if i := slices.Index(mainLanguages, s.General.MainLanguage); i >= 0 {
		f.mainLanguage = mainLanguageOptions()[i+1]
	}
	f.theme = max(0, slices.Index(settings.Themes, s.Appearance.Theme))
	f.scale = max(0, slices.Index(settings.Scales, s.Appearance.Scale))
	f.reduceMotion = s.Appearance.ReduceMotion
	f.menuBar, f.closeToTray, f.openAtLogin = s.General.MenuBar, s.General.CloseToTray, s.General.LaunchAtLogin
	px := proxyOptions()
	switch s.Network.Proxy {
	case "":
		f.proxyMode = px[0]
	case settings.ProxyDirect:
		f.proxyMode = px[1]
	default:
		f.proxyMode, f.proxy = px[2], s.Network.Proxy
	}
	f.keepHistory, f.keepAudio, f.logText = s.Privacy.KeepHistory, s.Privacy.KeepAudio, s.Privacy.LogText
	f.sounds = s.Audio.Sounds
	f.sidebar = sidebarChoice(s.Appearance)
	a.loadOverlayForm() // settings_overlay.go
}

// syncOpenAtLogin reads whether macOS opens Auralink at login (the user
// may have turned it off in System Settings) and makes the setting say
// so; it runs when the window opens.
func (a *app) syncOpenAtLogin() {
	go func() {
		on := mygo.App.OpenAtLogin()
		a.post(func() {
			a.loginIs = on
			if on != a.prefs.General.LaunchAtLogin {
				a.updateSettings(func(s *settings.Settings) { s.General.LaunchAtLogin = on })
			}
		})
	}()
}

// openSettings shows the settings dialog at a section's path ("/network").
func (a *app) openSettings(sub string) {
	if a.setRouter.Path() != sub || !a.settingsOpen {
		a.settingsShown(sub)
	}
	if a.setRouter.Path() != sub {
		a.setRouter.Replace(sub)
	}
	a.settingsOpen = true
}

// settingsShown runs when the settings dialog goes to a section: sections
// that read something when they show start it here.
func (a *app) settingsShown(sub string) {
	switch sub {
	case "/permissions":
		a.recheckPermissions() // permissions.go
	case "/audio":
		a.loadDevices() // settings_audio.go
	}
}

// shownPath is the path of what shows: the settings dialog's section
// ("/settings/network") while it is open, else the page's.
func (a *app) shownPath() string {
	if a.settingsOpen {
		return "/settings" + a.setRouter.Path()
	}
	return a.router.Path()
}

// settingsDialog is Settings (⌘,): a dialog over the page, its sections
// in a column on the left (AudioSmith 0.3: Typeless-style).
func (a *app) settingsDialog(c *ui.Context) {
	if !a.settingsOpen {
		return
	}
	t := c.Theme()
	k := tok(t)
	w, h := c.Size()
	margin := px(t, sp6)
	ui.DialogBase(c.Key("settings-dialog"), &a.settingsOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		panel.Width(min(w-2*margin, px(t, 900))).Height(h-2*margin).Radius(px(t, radiusCard)).Background(k.Bg).Clip().
			Shadow(0, 16, 48, 0, ui.RGBA(0, 0, 0, 0.28)).Label(i18n.T("Settings"))
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			// Every section's name shows in full down to the smallest window.
			ui.Column(c).Width(min(px(t, 252), max(px(t, 240), (w-2*margin)*0.3))).Shrink(0).Background(k.Sidebar).Children(func() {
				ui.Row(c).Height(px(t, 52)).Shrink(0).AlignItems(ui.Center).Padding(0, px(t, sp4)).Children(func() {
					ui.Text(c, i18n.T("Settings")).FontSize(px(t, fsSection)).FontWeight(600).SingleLine()
				})
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Column(c).Padding(0, px(t, sp2), px(t, sp2)).Gap(px(t, 2)).Children(func() {
						cur := strings.TrimPrefix(a.setRouter.Path(), "/")
						cur, _, _ = strings.Cut(cur, "/")
						for _, s := range settingsSections() {
							sel := cur == s.id
							b := ui.ButtonBase(c.Key("settings-"+s.id)).Padding(px(t, 6), px(t, sp2)).Gap(px(t, sp2)).Radius(px(t, radiusControl)).
								Justify(ui.Start).Label(s.title).Checked(sel)
							switch {
							case sel:
								b.Background(k.Pressed)
							case b.Hovered():
								b.Background(k.Hover)
							}
							b.Children(func() {
								col := k.Text2
								if sel {
									col = k.Text
								}
								ui.Icon(c, settingsSectionIcon(s.id).Get(sel)).Size(px(t, 16), px(t, 16)).TextColor(col)
								txt := ui.Text(c, s.title).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
								if sel {
									txt.FontWeight(600)
								} else {
									txt.TextColor(k.G[8])
								}
							})
							if b.Clicked() {
								a.navigate("/settings/" + s.id)
							}
						}
					})
				})
			})
			ui.Box(c).Width(1).Background(k.Line)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				a.setRouter.View(c, func(r *ui.Route) {
					title := ""
					for _, s := range settingsSections() {
						if r.Match("/" + s.id) {
							title = s.title
						}
					}
					ui.Row(c).Height(px(t, 52)).Shrink(0).AlignItems(ui.Center).Padding(0, px(t, sp3), 0, px(t, sp6)).Gap(px(t, sp2)).Children(func() {
						ui.Text(c, title).FontSize(px(t, fsSection)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
						if iconButton(c.Key("settings-close"), icons.Close, i18n.T("Close")).Clicked() {
							a.settingsOpen = false
						}
					})
					ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
						ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).MaxWidth(px(t, 680)).Children(func() {
							a.settingsSection(c, r)
						})
					})
				}).Grow(1).MinHeight(0)
			})
		})
	})
}

// settingsSection builds the section of the settings dialog r shows.
func (a *app) settingsSection(c *ui.Context, r *ui.Route) {
	sections := map[string]func(*ui.Context){
		"general":     a.generalSettings,
		"permissions": a.permissionsSettings, // permissions.go
		"audio":       a.audioSettings,       // settings_audio.go
		"overlay":     a.overlaySettings,     // settings_overlay.go
		"shortcuts":   a.shortcutsSettings,   // settings_shortcuts.go
		"appearance":  a.appearanceSettings,
		"network":     a.networkSettings, // settings_network.go
		"privacy":     a.privacySettings, // settings_privacy.go
		"storage":     a.storageSettings, // settings_storage.go
		"about":       a.aboutSettings,   // settings_about.go
	}
	for _, s := range settingsSections() {
		if r.Match("/" + s.id) {
			r.Title(s.title)
			a.lastSettings = s.id
			sections[s.id](c)
			return
		}
	}
	ui.Text(c, i18n.T("There is no page at %s.", a.shownPath())).TextColor(c.Theme().TextMuted)
}

// settingRow is a setting: its name, a ⓘ telling what it does (desc, ""
// for none) and its control on the right (formRow in ui_kit.go).
func settingRow(c *ui.Context, key, title, desc string, control func()) {
	formRow(c, key, title, desc, control)
}

// settingRowNote is a setting whose description is a state to read at
// once (a path, what is missing, a version): it shows under the name.
func settingRowNote(c *ui.Context, key, title, note string, control func()) {
	t := c.Theme()
	ui.Row(c.Key(key)).Wrap().GapX(px(t, sp4)).GapY(px(t, 6)).AlignItems(ui.Center).MinHeight(px(t, 32)).Children(func() {
		ui.Column(c).Grow(1).Basis(px(t, 200)).MinWidth(px(t, 140)).Gap(px(t, 2)).Children(func() {
			ui.Text(c, title)
			if note != "" {
				caption(c, note).Selectable()
			}
		})
		ui.Row(c).Wrap().Gap(px(t, sp2)).AlignItems(ui.Center).Shrink(1).MinWidth(0).Children(control)
	})
}

// sectionTitle heads a group of settings.
func sectionTitle(c *ui.Context, title string) {
	t := c.Theme()
	sectionTitleText(c, title).Margin(px(t, sp2), 0, 0, 0)
}

func sectionTitleText(c *ui.Context, title string) ui.Element {
	t := c.Theme()
	return ui.Text(c, title).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(tok(t).Text2)
}

// sectionTitleTip is sectionTitle with a ⓘ beside it: the margin goes on
// the row, so the title and the ⓘ share one centre line.
func sectionTitleTip(c *ui.Context, title, tip string) {
	t := c.Theme()
	ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Margin(px(t, sp2), 0, 0, 0).Children(func() {
		sectionTitleText(c, title)
		infoTip(c, tip)
	})
}

func (a *app) generalSettings(c *ui.Context) {
	t := c.Theme()
	sectionTitle(c, i18n.T("Languages"))
	settingRow(c, "language", i18n.T("App language"), i18n.T("The language of Auralink's own text. Following the system uses English unless the system's language is supported."), func() {
		opts := languageOptions()
		if ui.Select(c.Key("language-select"), &a.settingsForm.language, opts).Label(i18n.T("App language")).Width(px(t, 220)).Changed() {
			tag := ""
			if i := slices.Index(opts, a.settingsForm.language); i > 0 {
				tag = i18n.Supported()[i-1]
			}
			a.updateSettings(func(s *settings.Settings) { s.General.Language = tag })
		}
	})
	settingRow(c, "main-language", i18n.T("Main language"), i18n.T("The language you mostly speak and write. Clean-up keeps your text in it; Translate writes in English, or in Chinese when this is English, unless the Translate page says otherwise."), func() {
		opts := mainLanguageOptions()
		if ui.Select(c.Key("main-language-select"), &a.settingsForm.mainLanguage, opts).Label(i18n.T("Main language")).Width(px(t, 220)).Changed() {
			code := ""
			if i := slices.Index(opts, a.settingsForm.mainLanguage); i > 0 {
				code = mainLanguages[i-1]
			}
			a.setMainLanguage(code)
		}
	})
	a.otherLanguagesRow(c)

	sectionTitle(c, i18n.T("Menu bar and login"))
	settingRow(c, "menu-bar", i18n.T("Show in the menu bar"), i18n.T("An icon in the menu bar to start a transcription, see the last result and open Auralink."), func() {
		if ui.Switch(c.Key("menu-bar-switch"), &a.settingsForm.menuBar).Label(i18n.T("Show in the menu bar")).Changed() {
			on := a.settingsForm.menuBar
			a.updateSettings(func(s *settings.Settings) { s.General.MenuBar = on })
		}
	})
	settingRow(c, "close-to-tray", i18n.T("Keep running when the window closes"), i18n.T("Closing the window leaves Auralink in the menu bar, so its shortcuts keep working. Quit from the menu bar icon or with ⌘Q."), func() {
		sw := ui.Switch(c.Key("close-to-tray-switch"), &a.settingsForm.closeToTray).Label(i18n.T("Keep running when the window closes")).Disabled(!a.prefs.General.MenuBar)
		if sw.Changed() {
			on := a.settingsForm.closeToTray
			a.updateSettings(func(s *settings.Settings) { s.General.CloseToTray = on })
		}
	})
	settingRow(c, "open-at-login", i18n.T("Open at login"), "", func() {
		if ui.Switch(c.Key("open-at-login-switch"), &a.settingsForm.openAtLogin).Label(i18n.T("Open at login")).Changed() {
			on := a.settingsForm.openAtLogin
			a.updateSettings(func(s *settings.Settings) { s.General.LaunchAtLogin = on })
		}
	})

	sectionTitle(c, i18n.T("Getting started"))
	a.onboardingRow(c) // onboarding.go
}

// setMainLanguage sets the main language (a lang code, "" = the app's)
// and takes it off the other languages.
func (a *app) setMainLanguage(code string) {
	a.updateSettings(func(s *settings.Settings) {
		s.General.MainLanguage = code
		s.General.OtherLanguages = withoutLanguage(s.General.OtherLanguages, session.MainLanguage(*s, i18n.Locale()))
	})
}

// otherLanguagesRow is General → Other languages: the languages the user
// mixes in, as chips with ×, and Add (a menu of the languages left).
func (a *app) otherLanguagesRow(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	main := lang.Canonical(session.MainLanguage(a.prefs, i18n.Locale()))
	others := withoutLanguage(a.prefs.General.OtherLanguages, main)
	settingRow(c, "other-languages", i18n.T("Other languages"), i18n.T("Languages you mix into what you say and write. Clean-up keeps them as they are instead of translating them into your main language."), func() {
		for _, code := range others {
			label := langLabel(code)
			ui.Row(c.Key("other-lang-"+code)).Gap(px(t, 2)).AlignItems(ui.Center).Padding(px(t, 2), px(t, 2), px(t, 2), px(t, 10)).
				Radius(px(t, radiusControl)).Background(k.Hover).Shrink(0).Children(func() {
				ui.Text(c, label).FontSize(px(t, fsCaption)).SingleLine()
				b := ui.ButtonBase(c.Key("remove")).Size(px(t, 20), px(t, 20)).Center().Radius(px(t, 4)).Label(i18n.T("Remove %s", label)).Tooltip(i18n.T("Remove"))
				if b.Hovered() {
					b.Background(k.Pressed)
				}
				b.Children(func() { ui.Icon(c, icons.Close.Linear).Size(px(t, 12), px(t, 12)).TextColor(k.Text2) })
				if b.Clicked() {
					a.updateSettings(func(s *settings.Settings) { s.General.OtherLanguages = withoutLanguage(s.General.OtherLanguages, code) })
				}
			})
		}
		var left []string
		for _, code := range mainLanguages {
			if lang.Canonical(code) != main && !slices.ContainsFunc(others, func(o string) bool { return lang.Canonical(o) == lang.Canonical(code) }) {
				left = append(left, code)
			}
		}
		if len(left) == 0 {
			return
		}
		dropButton(c.Key("other-lang-add"), icons.Add, i18n.T("Add a language")).Shrink(0).Menu(func(m *ui.Menu) {
			for _, code := range left {
				if m.Item(langLabel(code)).Chosen() {
					a.updateSettings(func(s *settings.Settings) {
						if !slices.Contains(s.General.OtherLanguages, code) {
							s.General.OtherLanguages = append(slices.Clone(s.General.OtherLanguages), code)
						}
					})
				}
			}
		})
	})
}

// withoutLanguage is list without code (in any spelling: "zh", "zh-Hans").
func withoutLanguage(list []string, code string) []string {
	c := lang.Canonical(code)
	return slices.DeleteFunc(slices.Clone(list), func(x string) bool { return x == code || c != "" && lang.Canonical(x) == c })
}

func (a *app) appearanceSettings(c *ui.Context) {
	settingRow(c, "theme", i18n.T("Theme"), "", func() {
		if ui.Segmented(c.Key("theme-seg"), &a.theme, i18n.T("System"), i18n.T("Light"), i18n.T("Dark")).Label(i18n.T("Theme")).Changed() {
			theme := settings.Themes[a.theme]
			a.updateSettings(func(s *settings.Settings) { s.Appearance.Theme = theme })
		}
	})
	settingRow(c, "scale", i18n.T("Interface size"), i18n.T("Scales text and spacing."), func() {
		if ui.Segmented(c.Key("scale-seg"), &a.scale, scaleLabels...).Label(i18n.T("Interface size")).Changed() {
			scale := settings.Scales[a.scale]
			a.updateSettings(func(s *settings.Settings) { s.Appearance.Scale = scale })
		}
	})
	a.sidebarRow(c) // settings_appearance.go
	settingRow(c, "motion", i18n.T("Reduce motion"), i18n.T("Pages and panels change without animating. The system's setting is followed too."), func() {
		if ui.Switch(c.Key("motion-switch"), &a.reduceMotion).Label(i18n.T("Reduce motion")).Changed() {
			on := a.reduceMotion
			a.updateSettings(func(s *settings.Settings) { s.Appearance.ReduceMotion = on })
		}
	})
}
