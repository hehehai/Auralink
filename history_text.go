package main

// The words of a history entry (history_page.go, Home's recent results):
// its title, its line of details, its marks, the delivery, the stages'
// times and the sound.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/store"
)

// entryTitle is an entry's line in the list: the first line of its
// result, else of what was heard, else why it failed (failed true).
func entryTitle(e store.Entry) (title string, failed bool) {
	if s := firstLine(e.Result); s != "" {
		return s, false
	}
	if e.Status == store.StatusFailed && e.Error != "" {
		return firstLine(e.Error), true
	}
	if s := firstLine(e.RawText); s != "" {
		return s, false
	}
	if s := firstLine(e.SourceText); s != "" {
		return s, false
	}
	return i18n.T("No text"), true
}

// firstLine is the first line of s that is not blank, trimmed.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// entryMeta is an entry's second line: when, the app, how long it was
// spoken.
func entryMeta(e store.Entry) string {
	parts := []string{timeAgo(e.CreatedAt)} // shell_status.go
	if e.AppName != "" {
		parts = append(parts, e.AppName)
	}
	if e.TargetLang != "" {
		parts = append(parts, "→ "+targetLanguageLabel(e.TargetLang))
	}
	if e.Kind == store.KindRewrite && e.SourceText != "" {
		parts = append(parts, i18n.T("Selection"))
	}
	if e.AudioMS > 0 {
		parts = append(parts, clockMS(e.AudioMS))
	}
	return strings.Join(parts, " · ")
}

// entryMarks are an entry's status marks: failed or cancelled, or left
// in the answer card, or copied from it.
func entryMarks(c *ui.Context, e store.Entry) {
	k := tok(c.Theme())
	switch e.Status {
	case store.StatusFailed:
		tagChip(c, i18n.T("Failed")).TextColor(k.Danger)
		return
	case store.StatusCancelled:
		tagChip(c, i18n.T("Cancelled"))
		return
	}
	switch e.Delivery {
	case "card":
		tagChip(c, i18n.T("Not inserted")).Tooltip(i18n.T("Shown in the answer card, and not inserted"))
	case "copy":
		tagChip(c, i18n.T("Copied")).Tooltip(i18n.T("Copied from the answer card"))
	}
}

// targetLang is a translation's target language.
func targetLang(e store.Entry) string {
	if e.TargetLang == "" {
		return ""
	}
	return targetLanguageLabel(e.TargetLang)
}

// deliveryLabel says where a result went.
func deliveryLabel(e store.Entry) string {
	switch e.Delivery {
	case "insert":
		return i18n.T("Inserted")
	case "card":
		return i18n.T("Shown in the answer card")
	case "copy":
		return i18n.T("Copied")
	}
	return ""
}

// clockMS is a length as m:ss.
func clockMS(ms int64) string {
	s := (ms + 500) / 1000
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// stageTime is a stage's time: "850 ms", "1.2 s", "1:05"; "" for none.
func stageTime(ms int64) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 1000:
		return fmt.Sprintf("%d ms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1f s", float64(ms)/1000)
	}
	return clockMS(ms)
}

// recordingTime is the recording's length and, when the speech service
// got less of it (the silence left out), how much: "7.5 s, 2.5 s sent".
func recordingTime(e store.Entry) string {
	rec := stageTime(e.AudioMS)
	if rec == "" || e.SentMS <= 0 || e.SentMS >= e.AudioMS-50 {
		return rec
	}
	return i18n.T("%s, %s sent", rec, stageTime(e.SentMS))
}

// soundText is what the sound processing did to the speech service's
// copy of the recording (store.Entry.DSP): "Hum filtered, volume
// +7.5 dB"; "" for none.
func soundText(e store.Entry) string {
	hp, level := false, false
	for _, st := range strings.Split(e.DSP, "+") {
		hp, level = hp || st == "highpass", level || st == "level"
	}
	gain := fmt.Sprintf("%+.1f dB", e.GainDB)
	switch {
	case hp && level && e.GainDB != 0:
		return i18n.T("Hum filtered, volume %s", gain)
	case hp && level:
		return i18n.T("Hum filtered, volume unchanged")
	case hp:
		return i18n.T("Hum filtered")
	case level && e.GainDB != 0:
		return i18n.T("Volume %s", gain)
	case level:
		return i18n.T("Volume unchanged")
	}
	return ""
}
