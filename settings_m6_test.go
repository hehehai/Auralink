package main

// Tests of the settings sections M6 added (Audio, Overlay style,
// Shortcuts, General's other languages and opening at login), the
// sidebar's automatic rail, the recording's sounds and the menu bar.

import (
	"bytes"
	"encoding/json"
	"image/png"
	"slices"
	"testing"

	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/macx"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

func TestAudioSettingsDevices(t *testing.T) {
	a, fs, tt := newTestApp(t)
	a.navigate("/settings/audio")
	if !slices.Contains(fs.methods(), proto.MethodCaptureDevices) {
		t.Fatalf("the section did not list the inputs: %v", fs.methods())
	}
	if !has(tt, "No microphone chosen: Auralink records from the system's default input.") || !has(tt, "MacBook Air Microphone") {
		t.Fatalf("empty list: %q", firstTexts(tt))
	}
	click(t, tt, "Add AirPods Pro")
	click(t, tt, "Add MacBook Air Microphone")
	got := a.settings.Get().Audio
	if !slices.Equal(got.Devices, []string{"AirPodsPro:input", "BuiltInMicrophoneDevice"}) || got.DeviceNames["AirPodsPro:input"] != "AirPods Pro" {
		t.Fatalf("added: %+v", got)
	}
	if !has(tt, "Records now") || !has(tt, "No other microphone is connected.") {
		t.Errorf("after adding: %q", firstTexts(tt))
	}
	click(t, tt, "Move MacBook Air Microphone up")
	if d := a.settings.Get().Audio.Devices; !slices.Equal(d, []string{"BuiltInMicrophoneDevice", "AirPodsPro:input"}) {
		t.Fatalf("moved: %q", d)
	}
	click(t, tt, "Remove MacBook Air Microphone")
	if d := a.settings.Get().Audio.Devices; !slices.Equal(d, []string{"AirPodsPro:input"}) {
		t.Fatalf("removed: %q", d)
	}
	// A device that is gone keeps its name, marked.
	fs.mu.Lock()
	fs.handlers[proto.MethodCaptureDevices] = func(json.RawMessage) (any, error) {
		return proto.DevicesResult{Devices: []proto.CaptureDevice{{ID: "BuiltInMicrophoneDevice", Name: "MacBook Air Microphone", Default: true}}}, nil
	}
	fs.mu.Unlock()
	click(t, tt, "Refresh the microphones")
	if !has(tt, "AirPods Pro") || !has(tt, "Not connected") || !has(tt, "Records now · System default") {
		t.Errorf("unplugged: %q", firstTexts(tt))
	}
	// Without the helper: a notice, no list.
	fs.set(helper.Status{State: helper.Stopped})
	if !has(tt, "The helper is not running: the microphones cannot be listed.") {
		t.Errorf("no helper: %q", firstTexts(tt))
	}
}

func TestDeviceInUse(t *testing.T) {
	devs := []proto.CaptureDevice{{ID: "mic"}, {ID: "usb"}, {ID: "def", Default: true}}
	for _, tc := range []struct {
		list []string
		want string
	}{
		{nil, "def"},
		{[]string{"gone", "usb"}, "usb"},
		{[]string{"gone"}, "def"},
		{[]string{"mic", "usb"}, "mic"},
	} {
		if got := deviceInUse(tc.list, devs); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.list, got, tc.want)
		}
	}
	if got := deviceInUse([]string{"x"}, nil); got != "" {
		t.Errorf("no devices: %q", got)
	}
}

func TestRecordingCues(t *testing.T) {
	snap := func(id string, st session.State) session.Snapshot { return session.Snapshot{ID: id, State: st} }
	for _, tc := range []struct {
		name    string
		was, s  session.Snapshot
		want    macx.Sound
		wantAny bool
	}{
		{"start", snap("", session.Idle), snap("a", session.Recording), macx.SoundStart, true},
		{"after reading", snap("a", session.Reading), snap("a", session.Recording), macx.SoundStart, true},
		{"a new session", snap("a", session.Recording), snap("b", session.Recording), macx.SoundStart, true},
		{"still recording", snap("a", session.Recording), snap("a", session.Recording), "", false},
		{"stop", snap("a", session.Recording), snap("a", session.Recognizing), macx.SoundStop, true},
		{"stop to compose", snap("a", session.Recording), snap("a", session.Composing), macx.SoundStop, true},
		{"cancelled", snap("a", session.Recording), snap("a", session.Cancelled), "", false},
		{"failed", snap("a", session.Recording), snap("a", session.Failed), "", false},
		{"reading a selection", snap("a", session.Reading), snap("a", session.Composing), "", false},
		{"recognized", snap("a", session.Recognizing), snap("a", session.Composing), "", false},
	} {
		got, ok := recordingCue(tc.was, tc.s)
		if got != tc.want || ok != tc.wantAny {
			t.Errorf("%s: %q %v", tc.name, got, ok)
		}
	}
}

func TestSessionSounds(t *testing.T) {
	a, _, _ := newTestApp(t)
	var cues []macx.Sound
	a.playSound = func(s macx.Sound) { cues = append(cues, s) }
	rev := a.sess.Rev
	feed := func(id string, st session.State) {
		rev++
		a.sessionChanged(session.Snapshot{Rev: rev, ID: id, Kind: store.KindTranscribe, State: st, Try: true})
	}
	feed("s1", session.Recording)
	feed("s1", session.Recognizing)
	feed("s1", session.Done)
	feed("s2", session.Recording)
	feed("s2", session.Cancelled)
	if want := []macx.Sound{macx.SoundStart, macx.SoundStop, macx.SoundStart}; !slices.Equal(cues, want) {
		t.Fatalf("cues %q, want %q", cues, want)
	}
	// Off in Settings → Audio: silence.
	a.navigate("/settings/audio")
	a.updateSettings(func(s *settings.Settings) { s.Audio.Sounds = false })
	cues = nil
	feed("s3", session.Recording)
	feed("s3", session.Recognizing)
	if len(cues) != 0 {
		t.Errorf("sounds off: %q", cues)
	}
}

func TestOverlayStyleSettings(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/settings/overlay")
	if !has(tt, "Preview") {
		t.Fatalf("section: %q", firstTexts(tt))
	}
	if _, ok := tt.Find("Preview of the overlay"); !ok {
		t.Error("no preview")
	}
	click(t, tt, "Top centre")
	if o := a.settings.Get().Overlay; o.Position != "top" || o.Opacity != 1 || o.Margin != 96 {
		t.Fatalf("top: %+v", o)
	}
	// A slider's change goes to the overlay and the settings.
	a.settingsForm.ovStyle.opacity = 0.73
	a.overlayEdited()
	if o := a.settings.Get().Overlay; o.Opacity != 0.73 || o.Position != "top" {
		t.Fatalf("opacity: %+v", o)
	}
	// The preview's pill has no buttons of its own.
	if _, ok := tt.Find("Stop"); ok {
		t.Error("the preview has a Stop button")
	}
	// A change saved elsewhere reaches the form.
	a.updateSettings(func(s *settings.Settings) { s.Overlay.Radius = 8 })
	if a.settingsForm.ovStyle.radius != 8 {
		t.Errorf("form radius %v", a.settingsForm.ovStyle.radius)
	}
}

func TestShortcutsSettings(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/settings/shortcuts")
	for _, s := range []string{"Transcribe", "Translate", "Rewrite", "Paste the last result", "fn ⇧", "⌃⌘V"} {
		if !has(tt, s) {
			t.Errorf("no %q: %q", s, firstTexts(tt))
		}
	}
	// A conflict shows on both rows.
	a.updateSettings(func(s *settings.Settings) {
		s.Features.Translate.Hotkeys = []settings.Hotkey{{Keys: "Fn", Trigger: settings.TriggerTap}}
	})
	if !has(tt, "Transcribe has the same keys and trigger: only one of them works.") || !has(tt, "Translate has the same keys and trigger: only one of them works.") {
		t.Errorf("conflict: %q", tt.Texts())
	}
	a.updateSettings(func(s *settings.Settings) { s.Features.Rewrite.Hotkeys = nil })
	if !has(tt, "None") {
		t.Error("a feature without shortcut")
	}
	click(t, tt, "Paste the last result ⌃⌘V")
	if a.settingsOpen || a.shownPath() != "/transcribe" {
		t.Fatalf("row click: %v %s", a.settingsOpen, a.shownPath())
	}
	a.navigate("/settings/shortcuts")
	click(t, tt, "Translate fn")
	if a.shownPath() != "/translate" {
		t.Fatalf("Translate row: %s", a.shownPath())
	}
}

func TestOtherLanguages(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/settings/general")
	click(t, tt, "Add a language")
	if err := tt.ChooseMenuItem(langLabel("ja")); err != nil {
		t.Fatal(err)
	}
	frames(tt)
	if err := tt.Click("Add a language"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(tt.Menu(), langLabel("en")) || slices.Contains(tt.Menu(), langLabel("ja")) {
		t.Errorf("the menu offers the main language or a chosen one: %q", tt.Menu())
	}
	if err := tt.ChooseMenuItem(langLabel("zh")); err != nil {
		t.Fatal(err)
	}
	frames(tt)
	if got := a.settings.Get().General.OtherLanguages; !slices.Equal(got, []string{"ja", "zh"}) {
		t.Fatalf("added: %q", got)
	}
	click(t, tt, "Remove "+langLabel("ja"))
	if got := a.settings.Get().General.OtherLanguages; !slices.Equal(got, []string{"zh"}) {
		t.Fatalf("removed: %q", got)
	}
	// Choosing it as the main language takes it off the others.
	a.setMainLanguage("zh") // the select's choice
	if got := a.settings.Get().General.MainLanguage; got != "zh" {
		t.Fatalf("main language %q", got)
	}
	if got := a.settings.Get().General.OtherLanguages; len(got) != 0 {
		t.Errorf("main language still among the others: %q", got)
	}
	if got := withoutLanguage([]string{"zh-Hans", "en", "x-unknown"}, "zh"); !slices.Equal(got, []string{"en", "x-unknown"}) {
		t.Errorf("withoutLanguage: %q", got)
	}
	if got := withoutLanguage([]string{"x-unknown", "en"}, "y-unknown"); !slices.Equal(got, []string{"x-unknown", "en"}) {
		t.Errorf("withoutLanguage of an unknown code: %q", got)
	}
}

func TestAutoRail(t *testing.T) {
	a, _, tt := newTestApp(t)
	frames(tt)
	if a.railMode() {
		t.Fatal("rail at the default size")
	}
	tt.SetSize(windowMinWidth, windowMinHeight)
	frames(tt)
	if !a.railMode() || !has(tt, "Show the sidebar") {
		t.Fatal("no rail in a narrow window")
	}
	if a.prefs.Appearance.Rail || a.prefs.Appearance.RailChosen {
		t.Error("the automatic rail was saved")
	}
	// The user's choice wins, at any width.
	click(t, tt, "Show the sidebar")
	if a.railMode() || !a.prefs.Appearance.RailChosen {
		t.Fatalf("chose the sidebar: rail %v %+v", a.railMode(), a.prefs.Appearance)
	}
	tt.SetSize(windowWidth, windowHeight)
	frames(tt)
	click(t, tt, "Show icons only")
	if !a.railMode() {
		t.Fatal("chose the rail")
	}
	// Settings → Appearance: back to automatic.
	a.navigate("/settings/appearance")
	click(t, tt, "Automatic")
	if ap := a.settings.Get().Appearance; ap.Rail || ap.RailChosen || a.railMode() {
		t.Fatalf("automatic: %+v", ap)
	}
	click(t, tt, "Icons only")
	if ap := a.settings.Get().Appearance; !ap.Rail || !ap.RailChosen {
		t.Fatalf("icons only: %+v", ap)
	}
	click(t, tt, "Names and icons")
	if ap := a.settings.Get().Appearance; ap.Rail || !ap.RailChosen {
		t.Fatalf("names and icons: %+v", ap)
	}
	// An older document's rail stays a rail.
	if !(&app{shellState: shellState{prefs: settings.Settings{Appearance: settings.Appearance{Rail: true}}}}).railMode() {
		t.Error("older rail")
	}
}

func TestStartHidden(t *testing.T) {
	on := settings.General{MenuBar: true, CloseToTray: true}
	if startHidden(false, on) || !startHidden(true, on) {
		t.Error("menu bar and close to tray")
	}
	if startHidden(true, settings.General{MenuBar: false, CloseToTray: true}) || startHidden(true, settings.General{MenuBar: true}) {
		t.Error("a window that would not come back")
	}
}

func TestTrayIcons(t *testing.T) {
	if bytes.Equal(trayIcon, trayLiveIcon) {
		t.Fatal("the recording icon is the same")
	}
	for _, b := range [][]byte{trayIcon, trayLiveIcon} {
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if r := img.Bounds(); r.Dx() != 36 || r.Dy() != 36 {
			t.Errorf("size %v", r)
		}
	}
}

func TestTrayRecent(t *testing.T) {
	es := []store.Entry{
		{Status: store.StatusDone, Result: "newest"},
		{Status: store.StatusFailed, Result: "failed"},
		{Status: store.StatusDone, Result: "  "},
		{Status: store.StatusDone, Result: "second"},
		{Status: store.StatusDone, Result: "third"},
		{Status: store.StatusDone, Result: "fourth"},
	}
	if got := recentResults(es, 3); !slices.Equal(got, []string{"newest", "second", "third"}) {
		t.Fatalf("recent %q", got)
	}
	long := "一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十一二三"
	if got := trayLabel(long); len([]rune(got)) != trayRecentChars || got[len(got)-len("…"):] != "…" {
		t.Errorf("long label %q", got)
	}
	if got := trayLabel("two\nlines  here"); got != "two lines here" {
		t.Errorf("label %q", got)
	}
	// The items: the recent results, a click copies one.
	a, _, _ := newTestApp(t)
	var copied []string
	a.copyText = func(s string) error { copied = append(copied, s); return nil }
	a.recent = []string{"hello there"}
	found := false
	for _, it := range a.trayItems() {
		if it.Label == "hello there" {
			found = true
			it.Click(it, nil)
		}
	}
	if !found || !slices.Equal(copied, []string{"hello there"}) {
		t.Errorf("recent item: found %v, copied %q", found, copied)
	}
}
