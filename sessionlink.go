package main

// Sessions as the UI sees them (M3): the session manager
// (internal/session) wired to the helper, the providers and the store;
// the shortcuts (internal/hotkey) sent to the helper's event tap, with
// MyGo's Carbon shortcuts as the fallback without Accessibility; the
// snapshot the overlay, the Transcribe page and the menu bar read; and
// what their buttons do (stop, cancel, insert, copy, paste the last
// result).
//
// The manager runs sessions on goroutines; its snapshots come back
// through a.post (sessionChanged), newest revision wins. Starting a
// session checks on the main thread what can be checked without I/O (the
// helper, a speech service), so that a missing setup shows a notice
// instead of switching the microphone on.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
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

// liveFeatures are the features whose shortcuts are bound: Transcribe in
// M3 (Translate and Rewrite join in M4).
var liveFeatures = []string{hotkey.Transcribe}

// sessionState is the sessions' view state.
type sessionState struct {
	mgr *session.Manager
	// sess is the current (or last) session, as its newest snapshot said.
	sess session.Snapshot
	// answer is the answer card: open while a result waits there.
	answer answerCard
	// notice is the overlay's one-line message, when it shows one.
	notice *sessionNotice
	// hotkeysSent is the hotkey.configure last sent (as JSON), so the
	// same one is not sent again; carbon the accelerators registered
	// with MyGo while the event tap is missing.
	hotkeysSent string
	carbon      []string
	// lastLoaded: the last result was read from the history at start.
	lastLoaded bool
}

// answerCard is the answer card's state.
type answerCard struct {
	open      bool
	id        string // the session whose result it shows
	text      string
	target    proto.App
	inserting bool
	msg       string // why Insert did not work
}

// Notice actions: where the notice's button goes.
const (
	actionNone        = ""
	actionProviders   = "providers"
	actionPermissions = "permissions"
	actionHelper      = "helper"
)

// sessionNotice is a one-line message in the overlay (plan §3.2): an
// error and a way forward, hidden after a few seconds.
type sessionNotice struct {
	text, more string
	action     string
	warn       bool // a warning (the session went on), not an error
	seq        int  // which notice: its timer hides only itself
}

// noticeTime is how long a notice shows.
const noticeTime = 4 * time.Second

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
		Resolve: resolver(kr),
		History: lib.store.History(),
		Glossary: func(ctx context.Context) ([]store.Term, error) {
			return lib.store.Dictionary().List(ctx, "")
		},
		Settings: st.Get,
		Locale:   i18n.Locale,
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
// from kr (the Keychain: off the main thread).
func resolver(kr keyring) session.Resolver {
	return func(ctx context.Context, kind session.Kind, s settings.Settings) (session.Services, error) {
		hasKey := func(acc string) bool { v, err := kr.Get(acc); return err == nil && v != "" }
		feature := string(kind)
		f := providers.FeatureOf(s, feature)
		var svc session.Services
		pa, ok := providers.Choose(s, providers.ASR, f.ASR, hasKey)
		if !ok {
			return svc, fmt.Errorf("%s: %w", feature, session.ErrNoASR)
		}
		key, _ := kr.Get(providers.Account(providers.ASR, pa.ID))
		p, err := providers.NewASR(pa, providers.Env{Key: key, Proxy: s.Network.Proxy})
		if err != nil {
			return svc, err
		}
		svc.ASR = p
		svc.ASRInfo = session.Model{ID: pa.ID, Name: providers.Name(providers.ASR, pa), Model: providers.Model(providers.ASR, pa)}
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
	if s.Try {
		a.historyChanged() // history_page.go (a try keeps nothing, but the page may show it)
		return
	}
	if s.Card && s.Result != "" {
		a.answer = answerCard{open: true, id: s.ID, text: s.Result, target: s.Target}
		if s.Delivery == deliver.ReasonNoAccess {
			a.answer.msg = i18n.T("Allow Accessibility to insert results where you type.")
		}
		a.syncHotkeys() // Escape closes the card
	}
	if p := s.Problem; p != nil && s.State != session.Cancelled {
		text, more, action := sessionProblemText(p)
		if text != "" {
			a.showNotice(sessionNotice{text: text, more: more, action: action, warn: p.Degraded})
		}
	}
	if s.State != session.Cancelled {
		a.historyChanged()
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
	case session.ProblemLLM:
		msg, more := providerErrorText(p.Err)
		return i18n.T("Inserted without clean-up: %s", msg), more, actionNone
	case session.ProblemNoSpeech:
		return i18n.T("Nothing was heard."), "", actionNone
	case session.ProblemUnsupported:
		return i18n.T("This feature comes in a later version."), "", actionNone
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
	seq := 1
	if a.notice != nil {
		seq = a.notice.seq + 1
	}
	n.seq = seq
	a.notice = &n
	a.overlaySync()
	if a.win == nil {
		return // tests hide it by hand (closeNotice)
	}
	time.AfterFunc(noticeTime, func() {
		a.post(func() {
			if a.notice != nil && a.notice.seq == seq {
				a.closeNotice()
			}
		})
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
		a.navigate("/permissions")
	case actionHelper:
		a.restartHelper()
	}
}

// helperReady reports whether the helper can record.
func (a *app) helperReady() bool {
	return a.hstatus.State == helper.Ready
}

// transcribeBlocker checks, without I/O, what a session needs: a running
// helper and a speech service. It returns the notice that says what is
// missing, or nil.
func (a *app) transcribeBlocker() *sessionNotice {
	if !a.helperReady() {
		return &sessionNotice{text: i18n.T("The helper is not running."), action: actionHelper}
	}
	f := a.prefs.Features.Transcribe.Feature
	if _, ok := providers.Choose(a.prefs, providers.ASR, f.ASR, a.hasKey); !ok {
		return &sessionNotice{text: i18n.T("No speech service is set up."), action: actionProviders}
	}
	return nil
}

// startTranscribe starts a Transcribe session for the app target (the app
// in front when the shortcut fired); hold names the long-pressed binding;
// try keeps the result on the Transcribe page. When something it needs is
// missing it returns the notice saying so, which a shortcut's session
// shows in the overlay (a try shows it on the page); nil when it started
// (or a session runs already).
func (a *app) startTranscribe(target proto.App, hold string, try bool) *sessionNotice {
	if a.mgr == nil || a.sess.State.Live() {
		return nil
	}
	if n := a.transcribeBlocker(); n != nil {
		if !try {
			a.showNotice(*n)
		}
		return n
	}
	a.answer = answerCard{}
	a.notice = nil
	if _, err := a.mgr.Start(session.StartRequest{Kind: store.KindTranscribe, Target: target, HoldBinding: hold, Try: try}); err != nil {
		return nil // a session runs already
	}
	return nil
}

// stopSession stops the recording (the overlay's Stop, the menu bar).
func (a *app) stopSession() {
	if a.mgr != nil && a.sess.State == session.Recording {
		a.mgr.Stop(a.sess.ID)
	}
}

// cancelSession cancels the session (×, Escape).
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

// closeAnswer closes the answer card.
func (a *app) closeAnswer() {
	a.answer = answerCard{}
	a.syncHotkeys()
	a.overlaySync()
}

// copyAnswer copies the card's text (the button's handler gives the
// window's clipboard) and closes the card.
func (a *app) copyAnswer(copyText func(string)) {
	if !a.answer.open {
		return
	}
	copyText(a.answer.text)
	a.setDelivery(a.answer.id, "copy")
	a.toast(i18n.T("Copied"))
	a.closeAnswer()
}

// insertAnswer inserts the card's text into the app the session started
// in: the user chose to, so any focused element will do.
func (a *app) insertAnswer() {
	card := a.answer
	if !card.open || card.inserting {
		return
	}
	a.answer.inserting, a.answer.msg = true, ""
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
	case session.Recognizing, session.Composing, session.Delivering:
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
	if e.Binding == proto.HotkeyEscape && a.answer.open && !a.sess.State.Live() {
		a.closeAnswer()
		return
	}
	d := hotkey.Route(e, a.hotkeyPhase(), a.sess.HoldBinding)
	switch d.Action {
	case hotkey.Start:
		hold := ""
		if d.Hold {
			hold = e.Binding
		}
		if d.Feature == hotkey.Transcribe {
			a.startTranscribe(target, hold, false)
		}
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
