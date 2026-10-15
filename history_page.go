package main

// The History page (/history, plan §2.3): finished sessions by feature,
// searchable, with their details.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
)

// historyState is the History page's view state.
type historyState struct{}

// historyPage builds the page.
func (a *app) historyPage(c *ui.Context) {
	pageHeader(c, i18n.T("History"), nil)
}

// historyChanged runs when a session kept (or changed) an entry: the page
// reads the history again when it shows.
func (a *app) historyChanged() {}
