package main

// Settings → Overlay style (/settings/overlay, M6): where the recording
// overlay shows (the bottom or the top of the display, how far from its
// edge), how opaque its face is and how round its corners are
// (settings.Overlay), with a preview: the overlay's own pill, drawn as
// overlay.go draws it, and a small screen with the pill in its place.
// A change moves the overlay at once when it shows; it is saved once the
// sliders rest.

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// overlayForm is the section's controls.
type overlayForm struct {
	position                int // 0 bottom, 1 top
	opacity, radius, margin float64
	// saveAt is when the last change waits to be saved (zero: saved).
	saveAt time.Time
}

// Bounds of the sliders (settings.normalize keeps wider ones).
const (
	ovMaxRadius = 26 // the pill's half height: rounder changes nothing
	ovMaxMargin = 240
	ovSaveDelay = 400 * time.Millisecond
)

// overlayPositions are the values of Overlay.Position, by segment.
var overlayPositions = []string{"bottom", "top"}

// loadOverlayForm sets the controls from the settings, unless a change
// waits to be saved (the sliders move).
func (a *app) loadOverlayForm() {
	f := &a.settingsForm.ovStyle
	if !f.saveAt.IsZero() {
		return
	}
	o := a.prefs.Overlay
	f.position = 0
	if o.Position == "top" {
		f.position = 1
	}
	f.opacity, f.radius, f.margin = o.Opacity, o.Radius, o.Margin
}

// overlayEdited applies the controls: at once to the overlay and the
// preview, to the settings after ovSaveDelay without another change.
func (a *app) overlayEdited() {
	f := &a.settingsForm.ovStyle
	o := settings.Overlay{
		Position: overlayPositions[min(max(f.position, 0), 1)],
		Opacity:  math.Round(f.opacity*100) / 100,
		Radius:   math.Round(f.radius),
		Margin:   math.Round(f.margin),
	}
	a.prefs.Overlay = o
	a.overlayRestyle() // overlay.go
	if a.win == nil {
		a.updateSettings(func(s *settings.Settings) { s.Overlay = o })
		return
	}
	at := time.Now()
	f.saveAt = at
	time.AfterFunc(ovSaveDelay, func() {
		a.post(func() {
			if f.saveAt != at {
				return // a later change saves
			}
			f.saveAt = time.Time{}
			o := a.prefs.Overlay
			a.updateSettings(func(s *settings.Settings) { s.Overlay = o })
		})
	})
}

// overlaySettings is the Overlay style section of the settings dialog.
func (a *app) overlaySettings(c *ui.Context) {
	t := c.Theme()
	f := &a.settingsForm.ovStyle
	slider := func(key, label string, value *float64, lo, hi, step float64, text string) {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			if ui.Slider(c.Key(key), value, lo, hi).Step(step).Label(label).Width(px(t, 160)).Shrink(1).MinWidth(px(t, 100)).Changed() {
				a.overlayEdited()
			}
			ui.Text(c, text).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Font("monospace").Width(px(t, 40)).Shrink(0).SingleLine()
		})
	}
	settingRow(c, "ov-position", i18n.T("Position"), i18n.T("On the display under the pointer, centred."), func() {
		if ui.Segmented(c.Key("ov-position-seg"), &f.position, i18n.T("Bottom centre"), i18n.T("Top centre")).Label(i18n.T("Position")).Changed() {
			a.overlayEdited()
		}
	})
	settingRow(c, "ov-margin", i18n.T("Distance from the edge"), i18n.T("From the bottom or the top of the screen, above the Dock and below the menu bar."), func() {
		slider("ov-margin-slider", i18n.T("Distance from the edge"), &f.margin, 0, ovMaxMargin, 4, fmt.Sprintf("%.0f", f.margin))
	})
	settingRow(c, "ov-opacity", i18n.T("Opacity"), "", func() {
		slider("ov-opacity-slider", i18n.T("Opacity"), &f.opacity, 0.5, 1, 0.05, fmt.Sprintf("%.0f%%", f.opacity*100))
	})
	settingRow(c, "ov-radius", i18n.T("Corner radius"), i18n.T("The answer card takes the same corners."), func() {
		slider("ov-radius-slider", i18n.T("Corner radius"), &f.radius, 0, ovMaxRadius, 1, fmt.Sprintf("%.0f", f.radius))
	})

	sectionTitle(c, i18n.T("Preview"))
	fillCard(c.Key("ov-preview")).Children(func() {
		ui.Row(c).Wrap().Gap(px(t, sp6)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			overlayScreen(c, a.prefs.Overlay)
			a.overlayPreviewPill(c)
		})
	})
}

// previewSession is the recording the preview shows: Transcribe, seven
// seconds in, speaking.
func previewSession() session.Snapshot {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	levels := make([]float64, session.Bars)
	for i := range levels {
		levels[i] = 0.25 + 0.6*math.Abs(math.Sin(float64(i)*0.8))
	}
	return session.Snapshot{Kind: store.KindTranscribe, State: session.Recording, StartedAt: t0, HeardAt: t0, StoppedAt: t0.Add(7 * time.Second), Levels: levels}
}

// overlayPreviewPill is the recording pill as the overlay draws it, at
// its size, in its dark theme.
func (a *app) overlayPreviewPill(c *ui.Context) {
	t := c.Theme()
	c.SetTheme(overlayTheme())
	ui.Column(c.Key("ov-preview-pill")).Size(ovPillW, ovPillH).Shrink(0).Label(i18n.T("Preview of the overlay")).Children(func() {
		overlayFaceFrame(c, "ov-preview-face", a.prefs.Overlay, ovRecording, ovPillH, false).Children(func() {
			a.overlayPill(c, previewSession(), ovRecording, false)
		})
	})
	c.SetTheme(t)
}

// Sizes of the preview's screen: a 1440 × 900 display, scaled.
const (
	ovScreenW  = 200
	ovScreenH  = 125
	ovDisplayW = 1440
	ovMenuBarH = 4 // the menu bar, drawn
)

// Colours of the preview's screen: a wallpaper of a mid grey-blue, on
// which the dark pill shows in either appearance.
var (
	ovWallpaper = ui.RGB(150, 160, 178)
	ovMenuBar   = ui.RGBA(255, 255, 255, 0.55)
)

// overlayScreen is a small screen with the pill where o puts it, at the
// scale of a 1440 DIPs wide display.
func overlayScreen(c *ui.Context, o settings.Overlay) {
	t := c.Theme()
	k := tok(t)
	scale := float32(ovScreenW) / ovDisplayW
	sw, sh := px(t, ovScreenW), px(t, ovScreenH)
	f := sw / ovScreenW // the theme's scale
	ui.Box(c.Key("ov-screen")).Size(sw, sh).Shrink(0).Radius(px(t, 6)).Background(ovWallpaper).Border(1, k.Line).Clip().
		Label(i18n.T("Where the overlay shows")).Children(func() {
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Height(px(t, ovMenuBarH)).Background(ovMenuBar)
		w, h := ovPillW*scale*f, ovPillH*scale*f
		m := float32(o.Margin) * scale * f
		pill := ui.Box(c).Absolute().Left((sw-w)/2).Size(w, h).Radius(min(float32(o.Radius), ovPillH/2) * scale * f).
			Background(ui.RGBA(22, 22, 26, 0.96*float32(min(max(o.Opacity, 0.5), 1))))
		if o.Position == "top" {
			pill.Top(px(t, ovMenuBarH) + m)
		} else {
			pill.Bottom(m)
		}
	})
}
