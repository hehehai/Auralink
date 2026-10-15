package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/settings"
)

// TestSnapshots renders each page, the settings sections and the shell's
// overlays to PNG with MyGo's renderer, for looking at the layout without
// a Mac screen, light and dark, in English (<name>-light.png) and Chinese
// (<name>-light-zh-Hans.png), at 2×. Set AURALINK_SNAPSHOTS to a
// directory:
//
//	AURALINK_SNAPSHOTS=/tmp/shots go test -run TestSnapshots .
//
// AURALINK_SNAPSHOTS_SIZE=720x480 renders at the window's smallest size
// (snapshotSize).
func TestSnapshots(t *testing.T) {
	out := os.Getenv("AURALINK_SNAPSHOTS")
	if out == "" {
		t.Skip("set AURALINK_SNAPSHOTS to a directory")
	}
	os.MkdirAll(out, 0o755)
	w, h := snapshotSize()
	for _, run := range []struct {
		lang string
		dark bool
	}{{"en", false}, {"en", true}, {"zh-Hans", false}, {"zh-Hans", true}} {
		a, fs, tt := newTestApp(t)
		tt.SetSize(w, h)
		if run.lang != "en" {
			a.updateSettings(func(s *settings.Settings) { s.General.Language = run.lang })
		}
		tt.SetDark(run.dark)
		tt.SetScale(2)
		a.version = "0.1.0"
		seedHistory(t, a)      // history_page_test.go
		seedWriteHistory(t, a) // history_page_test.go
		suffix := "light"
		if run.dark {
			suffix = "dark"
		}
		if run.lang != "en" {
			suffix += "-" + run.lang
		}
		save := func(name string) {
			t.Helper()
			a.toasts = nil
			frames(tt)
			f, err := os.Create(filepath.Join(out, name+"-"+suffix+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		// Every page of the sidebar.
		for _, p := range pages {
			a.navigate(p.path)
			name := strings.TrimPrefix(p.path, "/")
			if p.path == "/logs" {
				at := time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local)
				for i, l := range []core.LogEntry{
					{Source: "app", Message: "Auralink 0.1.0 started"},
					{Source: helper.LogSource, Message: "fake helper ready"},
					{Source: helper.LogSource, Level: core.SeverityWarning, Message: "AirPods Pro gave no audio within 1.2 s: recording from the default input"},
					{Source: "app", Level: core.SeverityError, Message: "settings: could not save: disk full"},
				} {
					l.Time = at.Add(time.Duration(i) * time.Second)
					if l.Level == "" {
						l.Level = core.SeverityInfo
					}
					a.logs.Append(l)
				}
			}
			save("page-" + name)
		}
		snapshotHistoryDetail(a, save)   // history_page_test.go
		transcribeShots(a, tt, save)     // the Try area's states (transcribe_page_test.go)
		appEnhanceShots(a, fs, tt, save) // appenhance_page_test.go
		dictShots(t, a, save)            // dictionary_page_test.go
		// The settings dialog, section by section.
		a.updateSettings(func(s *settings.Settings) {
			s.General.OtherLanguages = []string{"zh", "en", "ja"} // the main language's is not shown
			s.Audio.Devices = []string{"AirPodsPro:input", "USB-Mic-1234"}
			s.Audio.DeviceNames = map[string]string{"USB-Mic-1234": "Blue Yeti"}
		})
		for _, s := range settingsSections() {
			a.navigate("/settings/" + s.id)
			if s.id == "network" {
				a.proxyInfo = i18n.T("Requests go through %s.", "http://127.0.0.1:7897")
			}
			if s.id == "about" {
				a.sys = &sysInfo{OS: "macOS 26.7", Chip: "Apple M2", Memory: 16 << 30, Cores: 8, Arch: "arm64"}
			}
			save("settings-" + s.id)
			if s.id == "overlay" { // its preview, under the controls
				w, h := snapshotSize()
				tt.Scroll(float32(w)*0.7, float32(h)*0.6, 0, 600)
				save("settings-overlay-preview")
			}
		}
		a.settingsOpen = false

		// The shell's overlays.
		a.navigate("/home")
		a.openPalette()
		save("palette")
		a.paletteOpen = false
		a.sheetOpen = true
		save("shortcuts")
		a.sheetOpen = false
		a.notes.Post(core.Notification{Severity: core.SeverityWarning, Title: i18n.T("Helper out of date"), Link: "/settings/about"})
		a.helperOpen = true
		save("helper")
		a.helperOpen = false
		a.noticesOpen = true
		a.logsPanel = true
		save("notifications")
		a.noticesOpen, a.logsPanel = false, false
		fs.set(helper.Status{State: helper.Failed, Reason: helper.FailMissing, Err: "auralink-helper was not found"})
		a.navigate("/settings/permissions")
		save("permissions-no-helper")
		a.settingsOpen = false
		a.setRail(true)
		a.navigate("/home")
		save("rail")
		a.setRail(false)

		// The recording overlay, state by state (overlay_test.go).
		overlayShots(t, a, out, suffix)

		// Last, as they set services up: the setup guide, step by step
		// (onboarding_test.go), then Home ready (home_page_test.go).
		onboardingShots(a, tt, save)
		homeShots(a, tt, save)
	}
}

// snapshotSize is the window's size for snapshots: its default size, or
// its smallest with AURALINK_SNAPSHOTS_SIZE=720x480 (or "min").
func snapshotSize() (w, h int) {
	switch os.Getenv("AURALINK_SNAPSHOTS_SIZE") {
	case "min", fmt.Sprintf("%dx%d", windowMinWidth, windowMinHeight):
		return windowMinWidth, windowMinHeight
	}
	return windowWidth, windowHeight
}
