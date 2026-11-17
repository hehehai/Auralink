package main

// The feedback window (actnowkit's feedbackui) over the Manager
// feedback.go makes: Help → Send Feedback…, the palette and the menu bar
// open it; Settings → Feedback (feedback_view.go) lists this Mac's
// feedback; feedback_report.go puts "Report this problem…" beside the
// failures. The window draws the form, the outbox, the history and the
// replies; this file gives it Auralink's colours, appearance and
// diagnostics.

import (
	"encoding/json"
	"sync"

	"github.com/egoist/mygo"
	"github.com/hehehai/actnowkit/feedback"
	fbui "github.com/hehehai/actnowkit/feedbackui"

	"github.com/hehehai/Auralink/internal/i18n"
)

// feedbackPrivacyURL is actnow.dev's privacy policy (the form's last line).
const feedbackPrivacyURL = "https://actnow.dev/legal/privacy"

// openExternal opens a link in the browser; tests replace it.
var openExternal = func(u string) { go mygo.Shell.OpenExternal(u) }

// feedbackState is the feedback's state (main thread, but for the
// snapshot and the reports, which other goroutines read).
type feedbackState struct {
	fbMgr  *feedback.Manager
	fbWin  *fbui.Window
	fbStop func() // stops Resume (feedback.go)
	// fbOptions are the window's options before fbUse fills in
	// Auralink's (tests set Headless).
	fbOptions fbui.Options

	fbSnapMu sync.Mutex
	fbSnap   feedbackSnapshot

	fbRepMu   sync.Mutex
	fbReports map[string]*feedback.Report // a failure's key → its report (feedback_report.go)
}

// feedbackSnapshot is Auralink's part of the diagnostics, read on the
// main thread for the Manager's Collect, which runs off it.
type feedbackSnapshot struct {
	settings json.RawMessage
	extra    map[string]string
}

// fbUse connects the Manager and makes the window over it (nil: none;
// Settings → Feedback then offers the web form and GitHub only).
func (a *app) fbUse(m *feedback.Manager) {
	if a.fbWin != nil {
		a.fbWin.Stop()
		a.fbWin = nil
	}
	a.fbMgr = m
	a.fbRepMu.Lock()
	a.fbReports = nil
	a.fbRepMu.Unlock()
	if m == nil {
		return
	}
	o := a.fbOptions
	o.Manager, o.AppName, o.Site, o.PrivacyURL = m, "Auralink", "actnow.dev", feedbackPrivacyURL
	if o.Lang == nil {
		o.Lang = i18n.Locale
	}
	if o.Post == nil {
		o.Post = a.post
	}
	if o.Async == nil {
		o.Async = a.async
	}
	if o.OpenURL == nil {
		o.OpenURL = func(u string) { openExternal(u) }
	}
	if o.BeforeCollect == nil {
		o.BeforeCollect = a.fbTakeSnapshot
	}
	if o.StepName == nil {
		o.StepName = feedbackStepName
	}
	if o.Theme.Light == nil && o.Theme.Dark == nil && !o.Theme.FromMyGo {
		o.Theme.Light, o.Theme.Dark = feedbackPalette(false), feedbackPalette(true)
		o.Theme.Radius = radiusControl
	}
	if o.Theme.DarkMode == nil {
		o.Theme.DarkMode = a.fbDarkMode
	}
	a.fbWin = fbui.New(o)
}

// feedbackPalette is the window in Auralink's tokens (ui_tokens.go): the
// same greys and teal as the window's defaults, Auralink's red-orange
// for errors.
func feedbackPalette(dark bool) *fbui.Palette {
	k := makeTokens(dark)
	p := fbui.LightPalette()
	if dark {
		p = fbui.DarkPalette()
	}
	g := k.G
	p.Background, p.Raised, p.Track, p.Separator = g[0], g[1], g[2], g[3]
	p.Border, p.BorderStrong, p.Placeholder, p.TextMuted = g[4], g[5], g[6], g[7]
	p.TextSecondary, p.Text = g[8], g[9]
	p.Accent, p.Danger = k.Accent, k.Danger
	p.DangerBG = g[0].Mix(k.Danger, 0.1)
	p.PrimaryFill, p.PrimaryText = k.Ink, k.OnInk
	return p
}

// fbDarkMode is Settings → Appearance's theme (nil: the system's).
func (a *app) fbDarkMode() *bool {
	var dark bool
	switch a.prefs.Appearance.Theme {
	case "dark":
		dark = true
	case "light":
	default:
		return nil
	}
	return &dark
}

// sendFeedback opens the feedback window (Help → Send Feedback…, the
// palette, the menu bar); Settings → Feedback while there is no Manager.
func (a *app) sendFeedback() {
	if a.fbWin == nil {
		a.showMainWindow()
		a.navigate("/settings/feedback")
		return
	}
	a.fbWin.Open(feedback.Prefill{})
}

// fbUnread counts the feedback with replies not read (the dot on
// Settings → Feedback).
func (a *app) fbUnread() int {
	if a.fbWin == nil {
		return 0
	}
	return a.fbWin.Unread()
}

// fbTakeSnapshot reads the settings and the diagnostics' lines.
func (a *app) fbTakeSnapshot() {
	snap := feedbackSnapshot{settings: feedbackSettings(a.prefs), extra: feedbackExtra(a.diagnosticsText())}
	a.fbSnapMu.Lock()
	a.fbSnap = snap
	a.fbSnapMu.Unlock()
}

// fbSnapSettings and fbSnapExtra are the last snapshot's (Collect's
// Settings and Extra; any goroutine).
func (a *app) fbSnapSettings() json.RawMessage {
	a.fbSnapMu.Lock()
	defer a.fbSnapMu.Unlock()
	return a.fbSnap.settings
}

func (a *app) fbSnapExtra() map[string]string {
	a.fbSnapMu.Lock()
	defer a.fbSnapMu.Unlock()
	return a.fbSnap.extra
}

// fbWebURL is the site's feedback form (the app, its version and language).
func (a *app) fbWebURL() string {
	if a.fbMgr == nil {
		return feedbackSite + "/feedback?app=" + updateSlug
	}
	return a.fbMgr.WebURL()
}
