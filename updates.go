package main

// Updates: Auralink's own update UI on actnowkit's updates package (MyGo's
// updater plugin and its window until 0.1.0-beta.2; the ActNow project's
// licensing phase replaced them). MyGo only provides the updater's API:
// mygo.Updater.Check reads Auralink's feed on actnow.dev and
// Update.Install downloads the signed archive (a delta when there is one),
// checks it and swaps the bundle (putting the old one back if that fails).
// actnowkit's updates.Manager runs the state machine over it — idle →
// checking → available → downloading → ready (restart to finish) or
// failed — and reads each version's notes in Auralink's language from
// actnow.dev's changelog API (English where a version has none).
//
// What the user sees (updates_view.go): a check that finds a version shows
// "Update available" at the foot of the sidebar (a dot on the rail's
// download button), nothing pops up; the update panel has the version, its
// notes, Download and Install, the progress and Restart to Finish the
// Update; Settings → About → Updates: check now, check automatically,
// beta updates, install without asking, the changelog's history. "Check
// for Updates…" (the app menu, the menu bar icon's menu, the palette)
// opens the panel with the answer.
//
// Release builds only: `updates` is put in mygo.json by the release
// workflow (docs/release.md), so other builds cannot update themselves
// (the Manager is Disabled).
//
// The updater replaces Auralink.app while the old version runs. Until it
// restarts, the helper on disk is the new version's: the supervisor does
// not start it (helper.FailUpdated), and a card asks to restart Auralink
// (appUpdated), which a timer, a check and the helper's failure look for
// (checkBundle, appbundle.Updated).
//
// Beta updates (Settings → About): betas are in the feed's beta channel
// (https://actnow.dev/updates/auralink/beta, whose manifest is the newer
// of the newest beta and release), which MyGo reads only while
// mygo.Updater.SetPrereleases is on (patch 0006); the switch is
// Updates.Beta in Auralink's settings, on by default while the running
// version is itself a beta (settings.Updates.BetaOn), and applied when the
// app starts and when it changes (applyBeta). The changelog then lists
// betas too.

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/settings"
	akup "github.com/hehehai/actnowkit/updates"
)

// updateCheck is the update check of -tags updatecheck builds
// (update_check.go, scripts/mac-update-e2e.sh); nil in others.
var updateCheck func(report string)

// argAfter is the argument after flag on the command line ("": none).
func argAfter(flag string) string {
	for i := 1; i+1 < len(os.Args); i++ {
		if os.Args[i] == flag {
			return os.Args[i+1]
		}
	}
	return ""
}

// updateSlug is Auralink on actnow.dev (feed, changelog API).
const updateSlug = "auralink"

// updateInterval is how often the automatic check runs.
const updateInterval = 24 * time.Hour

// updateManager is what Auralink uses of actnowkit's *updates.Manager.
type updateManager interface {
	State() akup.State
	Subscribe(fn func(akup.State)) (unsubscribe func())
	Check(ctx context.Context) akup.State
	Install(ctx context.Context, acceptPaid bool) akup.State
	Restart() error
	Dismiss()
	History(ctx context.Context) (*akup.History, error)
	AutoCheck(ctx context.Context, interval time.Duration, enabled func() bool)
}

// updateState is the updater as the UI sees it.
type updateState struct {
	// updLive: the real updater (main): connectUpdates makes the Manager
	// over mygo.Updater. Tests set upd instead.
	updLive bool
	upd     updateManager // nil: no updater (the overlay check)
	// setPrereleases chooses the feed's channel
	// (mygo.Updater.SetPrereleases; a recorder in tests, nil: none).
	setPrereleases func(on bool)
	updConnected   bool

	// updSt is the manager's state, as last posted.
	updSt akup.State
	// updPanel shows the update panel; updUserCheck: the running check was
	// asked for (its answer shows in the panel, or as a toast).
	updPanel     bool
	updUserCheck bool
	// updAutoTried is the version an automatic install was started for
	// (Install updates without asking), so a failure is not retried in a
	// loop.
	updAutoTried string

	// The changelog's history (Settings → About).
	clogOpen    bool
	clog        *akup.History
	clogErr     string
	clogLoading bool
	// updMd: the notes' Markdown views (their parse is kept between frames).
	updMd markdown.Options

	updStop   context.CancelFunc
	updAutoOn atomic.Bool // "check automatically", read by the manager's loop

	// bundleStale reports that the app on disk is another version than
	// the one running (appbundle.Updated); nil: never (tests set it).
	bundleStale func() bool
	// bundleUpdated is set once it did: the card asks to restart.
	bundleUpdated bool
}

// The update's card: its ID, and the link of its action, which restarts
// Auralink (cardAction).
const (
	problemUpdate = "problem/update"
	linkRelaunch  = "action:relaunch"
)

// bundleWatch is how often the running app looks for an update installed
// in the background.
const bundleWatch = time.Minute

// connectUpdates makes the update manager (the real one in main) and
// follows its state; main calls it with updLive and bundleStale set.
func (a *app) connectUpdates() {
	// A card kept from before a restart: the restart finished the update.
	a.setProblem(problemUpdate, "", nil)
	if a.updLive && a.upd == nil {
		a.setPrereleases = mygo.Updater.SetPrereleases
		version := mygo.App.Version()
		a.upd = akup.New(akup.Config{
			Updater: akup.Funcs{
				EnabledFunc: mygo.Updater.Enabled,
				CheckFunc: func(ctx context.Context) (*akup.Release, error) {
					up, err := mygo.Updater.Check(ctx)
					if err != nil || up == nil {
						return nil, err
					}
					return &akup.Release{Version: up.Version, Notes: up.Notes, Date: up.Date, Install: up.Install}, nil
				},
			},
			Current:  version,
			History:  &akup.HistoryClient{App: updateSlug},
			Language: i18n.Locale,
			Beta:     func() bool { return a.prefs.Updates.BetaOn(version) },
			Restart:  func() { a.post(a.relaunch) },
			Log:      func(s string) { log.Printf("updates: %s", s) },
		})
		a.keepUpdaterChoices()
	}
	if a.upd == nil || a.updConnected {
		return
	}
	a.updConnected = true
	a.updSt = a.upd.State()
	a.upd.Subscribe(func(st akup.State) { a.post(func() { a.setUpdateState(st) }) })
}

// startUpdates runs the automatic checks (once a day while "check
// automatically" is on, the first a little after launch) until quit; it
// runs once the window is open.
func (a *app) startUpdates() {
	if !a.updEnabled() || a.updStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.updStop = cancel
	a.updAutoOn.Store(a.prefs.Updates.AutoCheckOn())
	mgr := a.upd
	go func() {
		select {
		case <-time.After(10 * time.Second):
			mgr.AutoCheck(ctx, updateInterval, a.updAutoOn.Load)
		case <-ctx.Done():
		}
	}()
}

// stopUpdates ends the automatic checks (quit).
func (a *app) stopUpdates() {
	if a.updStop != nil {
		a.updStop()
		a.updStop = nil
	}
}

// keepUpdaterChoices carries "check automatically" and "install without
// asking" over from MyGo's updater plugin (its updater.json, up to
// 0.1.0-beta.2) the first time.
func (a *app) keepUpdaterChoices() {
	if a.prefs.Updates.AutoCheck != nil || a.dataHome == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(a.dataHome, "updater.json"))
	if err != nil {
		return
	}
	var old struct {
		AutomaticChecks    *bool `json:"automaticChecks"`
		AutomaticDownloads bool  `json:"automaticDownloads"`
	}
	if json.Unmarshal(data, &old) != nil || old.AutomaticChecks == nil {
		return
	}
	on, install := *old.AutomaticChecks, old.AutomaticDownloads
	a.updateSettings(func(s *settings.Settings) {
		s.Updates.AutoCheck = &on
		s.Updates.AutoInstall = install
	})
}

// updEnabled says whether this build can update itself.
func (a *app) updEnabled() bool { return a.upd != nil && a.updSt.Phase != akup.Disabled }

// setUpdateState takes the manager's new state.
func (a *app) setUpdateState(st akup.State) {
	prev := a.updSt
	a.updSt = st
	if !st.CheckedAt.IsZero() && !st.CheckedAt.Equal(prev.CheckedAt) && st.Phase != akup.Failed {
		at := st.CheckedAt
		a.updateSettings(func(s *settings.Settings) { s.Updates.LastCheck = at })
		a.checkBundle() // a check may have been followed by an install
	}
	switch st.Phase {
	case akup.Available:
		if a.updUserCheck {
			a.updUserCheck, a.updPanel = false, true
			return
		}
		// Install updates without asking: a version the daily check found
		// downloads at once; the restart is the user's (the card).
		if a.prefs.Updates.AutoInstall && st.Release != nil && a.updAutoTried != st.Release.Version {
			a.updAutoTried = st.Release.Version
			log.Printf("updates: installing %s without asking (Settings → About)", st.Release.Version)
			a.installUpdate()
		}
	case akup.UpToDate:
		if a.updUserCheck {
			a.updUserCheck = false
			if !a.updPanel {
				a.toast(i18n.T("Auralink is up to date (%s)", st.Current))
			}
		}
	case akup.Failed:
		if a.updUserCheck && st.Step == "check" && !a.updPanel {
			a.toast(i18n.T("Cannot check for updates: %s", updateErrText(st.Err)))
		}
		a.updUserCheck = false
	case akup.Ready:
		// Installed: the panel asks for the restart while it shows, the
		// card otherwise (an install without asking).
		if !a.updPanel {
			a.appUpdated()
		}
		a.bundleUpdated = true
	}
}

// checkForUpdates checks now, as the user asked (the menus, the palette,
// Settings → About): the panel opens with the answer.
func (a *app) checkForUpdates() {
	if a.upd == nil {
		return
	}
	switch a.updSt.Phase {
	case akup.Available, akup.Downloading, akup.Ready, akup.Disabled:
		a.updPanel = true
		return
	case akup.Checking:
		a.updUserCheck, a.updPanel = true, true
		return
	}
	a.updUserCheck, a.updPanel = true, true
	mgr := a.upd
	a.async(func() { mgr.Check(context.Background()) })
}

// installUpdate downloads and installs the available update.
func (a *app) installUpdate() {
	if a.upd == nil || a.updSt.Phase != akup.Available {
		return
	}
	mgr := a.upd
	a.async(func() { mgr.Install(context.Background(), false) })
}

// restartForUpdate relaunches into the installed update.
func (a *app) restartForUpdate() {
	if a.upd != nil && a.updSt.Phase == akup.Ready {
		if err := a.upd.Restart(); err == nil {
			return
		}
	}
	a.relaunch()
}

// updateMenuItems is Check for Updates… for a menu (none without the
// updater); act runs a function on the main thread.
func (a *app) updateMenuItems(act func(func()) func(*mygo.MenuItem, *mygo.Window)) []*mygo.MenuItem {
	if a.upd == nil {
		return nil
	}
	return []*mygo.MenuItem{{Label: i18n.T("Check for Updates…"), Click: act(a.checkForUpdates)}}
}

// setAutoCheck changes whether updates are checked once a day; it is saved
// in the settings.
func (a *app) setAutoCheck(on bool) {
	a.updAutoOn.Store(on)
	a.updateSettings(func(s *settings.Settings) { s.Updates.AutoCheck = &on })
}

// setAutoInstall changes whether a version the daily check found installs
// without asking.
func (a *app) setAutoInstall(on bool) {
	a.updateSettings(func(s *settings.Settings) { s.Updates.AutoInstall = on })
}

// betaOn reports whether updates include betas: the user's choice, else
// whether this version is a beta.
func (a *app) betaOn() bool { return a.prefs.Updates.BetaOn(a.version) }

// applyBeta tells the updater whether to offer betas; it runs once the
// version is known (main, after openWindow) and when the choice changes.
func (a *app) applyBeta() {
	if a.setPrereleases != nil {
		a.setPrereleases(a.betaOn())
	}
}

// setBeta is the user's choice of Beta updates, which lasts.
func (a *app) setBeta(on bool) {
	a.updateSettings(func(s *settings.Settings) { s.Updates.Beta = &on })
	a.applyBeta()
}

// openHistory opens the changelog's history and reads it from actnow.dev.
func (a *app) openHistory() {
	a.clogOpen = true
	if a.upd == nil || a.clogLoading {
		return
	}
	a.clogLoading, a.clogErr = true, ""
	mgr := a.upd
	a.async(func() {
		h, err := mgr.History(context.Background())
		a.post(func() {
			a.clogLoading = false
			if err != nil {
				a.clogErr = updateErrText(err)
				return
			}
			a.clog = h
		})
	})
}

// updateErrText is an error for a sentence ("" for none).
func updateErrText(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	for _, p := range []string{"mygo: ", "updates: "} {
		s = strings.TrimPrefix(s, p)
	}
	return s
}

// watchBundle looks for an update installed while Auralink runs, every
// bundleWatch; it runs once the window is open.
func (a *app) watchBundle() {
	if a.bundleStale == nil {
		return
	}
	stale := a.bundleStale
	go func() {
		t := time.NewTicker(bundleWatch)
		defer t.Stop()
		for range t.C {
			if stale() {
				a.post(a.appUpdated)
				return
			}
		}
	}()
}

// checkBundle looks for an installed update once, in the background.
func (a *app) checkBundle() {
	if a.bundleStale == nil || a.bundleUpdated {
		return
	}
	stale := a.bundleStale
	a.async(func() {
		if stale() {
			a.post(a.appUpdated)
		}
	})
}

// appUpdated shows that an update is installed and Auralink is to
// restart: a card (with Restart Auralink), Settings → About and Home.
func (a *app) appUpdated() {
	a.bundleUpdated = true
	a.setProblem(problemUpdate, "installed", &core.Notification{Severity: core.SeverityWarning,
		Title: i18n.T("Update installed"), Body: i18n.T("Restart Auralink to finish updating."), Link: linkRelaunch})
}

// relaunch restarts Auralink into the version on disk: the quit stops the
// helper (OnQuit), the new version starts its own.
func (a *app) relaunch() { appRelaunch() }
