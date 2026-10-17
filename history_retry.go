package main

// Retry (重试) and Transcribe again (重新识别): a history entry made
// again from its recording (session.Retry), in the background, one at a
// time. A failed entry, or one that kept no text, offers Retry on its
// row (in place of the Failed mark), in its details and in its context
// menu; any other entry with a recording offers Transcribe again in its
// details and menu. The row and the details show the progress; the entry
// updates in place, and nothing is inserted (Copy takes the new text).
// The overlay's notice of a failed recognition opens the entry in History
// and retries it. From VoxtNew (4d5d5f62, 6443e13a).

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/store"
	"github.com/hehehai/Auralink/internal/uitext"
)

// entryRetry is the retry running: its entry and stage ("" for none).
type entryRetry struct {
	id    string
	stage session.State
}

// retryWanted: the entry failed or kept no text, so its recording is
// worth recognising again (Retry, not Transcribe again).
func retryWanted(e store.Entry) bool {
	_, failed := uitext.EntryTitle(e)
	return failed || e.Status == store.StatusFailed
}

// retryLabel is the action's name for the entry: Retry for one that
// failed, Transcribe again for the others.
func retryLabel(e store.Entry) string {
	if retryWanted(e) {
		return i18n.T("Retry")
	}
	return i18n.T("Transcribe again")
}

// retryStageText says what a retry does now.
func retryStageText(st session.State) string {
	if st == session.Composing {
		return i18n.T("Cleaning up…")
	}
	return i18n.T("Recognising…")
}

// retryBlocker says what a retry of a kind's entry lacks (memory only):
// a speech service (even to translate: the recording is recognised
// again), and the language model Translate and Rewrite need; "" for
// nothing.
func (a *app) retryBlocker(kind store.Kind) string {
	f := providers.FeatureOf(a.prefs, string(kind))
	if _, ok := providers.Choose(a.prefs, providers.ASR, f.ASR, a.hasKey); !ok {
		return i18n.T("No speech service is set up.")
	}
	if _, ok := providers.Choose(a.prefs, providers.LLM, f.LLM, a.hasKey); !ok && kind != store.KindTranscribe {
		return i18n.T("No language model is set up.")
	}
	return ""
}

// retryEntry makes the entry again from its recording, in the
// background; History's row and details show the progress, a toast the
// end.
func (a *app) retryEntry(e store.Entry) {
	if a.mgr == nil || a.hist.retry.id != "" || e.Audio == "" {
		return
	}
	if msg := a.retryBlocker(e.Kind); msg != "" {
		a.toast(msg)
		return
	}
	a.hist.retry = entryRetry{id: e.ID, stage: session.Recognizing}
	mgr := a.mgr
	a.async(func() {
		out, err := mgr.Retry(context.Background(), session.RetryRequest{Entry: e, OnStage: func(st session.State) {
			a.post(func() {
				if a.hist.retry.id == e.ID {
					a.hist.retry.stage = st
				}
			})
		}})
		a.post(func() {
			a.hist.retry = entryRetry{}
			switch {
			case err == nil:
				a.toast(i18n.T("Recognised again in %s", uitext.StageTime(out.TotalMS)))
			case errors.Is(err, session.ErrNoRecording):
				a.toast(i18n.T("The recording is gone."))
			case errors.Is(err, session.ErrRetrying):
				a.toast(i18n.T("Another entry is being retried."))
			default:
				msg, _ := uitext.ProviderErrorText(err)
				a.toast(i18n.T("Retry failed: %s", msg))
			}
			a.historyChanged()
		})
	})
}

// retryFromNotice opens a session's entry in History and retries it (the
// overlay's notice of a failed recognition): the entry is read first, as
// the session wrote it.
func (a *app) retryFromNotice(id string) {
	hs := a.lib.store.History()
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		e, err := hs.Get(ctx, id)
		a.post(func() {
			if err != nil {
				a.toast(i18n.T("The entry is gone."))
				return
			}
			a.showMainWindow()
			a.openHistoryEntry(e)
			a.retryEntry(e)
		})
	})
}

// entryStatus is a History row's marks: while it retries, the stage;
// Retry in place of Failed for an entry with a recording; else its marks.
func (a *app) entryStatus(c *ui.Context, e store.Entry) {
	t := c.Theme()
	k := tok(t)
	switch {
	case a.hist.retry.id == e.ID:
		ui.Row(c.Key("hist-retrying")).Gap(px(t, 4)).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Spinner(c).Size(px(t, 12), px(t, 12))
			ui.Text(c, retryStageText(a.hist.retry.stage)).FontSize(px(t, fsMicro)).TextColor(k.Text2).SingleLine()
		})
	case e.Audio != "" && retryWanted(e):
		b := ui.ButtonBase(c.Key("hist-retry-chip")).Padding(px(t, 1), px(t, 6)).Radius(px(t, 4)).Gap(px(t, 3)).AlignItems(ui.Center).Shrink(0).
			Label(i18n.T("Retry")).Tooltip(i18n.T("Recognise its recording again")).Disabled(a.hist.retry.id != "")
		bg := k.Hover
		if b.Hovered() && a.hist.retry.id == "" {
			bg = k.Pressed
		}
		b.Background(bg).Children(func() {
			ui.Icon(c, icons.Refresh.Linear).Size(px(t, 11), px(t, 11)).TextColor(k.Danger)
			ui.Text(c, i18n.T("Retry")).FontSize(px(t, fsMicro)).TextColor(k.Danger).SingleLine()
		})
		if b.Clicked() {
			a.retryEntry(e)
		}
	default:
		entryMarks(c, e)
	}
}

// entryRetryButton is the details' Retry or Transcribe again, or the
// progress while it runs; nothing for an entry without a recording.
func (a *app) entryRetryButton(c *ui.Context, e store.Entry) {
	t := c.Theme()
	if e.Audio == "" {
		return
	}
	if a.hist.retry.id == e.ID {
		ui.Row(c.Key("hist-detail-retrying")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(px(t, 16), px(t, 16))
			caption(c, retryStageText(a.hist.retry.stage))
		})
		return
	}
	label := retryLabel(e)
	b := secondaryIconButton(c.Key("hist-detail-retry"), icons.Refresh, label).Label(label).Disabled(a.hist.retry.id != "")
	if !retryWanted(e) {
		b.Tooltip(i18n.T("Recognise its recording again: the entry gets the new text, nothing is inserted"))
	}
	if b.Clicked() {
		a.retryEntry(e)
	}
}
