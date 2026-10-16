package main

// Sessions as the UI sees them (M3, M4): the session manager
// (internal/session) wired to the helper, the providers and the store;
// the shortcuts (internal/hotkey) sent to the helper's event tap, with
// MyGo's Carbon shortcuts as the fallback without Accessibility; the
// snapshot the overlay, the Transcribe page and the menu bar read; and
// what their buttons do (stop, cancel, insert, copy, try again, another
// target language, paste the last result). The answer card also takes a
// Rewrite follow-up: the Rewrite shortcut while it shows a Rewrite
// answer records the next instruction for it.
//
// The manager runs sessions on goroutines; its snapshots come back
// through a.post (sessionChanged), newest revision wins. Starting a
// session checks on the main thread what can be checked without I/O (the
// helper, a speech service), so that a missing setup shows a notice
// instead of switching the microphone on.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/Auralink/internal/compose"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/llm"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// sessionSource is the logs' source of session lines.
const sessionSource = "session"

// liveFeatures are the features whose shortcuts are bound.
var liveFeatures = []string{hotkey.Transcribe, hotkey.Translate, hotkey.Rewrite}

// sessionState is the sessions' view state.
type sessionState struct {
	mgr *session.Manager
	// sess is the current (or last) session, as its newest snapshot said.
	sess session.Snapshot
	// answer is the answer card: open while a result waits there.
	answer answerCard
	// notice is the overlay's one-line message, when it shows one;
	// noticeSeq numbers them, so that a notice's timer hides only itself.
	notice    *sessionNotice
	noticeSeq int
	// hotkeysSent is the hotkey.configure last sent (as JSON), so the
	// same one is not sent again; carbon the accelerators registered
	// with MyGo while the event tap is missing.
	hotkeysSent string
	carbon      []string
	// lastLoaded: the last result was read from the history at start.
	lastLoaded bool
	// followUp is the answer card's session a running Rewrite follows up
	// on ("" for none): its answer joins the card's conversation.
	followUp string
}

// answerCard is the answer card's state (overlay_card.go draws it).
type answerCard struct {
	open      bool
	id        string // the session whose result it shows
	kind      store.Kind
	text      string
	title     string // Rewrite's answer title ("" for none)
	target    proto.App
	inserting bool   // Insert pastes: the card is hidden meanwhile (Voxt's)
	msg       string // a line under the text: why Insert, or another language, did not work
	// why says why the result is in the card and not in place (Insert's
	// tip: Voxt says nothing; one line of orange was too loud, 10-15).
	why string
	// problem: the session failed (the card shows why, and Try again);
	// input is what it worked on, for Try again and another language.
	problem  *session.Problem
	input    session.TextInput
	lang     string // Translate's target
	langOpen bool   // the language menu shows (overlay_lang.go)
	// turns are a Rewrite conversation so far, the shown answer last
	// (a follow-up adds to them); conversation: a follow-up began, and
	// the card shows them as one (Voxt's conversation mode), from then on.
	turns        []compose.Turn
	conversation bool
	// redo is the session translating the text into redoLang, another
	// language chosen in the card, while it runs: the card keeps the text
	// it shows, with a spinner, until the new one replaces it (Voxt's).
	redo, redoLang string
	// copied: Copy's mark shows (2 s, Voxt's); copySeq numbers the
	// copies, so that a copy's timer clears only its own mark.
	copied  bool
	copySeq int
}

// Notice actions: where the notice's button goes.
const (
	actionNone        = ""
	actionProviders   = "providers"
	actionPermissions = "permissions"
	actionHelper      = "helper"
)

// sessionNotice is a one-line message in the overlay (plan §3.2), where
// the pill's waveform goes: an error and a way forward (a click), hidden
// after a few seconds.
type sessionNotice struct {
	text, more string
	action     string
	warn       bool // a warning (the session went on), not an error
	seq        int  // which notice: its timer hides only itself
}

// noticeTime is how long a notice shows.
const noticeTime = 5 * time.Second

// openSessions makes the session manager; connectHelper calls it once the
// supervisor exists.
func (a *app) openSessions() {
	sup := a.host.sup
	kr := a.keys
	lib := a.lib
	logs := a.logs
	st := a.settings
	a.mgr = session.New(session.Config{
		Audio:   session.HelperAudio{H: sup},
		Text:    session.HelperText{H: sup},
		Select:  session.HelperText{H: sup},
		Resolve: resolver(kr),
		History: lib.store.History(),
		Glossary: func(ctx context.Context) ([]store.Term, error) {
			return lib.store.Dictionary().List(ctx, "")
		},
		Settings: st.Get,
		Locale:   i18n.Locale,
		Scene:    a.sceneConfig(), // appenhance.go
		Self:     os.Getpid(),
		Activate: func(pid int) bool { ok, _ := activateTarget(pid); return ok },
		OnChange: func(s session.Snapshot) { a.post(func() { a.sessionChanged(s) }) },
		Log: func(level, msg string) {
			logs.Append(core.LogEntry{Time: time.Now(), Level: core.Severity(level), Source: sessionSource, Message: msg})
		},
		NewID: func() string { return core.NewID(time.Now()) },
	})
	a.sess = a.mgr.Current()
}

// resolver builds a session's services from the settings, with the keys
// from kr (the Keychain: off the main thread). A service that is not set
// up stays nil: the session says what it lacks when it needs it (a
// selection to translate needs no speech service).
func resolver(kr keyring) session.Resolver {
	return func(ctx context.Context, kind session.Kind, s settings.Settings) (session.Services, error) {
		hasKey := func(acc string) bool { v, err := kr.Get(acc); return err == nil && v != "" }
		feature := string(kind)
		f := providers.FeatureOf(s, feature)
		var svc session.Services
		if pa, ok := providers.Choose(s, providers.ASR, f.ASR, hasKey); ok {
			key, _ := kr.Get(providers.Account(providers.ASR, pa.ID))
			p, err := providers.NewASR(pa, providers.Env{Key: key, Proxy: s.Network.Proxy})
			if err != nil {
				return svc, err
			}
			svc.ASR = p
			svc.ASRInfo = session.Model{ID: pa.ID, Name: providers.Name(providers.ASR, pa), Model: providers.Model(providers.ASR, pa)}
		}
		if kind == store.KindTranscribe && !s.Features.Transcribe.Enhance {
			return svc, nil
		}
		pl, ok := providers.Choose(s, providers.LLM, f.LLM, hasKey)
		if !ok {
			return svc, nil // no language model: the text goes on as recognised
		}
		lkey, _ := kr.Get(providers.Account(providers.LLM, pl.ID))
		b, err := providers.NewLLM(pl, providers.Env{Key: lkey, Proxy: s.Network.Proxy})
		if err != nil {
			return svc, nil
		}
		model := providers.Model(providers.LLM, pl)
		svc.LLM = modelClient{c: &llm.Client{Backend: b, Retry: llm.RetryPolicy{MaxAttempts: 2}}, model: model}
		svc.LLMInfo = session.Model{ID: pl.ID, Name: providers.Name(providers.LLM, pl), Model: model}
		return svc, nil
	}
}

// modelClient sends a session's chat to the setup's model, at the skill's
// temperature.
type modelClient struct {
	c     *llm.Client
	model string
}

func (m modelClient) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	if req.Model == "" {
		req.Model = m.model
	}
	if req.Temperature == nil {
		req.Temperature = llm.F64(llm.DefaultTemperature(req.Skill))
	}
	return m.c.Chat(ctx, req)
}

// sessionChanged takes a session's snapshot (the newest wins) and
// updates the overlay, the shortcuts and the menu bar.
func (a *app) sessionChanged(s session.Snapshot) {
	if s.Rev <= a.sess.Rev {
		return
	}
	was := a.sess
	a.sess = s
	a.obSessionChanged(s) // onboarding.go: a try of the setup guide passes
	a.playCue(was, s)     // settings_audio.go: the start and stop sounds
	if s.ID != was.ID {
		a.notice = nil
	}
	if s.State.Ended() && !was.State.Ended() {
		a.sessionEnded(s)
	}
	if s.State.Live() != was.State.Live() {
		a.syncHotkeys()
		a.buildTrayMenu() // tray.go: Start or Stop
	}
	a.overlaySync() // overlay.go
}

// sessionEnded opens the answer card or shows a notice, and refreshes the
// history.
func (a *app) sessionEnded(s session.Snapshot) {
	if s.State == session.Done && strings.TrimSpace(s.Result) != "" {
		a.markTried() // home_page.go: Home stops offering its Try area
	}
	if s.Try {
		a.historyChanged() // history_page.go (a try keeps nothing, but the page may show it)
		return
	}
	switch {
	case a.answer.open && a.answer.redo != "" && a.answer.redo == s.ID:
		a.endRedo(s)
	case s.Card && (s.Result != "" || s.State == session.Failed):
		a.openAnswer(s)
		a.syncHotkeys() // Escape closes the card
	}
	if s.ID != "" && a.followUp != "" && !s.Card {
		a.followUp = "" // a follow-up that ended without an answer: the card keeps the last one
	}
	if p := s.Problem; p != nil && s.State != session.Cancelled && !(s.Card && s.State == session.Failed) {
		text, more, action := sessionProblemText(p)
		if text != "" {
			// Nothing heard is not a failure: a warning.
			a.showNotice(sessionNotice{text: text, more: more, action: action, warn: p.Degraded || p.Code == session.ProblemNoSpeech})
		}
	}
	if s.State != session.Cancelled {
		a.historyChanged()
	}
}

// openAnswer opens the answer card on an ended session: its result (or
// why it failed), what it worked on, a Rewrite conversation's turns.
func (a *app) openAnswer(s session.Snapshot) {
	card := answerCard{
		open: true, id: s.ID, kind: s.Kind, text: s.Result, title: s.Title, target: s.Target,
		input: session.TextInput{Raw: s.Raw, Selection: s.Selection}, lang: s.TargetLanguage,
		why: whyInCard(s.Delivery, s.Selection != "", s.Target.Name),
	}
	if s.State == session.Failed {
		card.problem = s.Problem
	}
	if s.Delivery == deliver.ReasonNoAccess {
		card.msg = i18n.T("Allow Accessibility to insert results where you type.")
	}
	if s.Kind == store.KindRewrite {
		// A follow-up continues the card's conversation.
		if a.answer.kind == store.KindRewrite && a.answer.id == a.followUp {
			card.turns = append(card.turns, a.answer.turns...)
			card.conversation = a.answer.conversation
			if card.why == "" {
				card.why = a.answer.why
			}
		}
		if card.problem == nil {
			// Any answer takes follow-ups, a rewritten selection's too
			// (Voxt's): the selection goes with its turn.
			card.turns = append(card.turns, compose.Turn{Instruction: s.Raw, Selection: s.Selection, Answer: compose.Answer{Title: s.Title, Content: s.Result}})
		} else if card.problem != nil && len(card.turns) > 0 {
			card.input.Raw = s.Raw // Try again asks the same follow-up
		}
	}
	a.followUp = ""
	if card.target.PID == 0 && a.answer.open {
		card.target = a.answer.target
	}
	a.answer = card
}

// endRedo ends the card's translation into another language: its text
// replaces the card's; a failure (or nothing) leaves the card as it was,
// in the language it was, and says so under it (Voxt goes back to the
// language without a word); a cancel just goes back.
func (a *app) endRedo(s session.Snapshot) {
	lang := a.answer.redoLang
	a.answer.redo, a.answer.redoLang = "", ""
	switch {
	case s.State == session.Done && s.Result != "":
		a.answer.id, a.answer.text, a.answer.lang = s.ID, s.Result, s.TargetLanguage
		a.answer.msg, a.answer.copied = "", false
	case s.State == session.Cancelled:
	default:
		why := i18n.T("the language model answered nothing")
		if s.Problem != nil && s.Problem.Code != session.ProblemEmpty && s.Problem.Code != session.ProblemNoSpeech {
			why, _, _ = sessionProblemText(s.Problem)
		}
		a.answer.msg = i18n.T("Not translated into %s: %s", targetLanguageLabel(lang), why)
	}
}

// sessionProblemText says what went wrong in a session, its detail (the
// service's own message) and where the notice's button goes. "" for a
// problem that needs no notice.
func sessionProblemText(p *session.Problem) (text, more, action string) {
	switch p.Code {
	case session.ProblemNoASR:
		return i18n.T("No speech service is set up."), "", actionProviders
	case session.ProblemMicrophone:
		return i18n.T("Auralink may not use the microphone."), errText(p.Err), actionPermissions
	case session.ProblemDevice:
		return i18n.T("No microphone gave any sound."), errText(p.Err), actionNone
	case session.ProblemHelper:
		return i18n.T("The helper stopped: try again in a moment."), errText(p.Err), actionHelper
	case session.ProblemASR:
		msg, more := providerErrorText(p.Err)
		return i18n.T("Speech recognition failed: %s", msg), more, actionProviders
	case session.ProblemNoLLM:
		return i18n.T("No language model is set up."), "", actionProviders
	case session.ProblemEmpty:
		return i18n.T("The language model answered nothing."), "", actionNone
	case session.ProblemLLM:
		if !p.Degraded {
			msg, more := providerErrorText(p.Err)
			return i18n.T("The language model failed: %s", msg), more, actionProviders
		}
		_, more := providerErrorText(p.Err)
		var le *llm.Error
		if errors.Is(p.Err, context.DeadlineExceeded) || errors.As(p.Err, &le) && le.Code == llm.CodeTimeout {
			return i18n.T("Inserted as recognised: the language model did not answer in time."), more, actionNone
		}
		return i18n.T("Inserted as recognised: the language model failed."), more, actionNone
	case session.ProblemNoSpeech:
		return i18n.T("Nothing was heard."), "", actionNone
	case session.ProblemUnsupported:
		return i18n.T("This feature is not available."), "", actionNone
	case session.ProblemTooShort:
		return "", "", actionNone
	}
	return i18n.T("Something went wrong: %s", errText(p.Err)), "", actionNone
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// showNotice shows a notice in the overlay; it hides by itself.
func (a *app) showNotice(n sessionNotice) {
	a.noticeSeq++
	seq := a.noticeSeq
	n.seq = seq
	a.notice = &n
	a.overlaySync()
	a.later(noticeTime, func() {
		if a.notice != nil && a.notice.seq == seq {
			a.closeNotice()
		}
	})
}

// closeNotice hides the notice.
func (a *app) closeNotice() {
	a.notice = nil
	a.overlaySync()
}

// noticeAction runs a notice's button.
func (a *app) noticeAction(action string) {
	a.closeNotice()
	switch action {
	case actionProviders:
		a.showMainWindow()
		a.navigate("/providers")
	case actionPermissions:
		a.showMainWindow()
		a.navigate("/settings/permissions")
	case actionHelper:
		a.restartHelper()
	}
}

// helperReady reports whether the helper can record.
func (a *app) helperReady() bool {
	return a.hstatus.State == helper.Ready
}

// transcribeBlocker checks, without I/O, what a Transcribe session
// needs (sessionBlocker).
func (a *app) transcribeBlocker() *sessionNotice { return a.sessionBlocker(store.KindTranscribe) }

// sessionBlocker checks, without I/O, what a session of kind needs: a
// running helper, a speech service (Transcribe, Rewrite: Translate may
// translate a selection without one) and a language model (Translate,
// Rewrite). It returns the notice that says what is missing, or nil.
func (a *app) sessionBlocker(kind store.Kind) *sessionNotice {
	if !a.helperReady() {
		return &sessionNotice{text: i18n.T("The helper is not running."), action: actionHelper}
	}
	f := providers.FeatureOf(a.prefs, string(kind))
	if kind != store.KindTranslate {
		if _, ok := providers.Choose(a.prefs, providers.ASR, f.ASR, a.hasKey); !ok {
			return &sessionNotice{text: i18n.T("No speech service is set up."), action: actionProviders}
		}
	}
	if kind != store.KindTranscribe {
		if _, ok := providers.Choose(a.prefs, providers.LLM, f.LLM, a.hasKey); !ok {
			return &sessionNotice{text: i18n.T("No language model is set up."), action: actionProviders}
		}
	}
	return nil
}

// startTranscribe starts a Transcribe session (startSession).
func (a *app) startTranscribe(target proto.App, hold string, try bool) *sessionNotice {
	return a.startSession(session.StartRequest{Kind: store.KindTranscribe, Target: target, HoldBinding: hold, Try: try})
}

// startSession starts a session for the app req.Target (the app in front
// when the shortcut fired); req.HoldBinding names the long-pressed
// binding; a try keeps the result on its page. When something it needs
// is missing it returns the notice saying so, which a shortcut's session
// shows in the overlay (a try shows it on the page); nil when it started
// (or a session runs already).
func (a *app) startSession(req session.StartRequest) *sessionNotice {
	_, n := a.startSessionID(req)
	return n
}

// startSessionID is startSession that also returns the session's id ("" when
// none started), so a Try area can tell its own session from any other.
func (a *app) startSessionID(req session.StartRequest) (string, *sessionNotice) {
	if a.mgr == nil || a.sess.State.Live() {
		return "", nil
	}
	if n := a.sessionBlocker(req.Kind); n != nil {
		if !req.Try {
			a.showNotice(*n)
		}
		return "", n
	}
	if len(req.Previous) == 0 {
		a.answer = answerCard{}
	}
	a.notice = nil
	id, err := a.mgr.Start(req)
	if err != nil {
		return "", nil // a session runs already
	}
	return id, nil
}

// startTry starts a try of kind (a Try area's Start).
func (a *app) startTry(kind store.Kind) (string, *sessionNotice) {
	return a.startSessionID(session.StartRequest{Kind: kind, Try: true})
}

// retryAnswer starts the card's session again with the text it worked on
// (Try again after a failure), or into the language lang (Translate's
// target select). The card stays until the new session's own result.
func (a *app) retryAnswer(lang string) {
	card := a.answer
	if !card.open || a.sess.State.Live() {
		return
	}
	in := card.input
	req := session.StartRequest{Kind: card.kind, Target: card.target, Text: &in, TargetLanguage: lang}
	if req.TargetLanguage == "" {
		req.TargetLanguage = card.lang
	}
	if lang != "" && card.problem == nil {
		// Another language for the card's text: it stays in the card,
		// which shows its text until the new one comes (Voxt's).
		req.Card = true
		a.answer.msg = ""
		if n := a.sessionBlocker(card.kind); n != nil {
			a.answer.msg = n.text
			return
		}
		id, err := a.mgr.Start(req)
		if err == nil {
			a.answer.redo, a.answer.redoLang = id, lang
		}
		a.syncHotkeys()
		a.overlaySync()
		return
	}
	if card.kind == store.KindRewrite && len(card.turns) > 0 && in.Selection == "" {
		req.Previous = card.turns
		if card.problem == nil {
			return // nothing to ask again
		}
		a.followUp = card.id
	}
	a.notice = nil
	if n := a.sessionBlocker(card.kind); n != nil {
		a.answer.msg = n.text
		return
	}
	if _, err := a.mgr.Start(req); err == nil && len(req.Previous) == 0 {
		a.answer = answerCard{}
	}
	a.syncHotkeys()
	a.overlaySync()
}

// followUpAnswer records a follow-up to the card's Rewrite answer (the
// Rewrite shortcut while the card shows one): the next instruction
// rewrites that answer, in the card.
func (a *app) followUpAnswer(hold string) bool {
	card := a.answer
	if !a.canFollowUp() {
		return false
	}
	a.followUp = card.id
	if n := a.startSession(session.StartRequest{Kind: store.KindRewrite, Target: card.target, HoldBinding: hold, Previous: card.turns}); n != nil {
		a.followUp = ""
	} else {
		// The card turns into a conversation, where the follow-up records
		// (Voxt's).
		a.answer.conversation, a.answer.msg = true, ""
		a.overlaySync()
	}
	return true
}

// canFollowUp says whether the card takes a follow-up now: a Rewrite
// answer written from an instruction, nothing running.
func (a *app) canFollowUp() bool {
	card := a.answer
	return card.open && card.kind == store.KindRewrite && card.problem == nil && len(card.turns) > 0 && !a.sess.State.Live()
}

// stopSession stops the recording (the shortcut, the menu bar).
func (a *app) stopSession() {
	if a.mgr != nil && a.sess.State == session.Recording {
		a.mgr.Stop(a.sess.ID)
	}
}

// cancelSession cancels the session (Escape, the answer card's Close).
func (a *app) cancelSession() {
	if a.mgr != nil && a.sess.State.Live() {
		a.mgr.Cancel(a.sess.ID)
	}
}

// toggleTranscribe is the menu bar's Start / Stop transcribing.
func (a *app) toggleTranscribe() {
	switch {
	case a.sess.State == session.Recording:
		a.stopSession()
	case !a.sess.State.Live():
		a.startTranscribe(a.front, "", false)
	}
}

// closeAnswer closes the answer card (and cancels a follow-up, or another
// language, on its way).
func (a *app) closeAnswer() {
	if a.cardSessionLive() {
		a.cancelSession()
	}
	a.answer, a.followUp = answerCard{}, ""
	a.syncHotkeys()
	a.overlaySync()
}

// cardSessionLive says whether the session running is the card's own: a
// follow-up, or the text into another language.
func (a *app) cardSessionLive() bool {
	s := a.sess
	return a.answer.open && s.State.Live() && !s.Try && (a.followUp != "" && a.followUp == a.answer.id || s.ID == a.answer.redo)
}

// copyMark is how long Copy shows its mark (Voxt's).
const copyMark = 2 * time.Second

// copyAnswer copies the card's text (the button's handler gives the
// window's clipboard); the card stays, Copy showing a mark for a moment
// (Voxt's).
func (a *app) copyAnswer(copyText func(string)) {
	if !a.answer.open || a.answer.text == "" {
		return
	}
	copyText(a.answer.text)
	a.setDelivery(a.answer.id, "copy")
	a.answer.copySeq++
	seq, id := a.answer.copySeq, a.answer.id
	a.answer.copied = true
	a.later(copyMark, func() {
		if a.answer.open && a.answer.copySeq == seq && a.answer.id == id {
			a.answer.copied = false
			if a.ov.win != nil {
				a.ov.win.Invalidate()
			}
		}
	})
}

// insertAnswer inserts the card's text into the app the session started
// in: the user chose to, so any focused element will do. The card hides
// while it pastes and comes back if that did not work (Voxt's).
func (a *app) insertAnswer() {
	card := a.answer
	if !card.open || card.inserting || card.text == "" || a.cardSessionLive() {
		return
	}
	a.answer.inserting, a.answer.msg = true, ""
	a.overlaySync()
	text := session.HelperText{H: a.host.sup}
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := deliver.Insert(ctx, text, deliver.Request{Text: card.text, Target: card.target, AnyEditable: true, Self: os.Getpid(),
			Activate: func(pid int) bool { ok, _ := activateTarget(pid); return ok }})
		a.post(func() {
			if !a.answer.open || a.answer.id != card.id {
				return
			}
			a.answer.inserting = false
			if res.Inserted {
				a.setDelivery(card.id, "insert")
				a.closeAnswer()
				return
			}
			a.answer.msg = deliveryText(res, card.target)
			a.overlaySync()
		})
	})
}

// deliveryText says why a result was not inserted.
func deliveryText(res deliver.Result, target proto.App) string {
	name := target.Name
	if name == "" {
		name = i18n.T("the app")
	}
	switch res.Reason {
	case deliver.ReasonNoAccess:
		return i18n.T("Allow Accessibility to insert results where you type.")
	case deliver.ReasonNotFront, deliver.ReasonNoTarget:
		return i18n.T("%s is not in front any more.", name)
	case deliver.ReasonNotEditable:
		return i18n.T("Click where the text should go in %s, then insert.", name)
	case deliver.ReasonFailed:
		return i18n.T("Could not insert: %v", res.Err)
	}
	return ""
}

// setDelivery records in the history how a result left the card.
func (a *app) setDelivery(id, how string) {
	h := a.lib.store.History()
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e, err := h.Get(ctx, id)
		if err != nil {
			return // not kept
		}
		e.Delivery = how
		if err := h.Put(ctx, e); err == nil {
			a.post(a.historyChanged)
		}
	})
}

// pasteLast pastes the last result into target (⌃⌘V, the menu bar).
func (a *app) pasteLast(target proto.App) {
	if a.mgr == nil {
		return
	}
	text := a.mgr.Last()
	if text == "" {
		a.showNotice(sessionNotice{text: i18n.T("Nothing to paste yet.")})
		return
	}
	io := session.HelperText{H: a.host.sup}
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := deliver.Insert(ctx, io, deliver.Request{Text: text, Target: target})
		a.post(func() {
			if !res.Inserted {
				a.showNotice(sessionNotice{text: deliveryText(res, target)})
			}
		})
	})
}

// loadLast reads the newest delivered result from the history, for
// "paste the last result" after a restart.
func (a *app) loadLast() {
	if a.lastLoaded || a.mgr == nil {
		return
	}
	a.lastLoaded = true
	h, mgr := a.lib.store.History(), a.mgr
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		es, err := h.List(ctx, store.HistoryQuery{Limit: 20})
		if err != nil {
			return
		}
		for _, e := range es {
			if e.Status == store.StatusDone && strings.TrimSpace(e.Result) != "" {
				mgr.SetLast(e.Result)
				return
			}
		}
	})
}

// ---- shortcuts ----

// hotkeyPhase is the session's phase for routing.
func (a *app) hotkeyPhase() hotkey.Phase {
	switch a.sess.State {
	case session.Recording:
		return hotkey.Recording
	case session.Reading, session.Recognizing, session.Composing, session.Delivering:
		return hotkey.Busy
	}
	return hotkey.Idle
}

// hotkeyFired acts on a shortcut (the helper's hotkey.event, or a Carbon
// shortcut).
func (a *app) hotkeyFired(e proto.HotkeyEventParams) {
	target := a.front
	if e.Front != nil {
		target = *e.Front
	}
	if e.Binding == proto.HotkeyEscape && a.answer.langOpen {
		a.closeLangMenu() // Escape closes the menu first, then the card
		return
	}
	if e.Binding == proto.HotkeyEscape && a.answer.open {
		switch {
		case !a.sess.State.Live() || a.sess.ID == a.answer.redo:
			// Voxt's: Escape closes the card, and stops another language
			// on its way.
			a.closeAnswer()
			return
		case a.followUp != "" && a.followUp == a.answer.id:
			// A follow-up: Escape cancels it, the card stays (Voxt's).
			a.cancelSession()
			return
		}
	}
	d := hotkey.Route(e, a.hotkeyPhase(), a.sess.HoldBinding)
	switch d.Action {
	case hotkey.Start:
		hold := ""
		if d.Hold {
			hold = e.Binding
		}
		if d.Feature == hotkey.Rewrite && a.followUpAnswer(hold) {
			return
		}
		kind := store.KindTranscribe
		switch d.Feature {
		case hotkey.Translate:
			kind = store.KindTranslate
		case hotkey.Rewrite:
			kind = store.KindRewrite
		}
		req := session.StartRequest{Kind: kind, Target: target, HoldBinding: hold}
		// Auralink in front, on a page with a Try area for it: a try there.
		switch place := a.tryPlace(kind, target); place {
		case placeNone:
		case placeIgnore:
			a.tryIgnored(kind) // onboarding.go
			return
		default:
			req.Try = true
			id, n := a.startSessionID(req)
			a.tryStarted(place, kind, id, n)
			return
		}
		a.startSession(req)
	case hotkey.Stop:
		a.stopSession()
	case hotkey.Cancel:
		a.cancelSession()
	case hotkey.Paste:
		a.pasteLast(target)
	}
}

// hotkeyPlan is what the settings' shortcuts become.
func (a *app) hotkeyPlan() hotkey.Plan { return hotkey.Build(a.prefs.Features, liveFeatures) }

// syncHotkeys sends the shortcuts to the helper (and flips "active" while
// a session runs or the answer card shows), and keeps the Carbon
// fallback in step with the event tap.
func (a *app) syncHotkeys() {
	plan := a.hotkeyPlan()
	a.syncCarbon(plan)
	if !a.helperReady() {
		a.hotkeysSent = ""
		return
	}
	params := plan.Configure(a.sess.State.Live() || a.answer.open)
	key := fmt.Sprintf("%+v", params)
	if key == a.hotkeysSent {
		return
	}
	a.hotkeysSent = key
	helperCall(a, proto.MethodHotkeyConfigure, params, 5*time.Second, func(st proto.HotkeyStatus, err error) {
		if err != nil {
			a.hotkeysSent = ""
			a.logs.Append(core.LogEntry{Time: time.Now(), Level: core.SeverityWarning, Source: sessionSource, Message: "hotkey.configure: " + err.Error()})
			return
		}
		was := a.hotkeys.Installed
		a.hotkeys = st
		if st.Installed != was {
			a.syncCarbon(a.hotkeyPlan())
		}
	})
}

// syncCarbon registers the plan's key shortcuts with MyGo while the
// helper's event tap is not installed (no Accessibility), and removes
// them once it is. Only the real app (it needs mygo.App).
func (a *app) syncCarbon(plan hotkey.Plan) {
	if !a.hasMenu {
		return
	}
	var want []string
	byAcc := map[string]string{}
	if !a.hotkeys.Installed {
		for _, c := range plan.Carbon {
			want = append(want, c.Accelerator)
			byAcc[c.Accelerator] = c.ID
		}
	}
	if strings.Join(want, "\n") == strings.Join(a.carbon, "\n") {
		return
	}
	for _, acc := range a.carbon {
		mygo.GlobalShortcut.Unregister(acc)
	}
	a.carbon = nil
	for _, acc := range want {
		id := byAcc[acc]
		err := mygo.GlobalShortcut.Register(acc, func() {
			a.post(func() {
				front := a.front
				a.hotkeyFired(proto.HotkeyEventParams{Binding: id, Phase: proto.PhaseTrigger, Behavior: proto.BehaviorTap, Front: &front})
			})
		})
		if err != nil {
			a.logs.Append(core.LogEntry{Time: time.Now(), Level: core.SeverityWarning, Source: sessionSource, Message: "shortcut " + acc + ": " + err.Error()})
			continue
		}
		a.carbon = append(a.carbon, acc)
	}
}

// closeSessions cancels a running session and removes the Carbon
// shortcuts; it runs on quit.
func (a *app) closeSessions() {
	a.cancelSession()
	if a.hasMenu {
		for _, acc := range a.carbon {
			mygo.GlobalShortcut.Unregister(acc)
		}
	}
	a.carbon = nil
}
