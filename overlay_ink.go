package main

// The overlay's ink check: what a picture of the overlay's window shows,
// measured from its pixels, against the face's layout (10-15: the pill's
// icon side looked wider than its waveform's, which no frame check saw).
// The same check reads the Tester's pictures on Linux (overlay_test.go)
// and the Mac's captures of the real window (overlay_check.go).
//
// The face is where the window is opaque (the shadow is faint); its ink
// is what is bright on it: the icon, the bars, the text (the hairline
// and the dark fill are not). Distances are in DIPs.

import (
	"fmt"
	"image"
	"math"
)

// ovInkWant is what the face should show.
type ovInkWant struct {
	w, h int // the face's size
	// row: the pill's row is there (the icon, then the waveform or the
	// loader); even: its ink is as far from the face's left edge as from
	// its right (the waveform's: not the loader's, nor a notice's).
	row, even bool
	// text: a line of text under the row; scrolled: it was wider than
	// the line, so it ends at the right padding and is cut at the left.
	text, scrolled bool
}

// ovInk is what the picture shows, in DIPs from the face's top left.
type ovInk struct {
	faceW, faceH float64
	// The row's ink: the icon's (the first stroke) and the rest's, the
	// left and right edges and the middle of each, top to bottom.
	iconL, iconMid float64
	restR, restMid float64
	// The text line's ink, left and right edges (0, 0: none).
	textL, textR float64
}

func (k ovInk) String() string {
	s := fmt.Sprintf("face %.1f×%.1f; row: left %.1f, right %.1f, icon middle %.1f, waveform middle %.1f",
		k.faceW, k.faceH, k.iconL, k.faceW-k.restR, k.iconMid, k.restMid)
	if k.textR > 0 {
		s += fmt.Sprintf("; text: left %.1f, right %.1f, %.1f wide", k.textL, k.faceW-k.textR, k.textR-k.textL)
	}
	return s
}

// Tolerances: of the layout's own sizes, and of the ink's: a glyph's ink
// is a little narrower than its advance, an edge's antialiasing.
const (
	ovInkTol     = 1.0
	ovInkSizeTol = 0.6
	ovInkTextTol = 2.0
)

// measureOverlayInk measures img, a picture of the overlay's window at
// scale pixels a DIP, the face's row padY DIPs from its top.
func measureOverlayInk(img image.Image, scale float64, padY float64, row, text bool) (ovInk, error) {
	b := img.Bounds()
	alpha := func(x, y int) uint32 { _, _, _, a := img.At(x, y).RGBA(); return a >> 8 }
	bright := func(x, y int) bool {
		r, g, bl, a := img.At(x, y).RGBA()
		if a>>8 < 128 {
			return false
		}
		// Unpremultiplied, the least channel: white ink, not the fill.
		m := min(r, g, bl) * 255 / max(a, 1)
		return m > 150
	}
	x0, x1, y0, y1 := b.Max.X, b.Min.X-1, b.Max.Y, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if alpha(x, y) > 128 {
				x0, x1, y0, y1 = min(x0, x), max(x1, x), min(y0, y), max(y1, y)
			}
		}
	}
	if x1 < x0 {
		return ovInk{}, fmt.Errorf("no face")
	}
	var k ovInk
	k.faceW, k.faceH = float64(x1+1-x0)/scale, float64(y1+1-y0)/scale
	if !row {
		return k, nil
	}
	inset := int(math.Ceil(2 * scale)) // past the hairline
	// The row: its band, the columns that have ink in it, split at the
	// first gap wider than 6 DIPs (the icon's slot, the gap, the bars'
	// air: 12 or more; a glyph's own gaps are narrower).
	rowTop, rowBottom := y0+int(math.Round(padY*scale)), y0+int(math.Round((padY+ovRowH)*scale))
	if !text {
		rowBottom = y1 + 1 - inset
	}
	var cols []int
	for x := x0 + inset; x <= x1-inset; x++ {
		for y := max(rowTop, y0+inset); y < rowBottom; y++ {
			if bright(x, y) {
				cols = append(cols, x)
				break
			}
		}
	}
	if len(cols) == 0 {
		return k, fmt.Errorf("no ink in the row")
	}
	split := len(cols)
	for i := 1; i < len(cols); i++ {
		if float64(cols[i]-cols[i-1]) > 6*scale {
			split = i
			break
		}
	}
	if split == len(cols) {
		return k, fmt.Errorf("the row's ink is one stroke")
	}
	mid := func(xa, xb int) float64 {
		ya, yb := math.MaxInt, -1
		for x := xa; x <= xb; x++ {
			for y := max(rowTop, y0+inset); y < rowBottom; y++ {
				if bright(x, y) {
					ya, yb = min(ya, y), max(yb, y)
				}
			}
		}
		return (float64(ya+yb+1)/2 - float64(y0)) / scale
	}
	k.iconL = float64(cols[0]-x0) / scale
	k.iconMid = mid(cols[0], cols[split-1])
	k.restR = float64(cols[len(cols)-1]+1-x0) / scale
	k.restMid = mid(cols[split], cols[len(cols)-1])
	if text {
		lineTop := rowBottom + int(math.Round(2*scale))
		tl, tr := math.MaxInt, -1
		for y := lineTop; y <= y1-inset; y++ {
			for x := x0 + inset; x <= x1-inset; x++ {
				if bright(x, y) {
					tl, tr = min(tl, x), max(tr, x)
				}
			}
		}
		if tr < 0 {
			return k, fmt.Errorf("no ink in the text's line")
		}
		k.textL, k.textR = float64(tl-x0)/scale, float64(tr+1-x0)/scale
	}
	return k, nil
}

// checkOverlayInk measures img and says what is wrong with it: the
// face's size, the row's sides, its middle, the text's sides.
func checkOverlayInk(img image.Image, scale float64, want ovInkWant) (ovInk, []string) {
	padY := float64(ovPillPadY)
	if want.text {
		padY = ovLivePadY
	}
	k, err := measureOverlayInk(img, scale, padY, want.row, want.text)
	if err != nil {
		return k, []string{err.Error()}
	}
	var bad []string
	failf := func(format string, args ...any) { bad = append(bad, fmt.Sprintf(format, args...)) }
	if math.Abs(k.faceW-float64(want.w)) > ovInkSizeTol || math.Abs(k.faceH-float64(want.h)) > ovInkSizeTol {
		failf("the face is %.1f×%.1f, not %d×%d", k.faceW, k.faceH, want.w, want.h)
	}
	if !want.row {
		return k, bad
	}
	left, right := k.iconL, k.faceW-k.restR
	if want.even && math.Abs(left-right) > ovInkTol {
		failf("the row's ink is %.1f from the left edge, %.1f from the right", left, right)
	}
	if d := k.iconMid - k.restMid; math.Abs(d) > ovInkTol {
		failf("the icon's middle is %.1f, the waveform's %.1f", k.iconMid, k.restMid)
	}
	if rowMid := padY + ovRowH/2; math.Abs(k.restMid-rowMid) > ovInkTol {
		failf("the row's middle is %.1f, not %.1f", k.restMid, rowMid)
	}
	if !want.text {
		return k, bad
	}
	tl, tr := k.textL, k.faceW-k.textR
	switch {
	case want.scrolled:
		// The newest words end at the right padding; the oldest go under
		// the left one, fading.
		if math.Abs(tr-ovLivePadX) > ovInkTextTol || tl < ovLivePadX-ovInkTol {
			failf("the scrolled text is %.1f from the left edge, %.1f from the right (%d)", tl, tr, ovLivePadX)
		}
		if math.Abs(k.faceW-ovLiveMaxW) > ovInkSizeTol {
			failf("the face with long text is %.1f wide, not %d", k.faceW, ovLiveMaxW)
		}
	default:
		if math.Abs(tl-tr) > ovInkTol {
			failf("the text is %.1f from the left edge, %.1f from the right", tl, tr)
		}
		// As wide as the text and the padding (its ink a little narrower
		// than its advance), or else as the row and the padding (the
		// icon's ink a little inside its slot, the bars' in theirs).
		byText := k.faceW-(k.textR-k.textL+2*ovLivePadX) <= 2*ovInkTextTol
		byRow := left <= ovLivePadX+4 && right <= ovLivePadX+4
		if !byText && !byRow {
			failf("the face is %.1f wide, wider than its text (%.1f) and its row", k.faceW, k.textR-k.textL)
		}
		if tl < ovLivePadX-ovInkTol {
			failf("the text is cut: %.1f from the left edge", tl)
		}
	}
	return k, bad
}
