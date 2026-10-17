package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/store"
	"github.com/hehehai/Auralink/internal/uitext"
)

// rightClick opens the context menu of the element showing label.
func rightClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	frames(tt)
	if err := tt.RightClick(label); err != nil {
		t.Fatalf("right-click %q: %v (texts: %q)", label, err, firstTexts(tt))
	}
	frames(tt)
	if tt.Menu() == nil {
		t.Fatalf("right-click %q: no menu", label)
	}
}

// choose chooses a context menu's item and runs the frames after it.
func choose(t *testing.T, tt *ui.Tester, item string) {
	t.Helper()
	if err := tt.ChooseMenuItem(item); err != nil {
		t.Fatalf("choose %q: %v (menu %q)", item, err, tt.Menu())
	}
	frames(tt)
}

// countText counts the texts on screen that are s.
func countText(tt *ui.Tester, s string) int {
	frames(tt)
	n := 0
	for _, x := range tt.Texts() {
		if x == s {
			n++
		}
	}
	return n
}

// focusRow clicks a History row, which takes the focus (and shows its
// details).
func focusRow(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	click(t, tt, label)
	if !tt.Focused(label) {
		t.Fatalf("%q is not focused", label)
	}
}

const insertedResult = "Let's move the design review to Thursday afternoon.\nI'll send the new agenda tonight."

func TestHistoryMenuItems(t *testing.T) {
	a, _, tt := newTestApp(t)
	seedHistory(t, a)
	a.navigate("/history")
	rightClick(t, tt, "Let's move the design review to Thursday afternoon.")
	if got, want := tt.Menu(), []string{"Copy", "Show details", "-", "Delete"}; !slices.Equal(got, want) {
		t.Errorf("menu %q, want %q", got, want)
	}
	tt.CloseMenu()
	if a.hist.sel != "" || a.hist.delAsk {
		t.Errorf("a right-click selected %q or asked %v", a.hist.sel, a.hist.delAsk)
	}
}

func TestHistoryMenuCopy(t *testing.T) {
	a, _, tt := newTestApp(t)
	seedHistory(t, a)
	a.navigate("/history")
	chips := countText(tt, "Copied") // the copied entry's mark
	rightClick(t, tt, "Let's move the design review to Thursday afternoon.")
	choose(t, tt, "Copy")
	if got := tt.Clipboard(); got != insertedResult {
		t.Errorf("clipboard %q", got)
	}
	if n := countText(tt, "Copied"); n != chips+1 {
		t.Errorf("no toast: %d texts \"Copied\", %d before", n, chips)
	}
	if a.hist.sel != "" {
		t.Errorf("Copy selected %q", a.hist.sel)
	}
	// A failed entry has no result: Copy is disabled.
	tt.SetClipboard("before")
	rightClick(t, tt, "doubao: network: no result within 30s")
	if err := tt.ChooseMenuItem("Copy"); err == nil {
		t.Error("Copy is enabled on an entry without a result")
	}
	tt.CloseMenu()
	frames(tt)
	if tt.Clipboard() != "before" {
		t.Errorf("clipboard %q", tt.Clipboard())
	}
}

func TestHistoryMenuDetails(t *testing.T) {
	a, _, tt := newTestApp(t)
	seedHistory(t, a)
	a.navigate("/history")
	rightClick(t, tt, "明天上午十点的站会改到线上，链接我稍后发到群里。")
	choose(t, tt, "Show details")
	if a.hist.sel != histCard || !has(tt, "Entry details") {
		t.Fatalf("details of %q: %q", a.hist.sel, firstTexts(tt))
	}
	// On the selected entry it keeps the details open (a click would close them).
	rightClick(t, tt, "Hi Anna, thanks for the quick turnaround on the contract. The changes look good to me.")
	choose(t, tt, "Show details")
	if a.hist.sel != histCopied {
		t.Fatalf("selected %q", a.hist.sel)
	}
	rightClick(t, tt, "Hi Anna, thanks for the quick turnaround on the contract. The changes look good to me.")
	choose(t, tt, "Show details")
	if a.hist.sel != histCopied {
		t.Errorf("Show details on the shown entry closed it: %q", a.hist.sel)
	}
}

// putAudio writes the recording of histInserted.
func putAudio(t *testing.T, a *app) string {
	t.Helper()
	dir := a.lib.store.History().AudioDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, histInserted+".wav")
	if err := os.WriteFile(path, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHistoryMenuDelete(t *testing.T) {
	a, _, tt := newTestApp(t)
	seedHistory(t, a)
	audio := putAudio(t, a)
	a.navigate("/history")
	a.hist.sel = histInserted // its details show
	frames(tt)

	// Cancel: nothing changes.
	rightClick(t, tt, "Let's move the design review to Thursday afternoon.")
	choose(t, tt, "Delete")
	if !a.hist.delAsk || a.hist.delID != histInserted || !has(tt, "Delete this entry?") {
		t.Fatalf("no confirmation: %v %q", a.hist.delAsk, firstTexts(tt))
	}
	click(t, tt, "Cancel")
	if _, err := a.lib.store.History().Get(context.Background(), histInserted); err != nil {
		t.Fatalf("Cancel deleted it: %v", err)
	}
	if _, err := os.Stat(audio); err != nil {
		t.Fatalf("Cancel deleted the recording: %v", err)
	}
	if a.hist.sel != histInserted || len(a.hist.list) != 4 {
		t.Errorf("after Cancel: selected %q, %v", a.hist.sel, historyIDs(a))
	}

	// Delete: the entry, its recording, and its details go.
	rightClick(t, tt, "Let's move the design review to Thursday afternoon.")
	choose(t, tt, "Delete")
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	frames(tt)
	if _, err := a.lib.store.History().Get(context.Background(), histInserted); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	if _, err := os.Stat(audio); !os.IsNotExist(err) {
		t.Errorf("the recording is still there: %v", err)
	}
	if a.hist.sel != "" || len(a.hist.list) != 3 || has(tt, "Let's move the design review to Thursday afternoon.") {
		t.Errorf("after Delete: selected %q, %v", a.hist.sel, historyIDs(a))
	}
	if slices.Contains(a.recent, insertedResult) {
		t.Errorf("the menu bar still lists it: %q", a.recent)
	}
}

// Deleting another entry than the one shown keeps the details.
func TestHistoryMenuDeleteOther(t *testing.T) {
	a, _, tt := newTestApp(t)
	seedHistory(t, a)
	a.navigate("/history")
	a.hist.sel = histCard
	frames(tt)
	rightClick(t, tt, "Hi Anna, thanks for the quick turnaround on the contract. The changes look good to me.")
	choose(t, tt, "Delete")
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	frames(tt)
	if a.hist.sel != histCard || len(a.hist.list) != 3 {
		t.Errorf("after Delete: selected %q, %v", a.hist.sel, historyIDs(a))
	}
}

func TestHistoryRowKeys(t *testing.T) {
	a, _, tt := newTestApp(t)
	seedHistory(t, a)
	a.navigate("/history")
	frames(tt)
	focusRow(t, tt, "Let's move the design review to Thursday afternoon.")
	tt.Key(ui.Cmd, ui.KeyC)
	frames(tt)
	if got := tt.Clipboard(); got != insertedResult {
		t.Errorf("⌘C copied %q", got)
	}
	// macOS: the Edit menu's Copy.
	tt.SetClipboard("")
	tt.Command("copy")
	frames(tt)
	if got := tt.Clipboard(); got != insertedResult {
		t.Errorf("Edit → Copy copied %q", got)
	}
	tt.Key(0, ui.KeyBackspace)
	frames(tt)
	if !a.hist.delAsk || a.hist.delID != histInserted {
		t.Errorf("⌫ asked %v for %q", a.hist.delAsk, a.hist.delID)
	}
	click(t, tt, "Cancel")
	// No result: ⌘C copies nothing.
	tt.SetClipboard("before")
	focusRow(t, tt, "doubao: network: no result within 30s")
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Command("copy")
	frames(tt)
	if tt.Clipboard() != "before" {
		t.Errorf("⌘C on a failed entry copied %q", tt.Clipboard())
	}
}

func TestHomeRecentMenu(t *testing.T) {
	a, tt := homeTester(t)
	seedHistory(t, a)
	seedWriteHistory(t, a)
	a.historyChanged()
	frames(tt)
	e := a.hm.recent[0]
	title, _ := uitext.EntryTitle(e)
	rightClick(t, tt, title)
	if got, want := tt.Menu(), []string{"Copy", "Show details", "-", "Delete"}; !slices.Equal(got, want) {
		t.Errorf("menu %q, want %q", got, want)
	}
	choose(t, tt, "Copy")
	if tt.Clipboard() != e.Result {
		t.Errorf("clipboard %q", tt.Clipboard())
	}
	// Delete, from Home: confirmed, then Home lists the others.
	rightClick(t, tt, title)
	choose(t, tt, "Delete")
	if !has(tt, "Delete this entry?") {
		t.Fatalf("no confirmation on Home: %q", firstTexts(tt))
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	frames(tt)
	if _, err := a.lib.store.History().Get(context.Background(), e.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	if a.router.Path() != "/home" || slices.ContainsFunc(a.hm.recent, func(r store.Entry) bool { return r.ID == e.ID }) {
		t.Errorf("after Delete: %s, recent %v", a.router.Path(), a.hm.recent)
	}
	// Show details opens it in History, as a click does.
	next := a.hm.recent[0]
	title, _ = uitext.EntryTitle(next)
	rightClick(t, tt, title)
	choose(t, tt, "Show details")
	if a.router.Path() != "/history" || a.hist.sel != next.ID || a.hist.kind() != next.Kind {
		t.Errorf("opened %s sel %q tab %s", a.router.Path(), a.hist.sel, a.hist.kind())
	}
}
