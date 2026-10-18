package main

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/conformance"
	famruntime "github.com/hehehai/actnowkit/runtime"
	"github.com/hehehai/actnowkit/shortcuts"
)

// voxtNew is VoxtNew's release bundle id (the fixture's manifest points
// at a fake .app).
const voxtNew = "dev.hehehai.voxtnew"

// voxtOnFn is VoxtNew on fn, as copied from Auralink, set at t.
func voxtOnFn(t time.Time) shortcuts.Shortcut {
	return shortcuts.Shortcut{ID: "transcribe", Title: "Transcribe", Keys: "Fn", Trigger: shortcuts.Tap, Enabled: true,
		Mechanism: shortcuts.EventTap, Exclusive: true, EnabledAt: &t}
}

func conflictCards(a *app) []string {
	var out []string
	for _, n := range a.notices {
		if n.Link == linkShortcuts {
			out = append(out, n.Title+": "+n.Body)
		}
	}
	return out
}

// TestFamilyShortcutConflict: VoxtNew set fn before Auralink joined the
// family: Auralink, the later, announces it once (a card), and its rows
// say so, with the link to VoxtNew's settings (spec §10.4, §16.1
// scenario 7).
func TestFamilyShortcutConflict(t *testing.T) {
	fx := withFamily(t)
	fx.Register(voxtNew, voxtOnFn(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)))
	a, _, tt := newTestApp(t)
	drain := joinFamily(t, a)
	drain()
	cards := conflictCards(a)
	if len(cards) != 1 || cards[0] != "Shortcut also used by VoxtNew: fn is also the Transcribe shortcut in VoxtNew. Pressing it will trigger both apps." {
		t.Fatalf("cards %q", cards)
	}
	if !slices.Contains(a.prefs.Family.Prompted, "transcribe|"+voxtNew+"|transcribe") {
		t.Errorf("prompted %v", a.prefs.Family.Prompted)
	}
	a.navigate("/settings/shortcuts")
	frames(tt)
	for _, s := range []string{"Also used by VoxtNew (Transcribe)", "Open VoxtNew settings"} {
		if !has(tt, s) {
			t.Errorf("no %q: %q", s, tt.Texts())
		}
	}
	// Read again (activation, a notification): announced once only.
	a.dismissNotice(a.notices[0].ID)
	drain()
	a.familyRefresh()
	drain()
	if cards := conflictCards(a); len(cards) != 0 {
		t.Errorf("announced again: %q", cards)
	}
	// Moved to right ⌘: no conflict.
	a.updateSettings(func(s *settings.Settings) { s.Features.Transcribe.Hotkeys = []settings.Hotkey{{Keys: "RightCommand"}} })
	drain()
	frames(tt)
	if len(a.famConflicts) != 0 || has(tt, "Also used by VoxtNew (Transcribe)") {
		t.Errorf("conflicts %+v", a.famConflicts)
	}
	// Typing VoxtNew's keys says so before they are saved.
	a.navigate("/transcribe")
	frames(tt)
	r := &a.fui("transcribe").rows[0]
	r.custom, r.text, r.edited = true, "globe", true
	frames(tt)
	if !has(tt, "Also used by VoxtNew (Transcribe)") {
		t.Errorf("typing: %q", tt.Texts())
	}
}

// TestFamilyShortcutEarlier: Auralink set fn before VoxtNew: VoxtNew
// announces it; Auralink only warns on its rows.
func TestFamilyShortcutEarlier(t *testing.T) {
	fx := withFamily(t)
	a, _, tt := newTestApp(t)
	drain := joinFamily(t, a)
	drain()
	fx.Register(voxtNew, voxtOnFn(time.Now().Add(time.Hour)))
	drain() // the apps notification
	if len(a.famConflicts) != 1 || a.famConflicts[0].Prompt {
		t.Fatalf("conflicts %+v", a.famConflicts)
	}
	if cards := conflictCards(a); len(cards) != 0 {
		t.Errorf("cards %q", cards)
	}
	a.navigate("/transcribe")
	frames(tt)
	if !has(tt, "Also used by VoxtNew (Transcribe)") {
		t.Errorf("no warning: %q", tt.Texts())
	}
}

// fakePaste is a TextIO whose Paste records whether the family saw
// Auralink pasting, and when.
type fakePaste struct {
	fx       *conformance.Fixture
	flagged  bool
	at       time.Time
	deliverd int
}

func (f *fakePaste) Focus(context.Context) (proto.FocusResult, error) {
	return proto.FocusResult{}, nil
}
func (f *fakePaste) Paste(context.Context, proto.PasteParams) (proto.PasteResult, error) {
	r, err := famruntime.Read(f.fx.Family, testBundleID)
	f.flagged, f.at = err == nil && r.Pasting, time.Now()
	f.deliverd++
	return proto.PasteResult{Posted: true}, nil
}

// TestFamilyPaste: Auralink waits (at most 500 ms) while another app
// pastes, and flags its own paste (spec §11.1).
func TestFamilyPaste(t *testing.T) {
	fx := withFamily(t)
	a, _, _ := newTestApp(t)
	joinFamily(t, a)
	io := &fakePaste{fx: fx}
	var link atomic.Pointer[familyLink]
	link.Store(a.fam)
	g := pasteGuard{TextIO: io, link: &link}
	if err := famruntime.Write(fx.Family, famruntime.File{BundleID: voxtNew, Pasting: true}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	go func() {
		time.Sleep(150 * time.Millisecond)
		famruntime.Write(fx.Family, famruntime.File{BundleID: voxtNew})
	}()
	if _, err := g.Paste(context.Background(), proto.PasteParams{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if waited := io.at.Sub(start); waited < 100*time.Millisecond || waited > 450*time.Millisecond {
		t.Errorf("waited %v for VoxtNew's paste", waited)
	}
	if !io.flagged {
		t.Error("Auralink's paste was not flagged")
	}
	if r, err := famruntime.Read(fx.Family, testBundleID); err != nil || r.Pasting {
		t.Errorf("after the paste: %+v %v", r, err)
	}
	// Another app that never finishes: Auralink pastes after 500 ms.
	famruntime.Write(fx.Family, famruntime.File{BundleID: voxtNew, Pasting: true})
	start = time.Now()
	g.Paste(context.Background(), proto.PasteParams{Text: "hi"})
	if waited := io.at.Sub(start); waited < famruntime.PasteWait || waited > 2*famruntime.PasteWait {
		t.Errorf("waited %v", waited)
	}
	// Outside the family, the helper's paste alone.
	link.Store(nil)
	g.Paste(context.Background(), proto.PasteParams{Text: "hi"})
	if io.deliverd != 3 {
		t.Errorf("%d pastes", io.deliverd)
	}
	a.quit()
	if _, err := famruntime.Read(fx.Family, testBundleID); err == nil {
		t.Error("the runtime file outlives the app")
	}
}

// TestFamilyConformanceShortcuts runs actnowkit's ShortcutConflict
// scenario (spec §16.1 scenario 7) against Auralink.
func TestFamilyConformanceShortcuts(t *testing.T) {
	for _, sc := range conformance.Scenarios {
		if sc.Name == "ShortcutConflict" {
			sc.Run(t, conformance.New(t), newFamilyAdapter)
			return
		}
	}
	t.Fatal("no ShortcutConflict scenario")
}

// Declare gives the app under test the scenario's shortcuts: as
// Transcribe's (the others off), set in the settings as the user would.
func (f *familyAdapter) Declare(_ context.Context, sc []shortcuts.Shortcut) error {
	var hs []settings.Hotkey
	for _, s := range sc {
		trig := settings.TriggerTap
		switch s.Trigger {
		case shortcuts.Hold:
			trig = settings.TriggerLong
		case shortcuts.Double:
			trig = settings.TriggerDouble
		}
		hs = append(hs, settings.Hotkey{Keys: strings.ReplaceAll(s.Keys, "Enter", "Return"), Trigger: trig})
	}
	f.a.updateSettings(func(s *settings.Settings) {
		s.Features.Transcribe.Hotkeys, s.Features.Translate.Hotkeys, s.Features.Rewrite.Hotkeys = hs, nil, nil
		s.Features.PasteLast = settings.Hotkey{}
	})
	f.drain()
	return nil
}

func (f *familyAdapter) Conflicts(context.Context) ([]string, error) {
	f.a.familyRefresh()
	f.drain()
	var out []string
	for _, c := range f.a.famConflicts {
		out = append(out, c.Other.BundleID)
	}
	return out, nil
}
