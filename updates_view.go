package main

// The updater's views (updates.go): the sidebar's "update available" line
// (a button on the rail), the update panel, Settings → About → Updates,
// and the changelog's history.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/uitext"
	akup "github.com/hehehai/actnowkit/updates"
)

// downloadURL is where a copy that cannot update itself gets the newest.
const downloadURL = "https://actnow.dev/download/auralink"

// updateLine is the sidebar's line while there is something to do: an
// update to install, its download, the restart to finish it ("" none).
func (a *app) updateLine() string {
	if a.upd == nil || a.updSt.Release == nil {
		return ""
	}
	switch a.updSt.Phase {
	case akup.Available:
		return i18n.T("Update available")
	case akup.Downloading:
		return i18n.T("Updating… %s", updatePercent(a.updSt.Fraction()))
	case akup.Ready:
		return i18n.T("Restart to update")
	}
	return ""
}

// updateEntry is the update line at the sidebar's foot, above Settings: a
// click opens the panel.
func (a *app) updateEntry(c *ui.Context) {
	text := a.updateLine()
	if text == "" {
		return
	}
	t := c.Theme()
	k := tok(t)
	tip := i18n.T("Auralink %s is available", a.updSt.Release.Version)
	b := ghostBase(c.Key("nav-update")).Padding(px(t, 6), px(t, sp2)).Gap(px(t, sp2)).Justify(ui.Start).Label(text).Tooltip(tip)
	b.Children(func() {
		ui.Icon(c, icons.DownloadMinimalistic.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.AccentInk)
		ui.Text(c, text).TextColor(k.AccentInk).FontWeight(500).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		ui.Box(c).Size(px(t, 7), px(t, 7)).Radius(px(t, 4)).Background(k.Accent).Shrink(0)
	})
	if b.Clicked() {
		a.updPanel = true
	}
}

// updateRailButton is the update line on the rail: the download icon with
// a dot.
func (a *app) updateRailButton(c *ui.Context) {
	text := a.updateLine()
	if text == "" {
		return
	}
	a.railButton(c, "rail-update", icons.DownloadMinimalistic, text, text, false, "update", func() { a.updPanel = true })
}

// updatePercent is a fraction as "42%".
func updatePercent(f float64) string { return fmt.Sprintf("%d%%", int(f*100+0.5)) }

// updatePanel is the update panel (a dialog): the version, its notes, and
// what can be done in the phase it is in.
func (a *app) updatePanel(c *ui.Context) {
	if !a.updPanel || a.upd == nil {
		return
	}
	t := c.Theme()
	st := a.updSt
	title, sub := i18n.T("Software update"), ""
	if st.Release != nil {
		title = i18n.T("Auralink %s is available", st.Release.Version)
		sub = i18n.T("You have %s", uitext.OrDash(a.version))
		if !st.Release.Date.IsZero() {
			sub += " · " + i18n.Date(st.Release.Date)
		}
		switch st.Phase {
		case akup.Downloading:
			title = i18n.T("Downloading Auralink %s", st.Release.Version)
		case akup.Ready:
			title = i18n.T("Auralink %s is installed", st.Release.Version)
		}
	}
	open := true
	dialogFrame(c, "upd-panel", &open, title, sub, px(t, 560), func() { a.updatePanelBody(c) }, func() { a.updatePanelFooter(c) })
	if !open {
		a.updPanel = false
		if st.Phase == akup.UpToDate || st.Phase == akup.Failed {
			a.upd.Dismiss()
		}
	}
}

func (a *app) updatePanelBody(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	st := a.updSt
	switch st.Phase {
	case akup.Checking, akup.Idle:
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, i18n.T("Checking for updates…")).TextColor(k.Text2)
		})
		return
	case akup.UpToDate:
		ui.Text(c.Key("upd-uptodate"), i18n.T("Auralink is up to date (%s)", uitext.OrDash(a.version))).TextColor(k.Text2)
		return
	case akup.Disabled:
		ui.Text(c, i18n.T("This copy cannot update itself: it is a development build, or it is in a folder Auralink cannot write to. Move Auralink to Applications.")).TextColor(k.Text2)
		ui.Link(c, i18n.T("Download the latest version from actnow.dev"), downloadURL).FontSize(px(t, fsCaption))
		return
	case akup.Failed:
		msg := i18n.T("The update did not install: %s. Auralink is unchanged.", updateErrText(st.Err))
		if st.Step == "check" {
			msg = i18n.T("Cannot check for updates: %s", updateErrText(st.Err))
		}
		notice(c, "upd-failed", noticeError, msg, "", nil, nil)
		if st.Release == nil {
			return
		}
	}
	switch st.Phase {
	case akup.Downloading:
		ui.Column(c.Key("upd-progress")).Gap(px(t, sp1)).Children(func() {
			ui.Progress(c.Key("upd-bar"), st.Fraction()).Label(i18n.T("Downloading the update"))
			line := i18n.T("Downloading… %s", updatePercent(st.Fraction()))
			if st.Total > 0 {
				line += " · " + uitext.HumanBytes(st.Downloaded) + " / " + uitext.HumanBytes(st.Total)
			}
			caption(c, line)
		})
	case akup.Ready:
		ui.Text(c.Key("upd-ready"), i18n.T("The new version runs once Auralink restarts.")).TextColor(k.Text2)
	}
	if st.Release == nil {
		return
	}
	if i18n.Locale() == "zh-Hans" && st.NotesLocale == "en" {
		caption(c.Key("upd-en"), i18n.T("These notes are not translated yet; here they are in English."))
	}
	notes := st.Notes
	if strings.TrimSpace(notes) == "" {
		notes = i18n.T("No notes for this version.")
	}
	ui.Column(c.Key("upd-notes")).Margin(px(t, sp1), 0, 0).Children(func() {
		markdown.View(c.Key("upd-md"), notes, &a.updMd)
	})
}

func (a *app) updatePanelFooter(c *ui.Context) {
	st := a.updSt
	ui.Spacer(c)
	switch st.Phase {
	case akup.Available:
		if ghostButton(c.Key("upd-later"), i18n.T("Later")).Clicked() {
			a.updPanel = false
		}
		if primaryButton(c.Key("upd-install"), i18n.T("Download and Install")).Clicked() {
			a.installUpdate()
		}
	case akup.Downloading:
		if ghostButton(c.Key("upd-hide"), i18n.T("Hide")).Clicked() {
			a.updPanel = false
		}
	case akup.Ready:
		if ghostButton(c.Key("upd-later"), i18n.T("Later")).Clicked() {
			a.updPanel = false
		}
		if primaryButton(c.Key("upd-restart"), i18n.T("Restart to Finish the Update")).Clicked() {
			a.restartForUpdate()
		}
	case akup.Failed:
		if ghostButton(c.Key("upd-close"), i18n.T("Close")).Clicked() {
			a.updPanel = false
			a.upd.Dismiss()
		}
		if primaryButton(c.Key("upd-retry"), i18n.T("Try again")).Clicked() {
			a.upd.Dismiss()
			a.checkForUpdates()
		}
	default:
		if ui.Button(c.Key("upd-ok"), i18n.T("OK")).Clicked() {
			a.updPanel = false
			a.upd.Dismiss()
		}
	}
}

// updateSettingsSection is the Updates part of Settings → About: check
// now, check automatically, beta updates, install without asking, the
// changelog.
func (a *app) updateSettingsSection(c *ui.Context) {
	if a.upd == nil {
		return
	}
	st := a.updSt
	sectionTitle(c, i18n.T("Updates"))
	if a.bundleUpdated || st.Phase == akup.Ready {
		settingRowNote(c, "about-relaunch", i18n.T("Update installed"), i18n.T("The new version runs once Auralink restarts."), func() {
			if primaryButton(c.Key("about-relaunch"), i18n.T("Restart Auralink")).Clicked() {
				a.restartForUpdate()
			}
		})
	}
	last := a.prefs.Updates.LastCheck
	if st.CheckedAt.After(last) {
		last = st.CheckedAt
	}
	desc := ""
	switch {
	case !a.updEnabled():
		desc = i18n.T("This copy cannot update itself: it is a development build, or it is in a folder Auralink cannot write to. Move Auralink to Applications.")
	case st.Phase == akup.Checking:
		desc = i18n.T("Checking for updates…")
	case st.Phase == akup.Available && st.Release != nil:
		desc = i18n.T("Auralink %s is available", st.Release.Version)
	case last.IsZero():
		desc = i18n.T("New versions come from actnow.dev, with their notes. Not checked yet.")
	default:
		desc = i18n.T("New versions come from actnow.dev, with their notes. Last checked %s.", uitext.TimeAgo(last))
	}
	settingRowNote(c, "about-update", i18n.T("Check for updates"), desc, func() {
		if ui.Button(c.Key("about-update"), i18n.T("Check for Updates…")).Disabled(st.Phase == akup.Checking).Clicked() {
			a.checkForUpdates()
		}
	})
	if a.updEnabled() {
		auto := a.prefs.Updates.AutoCheckOn()
		settingRow(c, "about-update-auto", i18n.T("Check automatically"), i18n.T("Once a day, in the background. A new version shows at the foot of the sidebar with its notes; it installs when you say so."), func() {
			if ui.Switch(c.Key("about-update-auto-switch"), &auto).Label(i18n.T("Check for updates automatically")).Changed() {
				a.setAutoCheck(auto)
			}
		})
		beta := a.betaOn()
		note := i18n.T("Releases only. Turning it on offers betas too: new features sooner, and more bugs.")
		switch {
		case beta:
			note = i18n.T("Betas and releases, whichever is newer. Betas bring new features sooner, and more bugs.")
		case strings.Contains(a.version, "-"):
			note = i18n.T("Releases only. This beta stays until a release newer than it comes out: Auralink never goes back to an older version.")
		}
		settingRowNote(c, "about-update-beta", i18n.T("Beta updates"), note, func() {
			if ui.Switch(c.Key("about-update-beta-switch"), &beta).Label(i18n.T("Get beta versions")).Changed() {
				a.setBeta(beta)
			}
		})
		install := a.prefs.Updates.AutoInstall
		settingRow(c, "about-update-install", i18n.T("Install updates automatically"), i18n.T("Updates found in the background download and install without asking; Auralink then asks to restart. Off by default."), func() {
			if ui.Switch(c.Key("about-update-install-switch"), &install).Label(i18n.T("Install updates without asking")).Disabled(!auto).Changed() {
				a.setAutoInstall(install)
			}
		})
	}
	settingRow(c, "about-changelog-history", i18n.T("Changelog"), i18n.T("What changed in each version, in Auralink's language."), func() {
		if ui.Button(c.Key("about-changelog-history"), i18n.T("Show Changelog…")).Clicked() {
			a.openHistory()
		}
	})
}

// historyDialog is the changelog: every version, newest first, its notes
// in Auralink's language (English where one has no translation).
func (a *app) historyDialog(c *ui.Context) {
	if !a.clogOpen {
		return
	}
	t := c.Theme()
	k := tok(t)
	dialogFrame(c, "upd-history", &a.clogOpen, i18n.T("Changelog"), i18n.T("Auralink · %s", i18n.Name(i18n.Locale())), px(t, 620), func() {
		switch {
		case a.clogLoading && a.clog == nil:
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, i18n.T("Loading…")).TextColor(k.Text2)
			})
			return
		case a.clogErr != "" && a.clog == nil:
			notice(c, "hist-err", noticeError, i18n.T("The changelog could not be read: %s", a.clogErr), "", nil, nil)
			ui.Link(c, i18n.T("Read it on actnow.dev"), changelogURL).FontSize(px(t, fsCaption))
			return
		case a.clog == nil || len(a.clog.Entries) == 0:
			ui.Text(c, i18n.T("No versions published yet.")).TextColor(k.Text2)
			return
		}
		for i, e := range a.clog.Entries {
			if i > 0 {
				ui.Box(c).Height(1).Background(k.Line).Margin(px(t, sp3), 0)
			}
			ui.Column(c.Key("hist-" + e.Version)).Gap(px(t, sp2)).Children(func() {
				ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
					ui.Text(c, e.Version).FontSize(px(t, fsSection)).FontWeight(600)
					if e.Date != "" {
						caption(c, e.Date)
					}
					if e.Channel == "beta" {
						tagChip(c, i18n.T("Beta"))
					}
					if e.Version == a.version {
						tagChip(c, i18n.T("This version"))
					}
				})
				if i18n.Locale() == "zh-Hans" && e.Locale == "en" {
					caption(c, i18n.T("These notes are not translated yet; here they are in English."))
				}
				markdown.View(c.Key("hist-md-"+e.Version), e.Notes, &a.updMd)
			})
		}
	}, func() {
		ui.Link(c, i18n.T("On actnow.dev"), changelogURL).FontSize(px(t, fsCaption))
		ui.Spacer(c)
		if ui.Button(c.Key("hist-close"), i18n.T("Close")).Clicked() {
			a.clogOpen = false
		}
	})
}
