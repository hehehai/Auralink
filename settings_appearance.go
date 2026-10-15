package main

// Settings → Appearance: the sidebar's layout (from AudioSmith), with an
// automatic one: icons only while the window is narrow (shell_nav.go).

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/settings"
)

// sidebarRow chooses the sidebar's layout: automatic, names and icons,
// icons only.
func sidebarChoice(ap settings.Appearance) int {
	switch {
	case ap.Rail:
		return 2
	case ap.RailChosen:
		return 1
	}
	return 0
}

func (a *app) sidebarRow(c *ui.Context) {
	f := &a.settingsForm
	settingRow(c, "sidebar-style", i18n.T("Sidebar"), i18n.T("Icons only leaves more room for the pages; their names show when the pointer rests on them. Automatic shows icons only while the window is narrow."), func() {
		if ui.Segmented(c.Key("sidebar-seg"), &f.sidebar, i18n.T("Automatic"), i18n.T("Names and icons"), i18n.T("Icons only")).Label(i18n.T("Sidebar")).Changed() {
			if f.sidebar == 0 {
				a.updateSettings(func(s *settings.Settings) { s.Appearance.Rail, s.Appearance.RailChosen = false, false })
				return
			}
			a.setRail(f.sidebar == 2)
		}
	})
}
