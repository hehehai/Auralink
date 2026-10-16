package main

// The overlay's motion (overlay.go): the recording's waveform, the working
// loader and the recording's clock.

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/session"
)

// Bars of the waveform (Voxt's): waveW wide in all, centred in the
// waveform's slot.
const (
	waveBarW    = 3.2
	waveBarGap  = 2.5
	waveBarR    = 1.5
	waveBarMinH = 3.2
	waveBarMaxH = 22
	waveW       = session.Bars*waveBarW + (session.Bars-1)*waveBarGap // 88.7
)

// waveform draws the input levels as 16 rounded bars, newest on the
// right, in the waveform's slot (ovWaveW, ovRowH), centred (Voxt's):
// smoothed (waveBars), and, where it is quiet, breathing a little about
// their least height while look moves. Dim until the first audio.
func waveform(c *ui.Context, levels []float64, heard bool, look pillLook) {
	bars := waveBars(levels)
	ui.Row(c.Key("ov-wave")).Width(ovWaveW).Height(ovRowH).Shrink(0).Center().Children(func() {
		ui.Row(c).Width(waveW).Height(ovRowH).Shrink(0).Gap(waveBarGap).AlignItems(ui.Center).Label(i18n.T("Input level")).Children(func() {
			for i, v := range bars {
				h := waveBarMinH + (waveBarMaxH-waveBarMinH)*float32(v)
				if look.motion {
					h += float32(waveIdle(i, v, look.phase))
				}
				// Voxt's fill: white, 98 % at the top to 80 % at the bottom.
				top, bottom := float32(0.98), float32(0.8)
				if !heard {
					top, bottom = 0.34, 0.28
				}
				bar := ui.Box(c).Width(waveBarW).Height(h).Radius(waveBarR).Gradient(ui.RGBA(255, 255, 255, top), ui.RGBA(255, 255, 255, bottom), 180).Shrink(0)
				if g := waveGlow(v); g > 0 && heard {
					bar.Shadow(0, 0, 3, 0, ui.RGBA(255, 255, 255, float32(g)))
				}
			}
		})
	})
}

// waveBars turns the last levels (session.Level, oldest first, about 20
// a second) into the bars' heights, 0–1: each level lifted so that quiet
// speech shows and room noise does not, then followed quickly as it rises
// and slowly as it falls (Voxt's waveform: rise 0.82, fall 0.24 a frame),
// so the bars swell with a word and ebb after it. Missing levels (the
// recording's first second) are 0.
func waveBars(levels []float64) [session.Bars]float64 {
	var out [session.Bars]float64
	pad := session.Bars - len(levels)
	if pad < 0 {
		levels, pad = levels[-pad:], 0
	}
	s := 0.0
	for i := range session.Bars {
		x := 0.0
		if i >= pad {
			x = waveLift(levels[i-pad])
		}
		if x >= s {
			s += (x - s) * 0.82
		} else {
			s += (x - s) * 0.24
		}
		out[i] = min(max(s, 0), 1)
	}
	return out
}

// waveLift maps a level (0 at −60 dBFS, 1 at 0 dBFS) to a bar: below
// −48 dBFS (a room) nothing, full at −12 dBFS, eased so that speech at
// −35 dBFS still stands up.
func waveLift(v float64) float64 {
	g := min(max((v-0.2)/0.6, 0), 1)
	return math.Pow(g, 0.8)
}

// waveIdle is a quiet bar's breath: up to 1.6 DIPs, a slow wave along
// the bars (1.6 s), gone as the bar rises.
func waveIdle(i int, v, phase float64) float64 {
	quiet := max(0, 1-v*8)
	return quiet * 1.6 * (0.5 + 0.5*math.Sin(2*math.Pi*phase/1.6+float64(i)*0.55))
}

// waveGlow is a loud bar's halo, as Voxt's: none when quiet.
func waveGlow(v float64) float64 {
	a := max(0, v-0.05)
	return min(0.24, 0.26*a)
}

// overlayLoader is the working pill's loader in the waveform's place,
// centred in its slot: five dots of 5, 4 apart, that swell and brighten
// in turn (Voxt's: 1.2 s, 0.12 s apart); still, all half bright, without
// motion.
func overlayLoader(c *ui.Context, label string, look pillLook) {
	ui.Row(c.Key("ov-loader")).Width(ovWaveW).Height(ovRowH).Shrink(0).Gap(4).AlignItems(ui.Center).Justify(ui.Center).
		Label(label).Children(func() {
		for i := range 5 {
			scale, alpha := 1.0, 0.7
			if look.motion {
				scale, alpha = loaderDot(look.phase - float64(i)*0.12)
			}
			d := float32(5 * scale)
			ui.Box(c).Size(d, d).Radius(d / 2).Background(ui.RGBA(255, 255, 255, float32(alpha))).Shrink(0)
		}
	})
}

// loaderDot is a loader dot's scale and opacity at t seconds: it swells
// and brightens over the first 30 % of a 1.2 s cycle, shrinks and fades
// over the next 30 %, and rests.
func loaderDot(t float64) (scale, alpha float64) {
	p := math.Mod(t, 1.2) / 1.2
	if p < 0 {
		p++
	}
	switch {
	case p < 0.3:
		u := p / 0.3
		return 0.6 + 0.7*u, 0.15 + 0.85*u
	case p < 0.6:
		u := (p - 0.3) / 0.3
		return 1.3 - 0.7*u, 1 - 0.85*u
	}
	return 0.6, 0.15
}

// clock shows a duration as m:ss.
func clock(d time.Duration) string {
	sec := int(d.Seconds())
	return itoa(sec/60) + ":" + string(rune('0'+sec%60/10)) + string(rune('0'+sec%10))
}
