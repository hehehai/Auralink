package main

// The context menu of a history entry, on History's rows and Home's recent
// results: Copy (its result), Show details, and Delete (confirmed by
// historyDeleteDialog). On History a focused row takes ⌘C and ⌫ too.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/store"
)

// entryCopyText is what Copy puts on the clipboard: the entry's result (as
// the details' "Copy the result" and Home's copy button), "" for none.
func entryCopyText(e store.Entry) string { return strings.TrimSpace(e.Result) }

// entryMenu gives row an entry's context menu; details shows the entry's
// details, as a click on the row does. keys adds ⌘C and ⌫ while the row
// has the focus, and shows them by the items.
func (a *app) entryMenu(c *ui.Context, row ui.Element, e store.Entry, keys bool, details func()) {
	text := entryCopyText(e)
	row.ContextMenu(func(m *ui.Menu) {
		cp := m.Item(i18n.T("Copy")).Disabled(text == "")
		if keys {
			cp.Shortcut(ui.Cmd, ui.KeyC)
		}
		if cp.Chosen() {
			a.copyEntry(c, text)
		}
		if m.Item(i18n.T("Show details")).Chosen() {
			details()
		}
		m.Separator()
		del := m.Item(i18n.T("Delete"))
		if keys {
			del.Shortcut(0, ui.KeyBackspace)
		}
		if del.Chosen() {
			a.askDeleteEntry(e.ID)
		}
	})
	if !keys {
		return
	}
	// On macOS ⌘C is the Edit menu's Copy, which comes as the command
	// "copy" to the focused element; elsewhere (and in tests) as a key.
	svc := c.Services()
	row.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputCommand || ev.Text != "copy" || text == "" {
			return false
		}
		svc.WriteClipboard(text)
		a.toast(i18n.T("Copied"))
		return true
	})
	if row.Shortcut(ui.Cmd, ui.KeyC) && text != "" {
		a.copyEntry(c, text)
	}
	if row.Shortcut(0, ui.KeyBackspace) || row.Shortcut(0, ui.KeyDelete) {
		a.askDeleteEntry(e.ID)
	}
}

// copyEntry copies an entry's text, as the details' copy buttons do.
func (a *app) copyEntry(c *ui.Context, text string) {
	mygoClipboard(c, text)
	a.toast(i18n.T("Copied"))
}

// askDeleteEntry opens the Delete dialog (historyDeleteDialog) for an
// entry.
func (a *app) askDeleteEntry(id string) {
	a.hist.delAsk, a.hist.delID = true, id
}
