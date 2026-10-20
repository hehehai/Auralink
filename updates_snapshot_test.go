package main

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/settings"
)

// TestSnapshotsUpdates renders the in-app updater (updates_view.go): the
// sidebar's "Update available" and the rail's button, the update panel
// (available with actnow.dev's notes, downloading, installed, failed), the
// notes in English in a Chinese window, Settings → About → Updates and the
// changelog's history, in English and Chinese, light and dark:
//
//	AURALINK_SNAPSHOTS=/tmp/upd go test -run TestSnapshotsUpdates .
func TestSnapshotsUpdates(t *testing.T) {
	runSnapshots(t, "AURALINK_SNAPSHOTS", snapshotsUpdates)
}

func snapshotsUpdates(t *testing.T, out string) {
	for _, run := range []struct {
		lang string
		dark bool
	}{{"en", false}, {"en", true}, {"zh-Hans", false}, {"zh-Hans", true}} {
		suffix := "light"
		if run.dark {
			suffix = "dark"
		}
		if run.lang != "en" {
			suffix += "-" + run.lang
		}
		setup := func() (*app, *testUpdates, func(name string), *ui.Tester) {
			a, _, tt := newTestApp(t)
			if run.lang != "en" {
				a.updateSettings(func(s *settings.Settings) { s.General.Language = run.lang })
			}
			a.updateSettings(func(s *settings.Settings) { s.Setup.Done = true })
			tt.SetDark(run.dark)
			tt.SetScale(2)
			a.version = "0.1.0-beta.2"
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
			return a, updatesOf(a), save, tt
		}
		a, u, save, _ := setup()
		a.navigate("/home")
		u.feed.offer("0.1.0")
		a.upd.Check(context.Background())
		save("updates-sidebar")
		a.setRail(true)
		save("updates-rail")
		a.setRail(false)
		a.updPanel = true
		save("updates-panel")
		u.feed.during = func() { save("updates-downloading") }
		a.installUpdate()
		save("updates-ready")

		a, u, save, _ = setup()
		u.feed.offer("0.1.1")
		u.feed.installErr = errors.New("the archive's signature does not match")
		a.checkForUpdates()
		a.installUpdate()
		save("updates-failed")

		if run.lang == "zh-Hans" {
			a, u, save, _ = setup()
			u.feed.offer("0.1.0-beta.3")
			a.checkForUpdates()
			save("updates-panel-english")
		}

		a, _, save, tt := setup()
		a.updateSettings(func(s *settings.Settings) { s.Updates.LastCheck = time.Now().Add(-2 * time.Hour) })
		tt.SetSize(windowWidth, 900)
		a.navigate("/settings/about")
		see(t, tt, i18n.T("Check automatically"))
		save("updates-settings")
		a.openHistory()
		save("updates-history")
	}
}
