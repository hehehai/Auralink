package main

// The setup guide (plan §2.3; AudioSmith's wizard in look, Voxt's
// OnboardingGuide in what it checks): a dialog over the window on the
// first start, and again from Settings → General, Home or ⌘K. Seven
// steps, each of which can be skipped:
//
//	0 welcome and privacy     4 try Translate (the sample text, or speech)
//	1 permissions             5 try Rewrite (write from a spoken request)
//	2 services and keys       6 App enhancement and the dictionary
//	3 try Transcribe
//
// The tries are real sessions (the microphone, the speech service, the
// language model; nothing is inserted or kept: session.StartRequest.Try),
// started with Start or the feature's own shortcut while Auralink is in
// front. A step passes only when the session it started (by id) ends
// done with a result: a key press, a selection or an older session's late
// snapshot never does. While its try runs the guide cannot move on, and
// closing the guide cancels it. Closing or finishing marks the guide done
// (settings.Setup), so it does not open again by itself.

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// The guide's steps.
const (
	obWelcome = iota
	obPermissions
	obServices
	obTranscribe
	obTranslate
	obRewrite
	obMore
	obSteps
)

// onboardingState is the guide's state.
type onboardingState struct {
	ob struct {
		open bool
		step int
		// tries are the sessions each try step started, by step; passed
		// the steps whose try ended with a result.
		tries  map[int]string
		passed map[int]bool
		// block says why a try did not start; hint that another feature's
		// shortcut was pressed.
		block *sessionNotice
		hint  string
	}
}

// obStepKind is the feature a step tries.
func obStepKind(step int) (store.Kind, bool) {
	switch step {
	case obTranscribe:
		return store.KindTranscribe, true
	case obTranslate:
		return store.KindTranslate, true
	case obRewrite:
		return store.KindRewrite, true
	}
	return "", false
}

// obStepTitles are the steps' short names, for the breadcrumbs.
func obStepTitles() []string {
	return []string{i18n.T("Welcome"), i18n.T("Permissions"), i18n.T("Services"), i18n.T("Transcribe"),
		i18n.T("Translate"), i18n.T("Rewrite"), i18n.T("More")}
}

// openOnboarding opens the guide at its first step.
func (a *app) openOnboarding() {
	a.settingsOpen = false
	a.ob.open, a.ob.step = true, obWelcome
	a.ob.tries, a.ob.passed = map[int]string{}, map[int]bool{}
	a.ob.block, a.ob.hint = nil, ""
	a.recheckPermissions()
}

// maybeOnboard opens the guide on the first start (openWindow).
func (a *app) maybeOnboard() {
	if !a.prefs.Setup.Done {
		a.openOnboarding()
	}
}

// obOwns reports whether the session id is one of the guide's tries.
func (a *app) obOwns(id string) bool {
	if id == "" {
		return false
	}
	for _, t := range a.ob.tries {
		if t == id {
			return true
		}
	}
	return false
}

// obBusy reports whether one of the guide's tries runs.
func (a *app) obBusy() bool { return a.sess.State.Live() && a.obOwns(a.sess.ID) }

// obTry starts the step's try (Start); text starts Translate with it as
// the selection instead of recording.
func (a *app) obTry(text string) *sessionNotice {
	kind, ok := obStepKind(a.ob.step)
	if !ok {
		return nil
	}
	req := session.StartRequest{Kind: kind, Try: true}
	if text != "" {
		req.Text = &session.TextInput{Selection: text}
	}
	id, n := a.startSessionID(req)
	a.obTryStarted(id, n)
	return n
}

// obTryStarted keeps the step's try (id; "" when it did not start, and n
// says why).
func (a *app) obTryStarted(id string, n *sessionNotice) {
	a.ob.block, a.ob.hint = n, ""
	if id != "" {
		a.ob.tries[a.ob.step] = id
	}
}

// tryIgnored says, on the guide's try step, that the shortcut pressed is
// another feature's.
func (a *app) tryIgnored(kind store.Kind) {
	want, ok := obStepKind(a.ob.step)
	if !a.ob.open || !ok {
		return
	}
	keys := a.featureKeys(string(want))
	if keys == "" {
		a.ob.hint = i18n.T("That was the %s shortcut: this step tries %s.", kindTitle(kind), kindTitle(want))
		return
	}
	a.ob.hint = i18n.T("That was the %s shortcut: this step tries %s (%s).", kindTitle(kind), kindTitle(want), keys)
}

// obSessionChanged passes a step whose own try ended with a result.
func (a *app) obSessionChanged(s session.Snapshot) {
	if s.State != session.Done || strings.TrimSpace(s.Result) == "" {
		return
	}
	for step, id := range a.ob.tries {
		if id == s.ID {
			a.ob.passed[step] = true
		}
	}
}

// closeOnboarding closes the guide for good (Settings → General opens it
// again), cancelling its try.
func (a *app) closeOnboarding() {
	if a.obBusy() {
		a.cancelSession()
	}
	a.ob.open = false
	if !a.prefs.Setup.Done {
		a.updateSettings(func(s *settings.Settings) { s.Setup.Done = true })
	}
}

// obGo moves to a step (Back, Continue, Skip).
func (a *app) obGo(step int) {
	if a.obBusy() {
		return
	}
	a.ob.step = min(max(step, 0), obSteps-1)
	a.ob.block, a.ob.hint = nil, ""
	if a.ob.step == obPermissions {
		a.recheckPermissions()
	}
}

// onboardingRow is Settings → General's way back to the guide.
func (a *app) onboardingRow(c *ui.Context) {
	settingRow(c, "setup-guide", i18n.T("Setup guide"), i18n.T("Permissions, services and keys, and a try of each feature, as on the first day."), func() {
		if ui.Button(c.Key("setup-guide-open"), i18n.T("Open the setup guide")).Clicked() {
			a.openOnboarding()
		}
	})
}

// ---- The view ----

// onboarding is the guide over the window: the steps as breadcrumbs over
// a thin ink progress line, a big title, one column, Back at the bottom
// left and Continue at the bottom right.
func (a *app) onboarding(c *ui.Context) {
	if !a.ob.open {
		return
	}
	t := c.Theme()
	k := tok(t)
	w, h := c.Size()
	if a.sess.Try && a.sess.State == session.Recording {
		c.After(250 * time.Millisecond) // the try's clock
	}
	open := true
	ui.DialogBase(c.Key("onboarding"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35))
		panel.Width(min(px(t, 720), w-px(t, sp6))).Height(min(px(t, 600), h-px(t, sp6))).Radius(px(t, radiusCard)).Background(k.Bg).Border(1, k.Line).
			Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.3)).Label(i18n.T("Set up Auralink"))
		ui.Column(c).Fill().Children(func() {
			a.obHeader(c)
			ui.Scroll(c.Key("ob-scroll-" + itoa(a.ob.step))).Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).Padding(px(t, sp2), px(t, sp8), px(t, sp5)).Gap(px(t, sp3)).Children(func() {
					a.obTitle(c)
					switch a.ob.step {
					case obWelcome:
						a.obWelcomeStep(c)
					case obPermissions:
						a.permissionsSettings(c) // permissions.go
					case obServices:
						a.obServicesStep(c)
					case obTranscribe, obTranslate, obRewrite:
						a.obTryStep(c)
					default:
						a.obMoreStep(c)
					}
				})
			})
			ui.Box(c).Height(1).Background(k.Line)
			a.obFooter(c)
		})
	})
	if !open { // Escape or a click beside it
		a.closeOnboarding()
	}
}

// obHeader is the steps as breadcrumbs (passed ones with a check), ×, and
// the progress line under them.
func (a *app) obHeader(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	steps := obStepTitles()
	ui.Row(c).Padding(px(t, sp3), px(t, sp3), px(t, sp3), px(t, sp8)).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.Row(c).GapX(px(t, 6)).GapY(px(t, 2)).AlignItems(ui.Center).Grow(1).MinWidth(0).Wrap().Children(func() {
			for i, s := range steps {
				if i > 0 {
					ui.Icon(c, icons.AltArrowRight.Linear).Size(px(t, 12), px(t, 12)).TextColor(k.Text3)
				}
				ui.Row(c.Key("ob-step-" + itoa(i))).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
					col := k.Text3
					switch {
					case i == a.ob.step:
						col = k.Text
					case a.ob.passed[i]:
						col = k.Text2
						ui.Icon(c, icons.CheckCircle.Bold).Size(px(t, 12), px(t, 12)).TextColor(k.Accent)
					case i < a.ob.step:
						col = k.Text2
					}
					tx := ui.Text(c, s).FontSize(px(t, fsCaption)).TextColor(col).SingleLine()
					if i == a.ob.step {
						tx.FontWeight(600)
					}
				})
			}
		})
		if iconButton(c.Key("ob-close"), icons.Close, i18n.T("Close the guide")).Tooltip(i18n.T("Close the guide (Settings → General opens it again)")).Clicked() {
			a.closeOnboarding()
		}
	})
	frac := float32(a.ob.step+1) / float32(obSteps)
	ui.Box(c).Height(2).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, k.Line, 0)
		p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * frac, H: r.H}, k.Ink, 0)
	})
}

// obTitle is the step's big title and its line of explanation.
func (a *app) obTitle(c *ui.Context) {
	t := c.Theme()
	var title, sub string
	switch a.ob.step {
	case obWelcome:
		title = i18n.T("Welcome to Auralink")
		sub = i18n.T("Speak in any app and get text where your cursor is: transcribe, translate and rewrite with the speech and language services you choose. A few short steps; you can skip any of them.")
	case obPermissions:
		title = i18n.T("Permissions")
	case obServices:
		title = i18n.T("Speech and language services")
		sub = i18n.T("Set up a speech service and a language model: paste a key, then test the connection. More are on the Model services page.")
	case obTranscribe:
		title = i18n.T("Try Transcribe")
		sub = i18n.T("Press the shortcut (or Start), read the sentence below or say anything, then press it again to stop. The result shows here.")
	case obTranslate:
		title = i18n.T("Try Translate")
		sub = i18n.T("Translate selected text, or what you say. Translate the sample below, or press the shortcut (or Start) and say a sentence.")
	case obRewrite:
		title = i18n.T("Try Rewrite")
		sub = i18n.T("With nothing selected, say what to write; with text selected, say how to change it. Press the shortcut (or Start) and ask for something short.")
	default:
		title = i18n.T("More to explore")
		sub = i18n.T("Auralink is ready. Two more things make it yours.")
	}
	ui.Column(c).Gap(px(t, sp1)).Margin(px(t, sp3), 0, px(t, sp2)).Children(func() {
		ui.Text(c, title).FontSize(px(t, fsTitle)).FontWeight(600)
		if sub != "" {
			ui.Text(c, sub).TextColor(tok(t).Text2)
		}
	})
}

// obFooter is Back, Skip and Continue (Start using Auralink on the last
// step). A try step's Continue waits for its try to pass; Skip moves on
// without it. Nothing moves while the step's try runs.
func (a *app) obFooter(c *ui.Context) {
	t := c.Theme()
	busy := a.obBusy()
	_, tryStep := obStepKind(a.ob.step)
	ui.Row(c).Padding(px(t, sp3), px(t, sp5)).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		if a.ob.step > 0 && ghostIconButton(c.Key("ob-back"), icons.AltArrowLeft, i18n.T("Back")).Label(i18n.T("Back")).Disabled(busy).Clicked() {
			a.obGo(a.ob.step - 1)
		}
		ui.Spacer(c)
		if busy {
			caption(c, i18n.T("Finish or cancel the try first.")).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		}
		switch {
		case a.ob.step == obMore:
			if primaryButton(c.Key("ob-finish"), i18n.T("Start using Auralink")).Clicked() {
				a.closeOnboarding()
				a.navigate("/home")
			}
		case a.ob.step == obWelcome:
			if ghostButton(c.Key("ob-later"), i18n.T("Set up later")).Tooltip(i18n.T("Close the guide (Settings → General opens it again)")).Clicked() {
				a.closeOnboarding()
			}
			if primaryButton(c.Key("ob-next"), i18n.T("Get started")).Clicked() {
				a.obGo(a.ob.step + 1)
			}
		case tryStep && !a.ob.passed[a.ob.step]:
			if ghostButton(c.Key("ob-skip"), i18n.T("Skip this step")).Disabled(busy).Clicked() {
				a.obGo(a.ob.step + 1)
			}
			primaryButton(c.Key("ob-next"), i18n.T("Continue")).Disabled(true).Tooltip(i18n.T("A try that gives a result unlocks Continue"))
		default:
			if primaryButton(c.Key("ob-next"), i18n.T("Continue")).Disabled(busy).Clicked() {
				a.obGo(a.ob.step + 1)
			}
		}
	})
}

// obRow is a line of the guide: an icon, a title and a line under it.
func obRow(c *ui.Context, key string, ic icons.Icon, title, detail string) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key(key)).Gap(px(t, sp3)).AlignItems(ui.Start).Padding(px(t, 6), 0).Children(func() {
		ui.Icon(c, ic.Linear).Size(px(t, 20), px(t, 20)).TextColor(k.Text2).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
			ui.Text(c, title).FontWeight(600)
			if detail != "" {
				ui.Text(c, detail).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			}
		})
	})
}

// obWelcomeStep is the privacy promise (plan §6).
func (a *app) obWelcomeStep(c *ui.Context) {
	obRow(c, "ob-p-services", providersIcon, i18n.T("Your audio and text go only to the services you set up"),
		i18n.T("Auralink has no server of its own, no account and no analytics. Each service's own privacy terms apply to what it receives."))
	obRow(c, "ob-p-keys", icons.KeyMinimalisticSquare, i18n.T("Keys stay in this Mac's Keychain"),
		i18n.T("Never in Auralink's settings, history or logs."))
	obRow(c, "ob-p-local", icons.Database, i18n.T("History stays on this Mac"),
		i18n.T("Results (and, if you choose, their recordings) are kept here only. Turn them off in Settings → Privacy."))
	obRow(c, "ob-p-urls", appEnhancementIcon, i18n.T("Browser addresses only pick a scene"),
		i18n.T("With App enhancement, the address of the tab in front chooses a group of rules; it is never sent to a model."))
}

// obLLMPresets are the language models the guide offers: those set up,
// then a few that suit most people.
var obLLMPresets = []string{"deepseek", "dashscope", "volcengine", "openai"}

// obServicesStep lists the speech services and a few language models as
// the Model services page's cards: a click opens the same dialog (key,
// endpoint, model, Test connection).
func (a *app) obServicesStep(c *ui.Context) {
	t := c.Theme()
	w, _ := c.Size()
	room := min(px(t, 720), w-px(t, sp6)) - px(t, 2*sp8) - px(t, 4) // the column's padding, rounding
	cols := max(1, int((room+px(t, sp3))/(px(t, 260)+px(t, sp3))))
	cardW := (room - px(t, sp3)*float32(cols-1)) / float32(cols)
	for _, kind := range []providers.Kind{providers.ASR, providers.LLM} {
		ready := providers.Ready(a.prefs, kind, a.hasKey)
		title := i18n.T("Speech recognition")
		if kind == providers.LLM {
			title = i18n.T("Language model")
		}
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Margin(px(t, sp2), 0, 0, 0).Children(func() {
			sectionTitleText(c, title)
			if len(ready) > 0 {
				statusMark(c, icons.CheckCircle, tok(t).Success, i18n.T("Ready: %s", providerName(kind, ready[0])))
			}
		})
		if len(ready) == 0 {
			if kind == providers.ASR {
				caption(c, i18n.T("Turns what you say into text. Every feature needs one to work by voice."))
			} else {
				caption(c, i18n.T("Cleans up, translates and rewrites the text. Without one, Transcribe types the text as recognised."))
			}
		}
		var list []providers.Preset
		for _, pr := range providers.Presets(kind) {
			_, set := providers.Setup(a.prefs, kind, pr.ID)
			if kind == providers.ASR || set {
				list = append(list, pr)
			}
		}
		if kind == providers.LLM {
			for _, id := range obLLMPresets {
				if _, set := providers.Setup(a.prefs, kind, id); set {
					continue
				}
				if pr, ok := providers.LookupPreset(kind, id); ok {
					list = append(list, pr)
				}
			}
		}
		ui.Row(c.Key("ob-cards-" + string(kind))).Wrap().Gap(px(t, sp3)).AlignItems(ui.Start).Children(func() {
			for _, pr := range list {
				a.providerCard(c, kind, pr, cardW) // providers_page.go
			}
		})
	}
}

// obTryStep is a try step: the shortcut, a sample, the Try area, and
// the result once it passed.
func (a *app) obTryStep(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	kind, _ := obStepKind(a.ob.step)
	feature := string(kind)
	step := a.ob.step
	// The shortcut.
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
		hs := hotkey.HotkeysOf(a.prefs.Features, feature)
		if len(hs) == 0 {
			caption(c, i18n.T("%s has no shortcut: use Start, and add one on its page.", kindTitle(kind)))
			return
		}
		caption(c, i18n.T("Shortcut"))
		h := hs[0]
		ui.Text(c, hotkeyText(h.Keys)).FontSize(px(t, fsCaption)).Font("monospace").Padding(px(t, 2), px(t, 8)).Radius(px(t, 4)).Background(k.Hover)
		caption(c, triggerHowText(h.Trigger))
		if hotkey.NeedsTap(h) && !a.hotkeys.Installed {
			warnLine(c, i18n.T("This shortcut works once Auralink may use Accessibility; Start works now."))
		}
	})
	if a.ob.passed[step] {
		notice(c, "ob-passed", noticeOK, i18n.T("It works. Continue, or try again."), "", nil, nil)
	}
	// A sample.
	switch kind {
	case store.KindTranscribe:
		obSample(c, "ob-sample", i18n.T("Read this, or say anything"), i18n.T("Um, so the design review moves to Thursday afternoon, no, Friday morning, and I'll send the agenda tonight."), nil)
	case store.KindTranslate:
		target := session.TranslateTarget(a.prefs, i18n.Locale(), "")
		sample := i18n.T("Let's go camping in the mountains this weekend; remember to bring a warm jacket and a torch.")
		obSample(c, "ob-sample", i18n.T("Sample text · into %s", targetLanguageLabel(target)), sample, func() {
			if ui.Button(c.Key("ob-translate-sample"), i18n.T("Translate this text")).Disabled(a.sess.State.Live()).Clicked() {
				a.obTry(sample)
			}
		})
	case store.KindRewrite:
		obSample(c, "ob-sample", i18n.T("Say, for example"), i18n.T("Write a short note to the team saying I'll be ten minutes late to the stand-up."), nil)
	}
	if a.ob.hint != "" {
		warnLine(c, a.ob.hint)
	}
	id := a.ob.tries[step]
	a.tryCard(c, tryOpts{
		prefix: "ob-" + featurePrefix(feature), feature: feature, mine: id != "" && a.sess.ID == id,
		block: &a.ob.block, start: func() *sessionNotice { return a.obTry("") },
	})
}

// triggerHowText says how a shortcut's trigger is used.
func triggerHowText(trigger string) string {
	switch trigger {
	case settings.TriggerDouble:
		return i18n.T("Press twice quickly to start; once more to stop.")
	case settings.TriggerLong:
		return i18n.T("Record while you hold the keys; release to stop.")
	}
	return i18n.T("Press to start, press again to stop.")
}

// obSample is a sample text in a grey card, with a title and an action.
func obSample(c *ui.Context, key, title, text string, action func()) {
	t := c.Theme()
	k := tok(t)
	fillCard(c.Key(key)).Padding(px(t, sp3), px(t, sp4)).Gap(px(t, sp2)).Children(func() {
		ui.Text(c, title).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(k.Text2)
		ui.Text(c, text).Selectable()
		if action != nil {
			ui.Row(c).Children(action)
		}
	})
}

// obMoreStep introduces App enhancement and the dictionary, and lists
// the shortcuts.
func (a *app) obMoreStep(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	feature := func(key string, ic icons.Icon, title, about, button, path string) {
		card(c.Key(key)).Children(func() {
			ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, ic.Linear).Size(px(t, 20), px(t, 20)).TextColor(k.Text2).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, sp2)).Children(func() {
					ui.Text(c, title).FontWeight(600)
					caption(c, about)
					ui.Row(c).Children(func() {
						if ui.Button(c.Key(key+"-open"), button).Clicked() {
							a.closeOnboarding()
							a.navigate(path)
						}
					})
				})
			})
		})
	}
	feature("ob-ae", appEnhancementIcon, i18n.T("App enhancement"),
		i18n.T("Rules by app or website: a casual tone in chats, a formal one in mail, Return pressed after inserting in a messenger. The overlay shows which group applies."),
		i18n.T("Open App enhancement"), "/app-enhancement")
	feature("ob-dict", dictionaryIcon, i18n.T("Dictionary"),
		i18n.T("Names and jargon the speech service should expect and the language model should keep as written."),
		i18n.T("Open Dictionary"), "/dictionary")
	sectionTitle(c, i18n.T("Your shortcuts"))
	ui.Column(c.Key("ob-keys")).Gap(px(t, sp1)).Children(func() {
		row := func(title, keys string) {
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).Grow(1).MinWidth(0)
				if keys == "" {
					keys = i18n.T("None")
				}
				ui.Text(c, keys).FontSize(px(t, fsCaption)).Font("monospace").TextColor(k.Text2)
			})
		}
		for _, f := range hotkey.Features {
			row(featureTitle(f), a.featureKeys(f))
		}
		row(featureTitle(hotkey.PasteLast), hotkeyText(a.prefs.Features.PasteLast.Keys))
	})
}
