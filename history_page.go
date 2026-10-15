package main

// The History page (/history, plan §2.3): finished sessions by feature
// (Transcribe, Translate, Rewrite), newest first, searchable (the store's
// full-text index), and the details of one in a side panel beside the
// list, or over it when the window is narrow: its texts to copy, where it
// went, the services and models, the time of each stage, why it failed,
// its recording in Finder, and Delete.
//
// The page keeps the entries of its tab and search in memory, read in the
// background when it shows, when the tab or the search changes, and when a
// session keeps an entry (historyChanged).

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/store"
)

// historyLimit is how many entries the list holds at most: the newest.
const historyLimit = 500

// Widths of the details panel and of the list beside it (DIPs at 100 %):
// under both, the panel shows over the list.
const (
	historyPanelW   = 320
	historyListMinW = 340
)

// historyState is the History page's view state.
type historyState struct {
	hist historyView
}

// historyView is the History page's state.
type historyView struct {
	tab   int    // the kind shown: an index in store.Kinds
	query string // the search field
	// list holds the entries read for listFor (the kind and the search),
	// "" before the first read; seq numbers the reads, so that only the
	// last one lands.
	list    []store.Entry
	listFor string
	seq     int
	loading bool
	stale   bool   // an entry changed while the page was not shown
	err     string // the last read's error
	sel     string // the entry in the details panel
	listUI  ui.ListState
	// The Delete dialog: open, and the entry it deletes.
	delAsk bool
	delID  string
}

// revealInFinder shows a file in Finder; tests replace it.
var revealInFinder = func(path string) { mygo.Shell.ShowItemInFolder(path) }

// historyKind is the kind of the tab shown.
func (h *historyView) kind() store.Kind {
	return store.Kinds[min(max(h.tab, 0), len(store.Kinds)-1)]
}

// wants says what the list should hold: the kind and the search.
func (h *historyView) wants() string {
	return string(h.kind()) + "\x00" + strings.TrimSpace(h.query)
}

// selected is the entry in the details panel, nil for none.
func (h *historyView) selected() *store.Entry {
	if h.sel == "" {
		return nil
	}
	for i := range h.list {
		if h.list[i].ID == h.sel {
			return &h.list[i]
		}
	}
	return nil
}

// loadHistory reads the entries of the tab and the search in the
// background.
func (a *app) loadHistory() {
	h := &a.hist
	h.seq++
	h.loading, h.stale = true, false
	seq, kind, search, want := h.seq, h.kind(), strings.TrimSpace(h.query), h.wants()
	hs := a.lib.store.History()
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		list, err := hs.List(ctx, store.HistoryQuery{Kind: kind, Search: search, Limit: historyLimit})
		a.post(func() {
			if seq != a.hist.seq {
				return // a newer read is on its way
			}
			h := &a.hist
			h.loading, h.listFor = false, want
			if err != nil {
				h.err = err.Error()
				return
			}
			h.list, h.err = list, ""
			if h.selected() == nil {
				h.sel = ""
			}
		})
	})
}

// historyChanged runs when a session kept (or changed) an entry: the page
// reads the history again now when it shows, else when it shows next.
func (a *app) historyChanged() {
	a.homeChanged() // home_page.go: today's numbers, the recent results
	if a.currentPage() == "history" {
		a.loadHistory()
		return
	}
	a.hist.stale = true
}

// historyShown runs when the router goes to the History page (pageShown).
func (a *app) historyShown() { a.loadHistory() }

// historyPage builds the page.
func (a *app) historyPage(c *ui.Context) {
	t := c.Theme()
	h := &a.hist
	// A read that pageShown did not start: the first, or after a change.
	if !h.loading && (h.listFor == "" || h.stale) {
		a.loadHistory()
	}
	pageHeader(c, i18n.T("History"), func() {
		if n := len(h.list); n > 0 {
			label := i18n.N(n, "%d entry", "%d entries", n)
			if n >= historyLimit {
				label = i18n.T("Newest %d", n)
			}
			caption(c, label).SingleLine()
		}
	})
	ui.Row(c).Padding(px(t, sp1), px(t, sp6), px(t, sp3)).Gap(px(t, sp2)).AlignItems(ui.Center).Shrink(0).Children(func() {
		tabs := []string{i18n.T("Transcribe"), i18n.T("Translate"), i18n.T("Rewrite")}
		if ui.Segmented(c.Key("hist-tab"), &h.tab, tabs...).Label(i18n.T("Feature")).Shrink(0).Changed() {
			h.sel = ""
			a.loadHistory()
		}
		if searchField(c, "hist-search", &h.query, i18n.T("Search"), px(t, 360)).Label(i18n.T("Search the history")).Changed() {
			a.loadHistory()
		}
	})
	if !a.prefs.Privacy.KeepHistory {
		ui.Column(c).Padding(0, px(t, sp6), px(t, sp3)).Shrink(0).Children(func() {
			notice(c, "hist-off", noticeInfo, i18n.T("History is off: new results are not kept."), "", func() {
				if ghostButton(c.Key("hist-privacy"), i18n.T("Privacy settings")).Clicked() {
					a.navigate("/settings/privacy")
				}
			}, nil)
		})
	}
	if h.err != "" {
		ui.Column(c).Padding(0, px(t, sp6), px(t, sp3)).Shrink(0).Children(func() {
			notice(c, "hist-err", noticeError, i18n.T("Could not read the history."), h.err, func() {
				if ghostButton(c.Key("hist-retry"), i18n.T("Try again")).Clicked() {
					a.loadHistory()
				}
			}, nil)
		})
	}
	list, sel := h.list, h.selected()
	w := px(t, historyPanelW)
	inline := a.pageWidth(c)-w >= px(t, historyListMinW)
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			switch {
			case h.listFor == "":
				// The first read is on its way: no empty state yet.
			case len(list) == 0:
				a.historyEmpty(c)
			default:
				c.After(time.Minute) // the times are relative
				ui.List(c.Key("hist-list"), &h.listUI, len(list)).Grow(1).MinHeight(0).Gap(px(t, 2)).Padding(0, 0, px(t, sp4)).
					ItemKey(func(i int) any { return list[i].ID }).Rows(func(r ui.ListRow) {
					a.historyRow(c, list[r.Index])
				})
			}
		})
		if sel != nil {
			closePanel := func() {
				if !h.delAsk { // a click in the Delete dialog is outside the drawer
					h.sel = ""
				}
			}
			sidePanel(c, "hist-detail", w, inline, i18n.T("Entry details"), closePanel, func() {
				a.historyDetail(c, *sel)
			})
		}
	})
	a.historyDeleteDialog(c)
}

// historyEmpty is the list's empty state: nothing kept yet for the tab,
// or nothing matching the search.
func (a *app) historyEmpty(c *ui.Context) {
	h := &a.hist
	if q := strings.TrimSpace(h.query); q != "" {
		if r := []rune(q); len(r) > 40 {
			q = string(r[:40]) + "…"
		}
		emptyState(c, icons.Magnifer, i18n.T("Nothing matches “%s”", q),
			i18n.T("The search looks in what was said, selected and written."), func() {
				if ghostButton(c.Key("hist-clear-search"), i18n.T("Clear the search")).Clicked() {
					h.query = ""
					a.loadHistory()
				}
			})
		return
	}
	switch h.kind() {
	case store.KindTranslate:
		emptyState(c, translateIcon, i18n.T("No translations yet"),
			i18n.T("Select text, or speak, and press your Translate shortcut: each translation shows up here with its source."), func() {
				if ghostButton(c.Key("hist-go-translate"), i18n.T("Set up Translate")).Clicked() {
					a.navigate("/translate")
				}
			})
	case store.KindRewrite:
		emptyState(c, rewriteIcon, i18n.T("No rewrites yet"),
			i18n.T("Press your Rewrite shortcut and say what to write, or how to change the selected text: each answer shows up here with your instruction."), func() {
				if ghostButton(c.Key("hist-go-rewrite"), i18n.T("Set up Rewrite")).Clicked() {
					a.navigate("/rewrite")
				}
			})
	default:
		emptyState(c, icons.History, i18n.T("No transcriptions yet"),
			i18n.T("Press your Transcribe shortcut and speak: each result shows up here, to find and copy again."), func() {
				if ghostButton(c.Key("hist-go-transcribe"), i18n.T("Set up Transcribe")).Clicked() {
					a.navigate("/transcribe")
				}
			})
	}
}

// historyRow is an entry in the list: the first line of its result (why
// it failed, when it did), its marks, and when, in which app and how long
// it was spoken. A click shows its details, or hides them.
func (a *app) historyRow(c *ui.Context, e store.Entry) {
	t := c.Theme()
	k := tok(t)
	h := &a.hist
	title, failed := entryTitle(e)
	row := ui.ButtonBase(c.Key("hist-row-"+e.ID)).Padding(px(t, sp2), px(t, sp2)).Margin(0, px(t, sp4)).Radius(px(t, radiusControl)).
		Label(title).Gap(px(t, sp2)).AlignItems(ui.Start)
	switch {
	case h.sel == e.ID:
		row.Background(k.Pressed)
	case row.Hovered():
		row.Background(k.Hover)
	}
	if row.Clicked() {
		if h.sel == e.ID {
			h.sel = ""
		} else {
			h.sel = e.ID
		}
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 3)).Children(func() {
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				tx := ui.Text(c, title).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
				if failed {
					tx.TextColor(k.Text2)
				}
				entryMarks(c, e)
			})
			ui.Text(c, entryMeta(e)).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
		})
	})
}

// entryTitle is an entry's line in the list: the first line of its
// result, else of what was heard, else why it failed (failed true).
func entryTitle(e store.Entry) (title string, failed bool) {
	if s := firstLine(e.Result); s != "" {
		return s, false
	}
	if e.Status == store.StatusFailed && e.Error != "" {
		return firstLine(e.Error), true
	}
	if s := firstLine(e.RawText); s != "" {
		return s, false
	}
	if s := firstLine(e.SourceText); s != "" {
		return s, false
	}
	return i18n.T("No text"), true
}

// firstLine is the first line of s that is not blank, trimmed.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// entryMeta is an entry's second line: when, the app, how long it was
// spoken.
func entryMeta(e store.Entry) string {
	parts := []string{timeAgo(e.CreatedAt)} // shell_status.go
	if e.AppName != "" {
		parts = append(parts, e.AppName)
	}
	if e.TargetLang != "" {
		parts = append(parts, "→ "+targetLanguageLabel(e.TargetLang))
	}
	if e.Kind == store.KindRewrite && e.SourceText != "" {
		parts = append(parts, i18n.T("Selection"))
	}
	if e.AudioMS > 0 {
		parts = append(parts, clockMS(e.AudioMS))
	}
	return strings.Join(parts, " · ")
}

// entryMarks are an entry's status marks: failed or cancelled, or left
// in the answer card, or copied from it.
func entryMarks(c *ui.Context, e store.Entry) {
	k := tok(c.Theme())
	switch e.Status {
	case store.StatusFailed:
		tagChip(c, i18n.T("Failed")).TextColor(k.Danger)
		return
	case store.StatusCancelled:
		tagChip(c, i18n.T("Cancelled"))
		return
	}
	switch e.Delivery {
	case "card":
		tagChip(c, i18n.T("Not inserted")).Tooltip(i18n.T("Shown in the answer card, and not inserted"))
	case "copy":
		tagChip(c, i18n.T("Copied")).Tooltip(i18n.T("Copied from the answer card"))
	}
}

// targetLang is a translation's target language.
func targetLang(e store.Entry) string {
	if e.TargetLang == "" {
		return ""
	}
	return targetLanguageLabel(e.TargetLang)
}

// deliveryLabel says where a result went.
func deliveryLabel(e store.Entry) string {
	switch e.Delivery {
	case "insert":
		return i18n.T("Inserted")
	case "card":
		return i18n.T("Shown in the answer card")
	case "copy":
		return i18n.T("Copied")
	}
	return ""
}

// clockMS is a length as m:ss.
func clockMS(ms int64) string {
	s := (ms + 500) / 1000
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// stageTime is a stage's time: "850 ms", "1.2 s", "1:05"; "" for none.
func stageTime(ms int64) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 1000:
		return fmt.Sprintf("%d ms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1f s", float64(ms)/1000)
	}
	return clockMS(ms)
}

// entryService is a service and its model as the details show them: the
// service's name today, and the model it used then; "" for none.
func (a *app) entryService(kind providers.Kind, id, model string) string {
	if id == "" {
		return ""
	}
	p, _ := providers.Setup(a.prefs, kind, id)
	name := providerName(kind, p)
	if name == "" {
		name = id
	}
	if model != "" {
		name += " · " + model
	}
	return name
}

// historyDetail is the details panel of an entry.
func (a *app) historyDetail(c *ui.Context, e store.Entry) {
	t := c.Theme()
	k := tok(t)
	h := &a.hist
	ui.Column(c.Key("hist-detail-"+e.ID)).Padding(px(t, sp3), px(t, sp5), px(t, sp5)).Gap(px(t, sp3)).Children(func() {
		// The panel's header: its title, then Show in Finder (with a
		// recording), Delete and Close.
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			sectionHeading(c, i18n.T("Entry details")).Grow(1)
			if e.Audio != "" && iconButton(c.Key("hist-reveal"), icons.FolderOpen, i18n.T("Show the recording in Finder")).Clicked() {
				revealInFinder(filepath.Join(a.lib.store.History().AudioDir(), e.Audio))
			}
			if iconButton(c.Key("hist-delete"), icons.TrashBinMinimalistic, i18n.T("Delete the entry…")).Clicked() {
				h.delAsk, h.delID = true, e.ID
			}
			if iconButton(c.Key("panel-close"), icons.Close, i18n.T("Close")).Clicked() {
				h.sel = ""
			}
		})
		if e.Error != "" {
			kind, text := noticeError, i18n.T("Failed: %s", firstLine(e.Error))
			if e.Status != store.StatusFailed {
				kind, text = noticeWarn, i18n.T("Done with a problem: %s", firstLine(e.Error))
			}
			notice(c, "hist-detail-err", kind, text, e.Error, nil, nil)
		}
		// The texts there are, each with Copy.
		textBlock := func(key, title, copyLabel, text string) {
			text = strings.TrimSpace(text)
			if text == "" {
				return
			}
			ui.Column(c.Key(key)).Gap(px(t, sp1)).Children(func() {
				ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).MinHeight(px(t, 28)).Children(func() {
					sectionTitleText(c, title).Grow(1).MinWidth(0)
					if iconButton(c.Key(key+"-copy"), icons.Copy, copyLabel).Clicked() {
						mygoClipboard(c, text)
						a.toast(i18n.T("Copied"))
					}
				})
				fillCard(c).Padding(px(t, sp2), px(t, sp3)).Children(func() {
					ui.Text(c, text).Selectable()
				})
			})
		}
		textBlock("hist-result", i18n.T("Result"), i18n.T("Copy the result"), e.Result)
		if e.PromptText != "" {
			textBlock("hist-prompt", i18n.T("Instruction"), i18n.T("Copy the instruction"), e.PromptText)
		}
		if e.SourceText != "" {
			textBlock("hist-source", i18n.T("Selected text"), i18n.T("Copy the selected text"), e.SourceText)
		}
		textBlock("hist-raw", i18n.T("Recognised text"), i18n.T("Copy the recognised text"), e.RawText)

		// The facts: labels as wide as the longest, values in the rest.
		app := e.AppName
		if e.URLHost != "" {
			app += " · " + e.URLHost
		}
		details := [][2]string{{i18n.T("Time"), i18n.DateTime(e.CreatedAt)}, {i18n.T("App"), app}}
		if e.Kind == store.KindTranslate {
			details = append(details, [2]string{i18n.T("Target language"), targetLang(e)})
		}
		details = append(details,
			[2]string{i18n.T("Delivery"), deliveryLabel(e)},
			[2]string{i18n.T("Speech recognition"), a.entryService(providers.ASR, e.ASRProvider, e.ASRModel)},
			[2]string{i18n.T("Language model"), a.entryService(providers.LLM, e.LLMProvider, e.LLMModel)},
			[2]string{i18n.T("App rule"), ruleText(e)})
		sections := []struct {
			title string
			rows  [][2]string
		}{
			{i18n.T("Details"), details},
			{i18n.T("Timing"), [][2]string{
				{i18n.T("Recording"), stageTime(e.AudioMS)},
				{i18n.T("Recognition"), stageTime(e.ASRMS)},
				{i18n.T("Language model"), stageTime(e.LLMMS)},
				{i18n.T("Total"), stageTime(e.TotalMS)},
			}},
		}
		sectionGap := [2]float32{sp1, sp4} // above the first heading, above the others
		ui.Grid(c.Key("hist-facts")).ColumnTracks(ui.FitContent(), ui.Fr(1)).GapX(px(t, sp3)).GapY(px(t, sp1)).Children(func() {
			for i, sec := range sections {
				sectionTitleText(c, sec.title).ColumnSpan(2).Margin(px(t, sectionGap[min(i, 1)]), 0, px(t, 2), 0)
				for _, r := range sec.rows {
					ui.Text(c, r[0]).FontSize(px(t, fsCaption)).TextColor(k.Text2)
					v := ui.Text(c.Key(fmt.Sprintf("hist-fact-%d-%s", i, r[0])), orDash(r[1])).FontSize(px(t, fsCaption)).Selectable().MinWidth(0)
					if r[1] == "" {
						v.TextColor(k.Text3)
					}
				}
			}
		})
	})
}

// ruleText is an entry's app enhancement group and how it matched:
// "Team chat (app)"; "" for none.
func ruleText(e store.Entry) string {
	if e.RuleName == "" {
		return ""
	}
	if how := ruleSourceLabel(e.RuleSource); how != "" { // appenhance_page.go
		return i18n.T("%s (%s)", e.RuleName, how)
	}
	return e.RuleName
}

// historyDeleteDialog confirms Delete.
func (a *app) historyDeleteDialog(c *ui.Context) {
	h := &a.hist
	if !h.delAsk {
		return
	}
	dialogFrame(c, "hist-del", &h.delAsk, i18n.T("Delete this entry?"), "", 420, func() {
		ui.Text(c, i18n.T("Its texts and its recording are deleted. This cannot be undone."))
	}, func() {
		ui.Spacer(c)
		if ui.Button(c.Key("hist-del-cancel"), i18n.T("Cancel")).AutoFocus().Clicked() {
			h.delAsk = false
		}
		if recordButton(c.Key("hist-del-ok"), i18n.T("Delete")).Clicked() {
			h.delAsk = false
			a.deleteHistoryEntry(h.delID)
		}
	})
}

// deleteHistoryEntry deletes an entry and its recording, in the
// background, then reads the list again.
func (a *app) deleteHistoryEntry(id string) {
	if id == "" {
		return
	}
	hs := a.lib.store.History()
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := hs.Delete(ctx, id)
		a.post(func() {
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				a.toast(i18n.T("Could not delete the entry: %v", err))
				return
			}
			if a.hist.sel == id {
				a.hist.sel = ""
			}
			a.toast(i18n.T("Entry deleted"))
			a.loadHistory()
		})
	})
}
