package main

// Settings → Appearance: the sidebar's layout (from AudioSmith).

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
)

// sidebarRow chooses the sidebar's layout.
func (a *app) sidebarRow(c *ui.Context) {
	rail := 0
	if a.prefs.Appearance.Rail {
		rail = 1
	}
	settingRow(c, "sidebar-style", i18n.T("Sidebar"), i18n.T("Icons only leaves more room for the pages; their names show when the pointer rests on them."), func() {
		if ui.Segmented(c.Key("sidebar-seg"), &rail, i18n.T("Names and icons"), i18n.T("Icons only")).Label(i18n.T("Sidebar")).Changed() {
			a.setRail(rail == 1)
		}
	})
}
