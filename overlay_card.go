package main

// The answer card (plan §3.2), redone after Voxt's WaveformAnswerCard
// (10-15, docs/behavior.md §5.24): where a Translate or Rewrite result
// that was not inserted waits, where an answer streams while the model
// writes it, and where a Rewrite answer takes follow-ups.
//
//	header   Voxt's answer icon (20), the title (15, semibold) and Voxt's
//	         arc spinner while it works; then its actions, 24 high:
//	         Translate's language capsule (it opens our own menu,
//	         overlay_lang.go), Continue (a Rewrite follow-up), Insert, Copy,
//	         Close (24 circles); a conversation shows Voxt's microphone and a
//	         small waveform in the title's place. 12 over it, 12 under it to
//	         the rule (Voxt's 16 and 8 looked off, 10-15)
//	rule     1 DIP, white 8 %, at 48 (Voxt's), 10 over the text
//	text     14 medium, white 90 %, selectable, as high as it is up to
//	         Voxt's 220, then it scrolls; a conversation's turns as
//	         bubbles instead
//	line     a small one under it, when there is something to say (why
//	         Insert did not work, another language that did not come)
//
// 560 wide (Voxt's, measured on the Mac: its card fills its 560 window),
// 18 of padding on the sides and 16 under the text: as high as its text,
// so a short answer leaves no empty space (Voxt's card is always 220 of
// text high, 295 in all). It grows and shrinks about the overlay's anchor,
// as the pill does (overlay.go). It does not move, and nothing in it takes
// the keyboard (a non-activating window): Escape (the helper's event tap)
// closes it, the Rewrite shortcut follows up.

import (
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/compose"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/store"
)

// Sizes of the answer card, in DIPs: Voxt's (WaveformAnswerCard.swift,
// WaveformView.swift).
const (
	ovAnswerW     = 560 // Voxt's answer window, which its card fills
	ovCardPadX = 18
	ovCardPadY = 16 // under the text (Voxt's)
	// The header: ovCardPadTop over it, ovCardRuleTop under it to the
	// rule, the same (10-15: Voxt's 16 and 8 left twice as much room over
	// the header as under it, which the user saw as off); the rule stays
	// at Voxt's 48.
	ovCardPadTop  = 12
	ovCardHeadH   = 24
	ovCardHeadGap = 12 // between the header's items
	ovCardRuleTop = 12 // the header to the rule
	ovCardRuleGap = 10 // the rule to the text
	ovCardRuleY   = ovCardPadTop + ovCardHeadH + ovCardRuleTop // where the rule is
	ovCardBodyMax = 220
	ovCardInset   = 10 // the text's right inset: the scroller's room
	ovCardBodyW   = ovAnswerW - 2*ovCardPadX - ovCardInset
	// ovCardChrome is the card's height without its text.
	ovCardChrome = ovCardPadTop + ovCardHeadH + ovCardRuleTop + 1 + ovCardRuleGap + ovCardPadY
	ovCardMsgGap = 8
	ovCardMsgH   = 16
	// ovCardMaxH is the highest card: the text at its most, and a line.
	ovCardMaxH = ovCardChrome + ovCardBodyMax + ovCardMsgGap + ovCardMsgH

	ovCardIcon       = 20
	ovCardTitleSize  = 15
	ovCardTextSize   = 14
	ovCardTextWeight = 500
	ovCardBtn        = 24
	ovCardBtnIcon    = 15
	ovCardLangMaxW   = 112 // the language's name in its menu, at most (Voxt 92: "Simplified Chinese" did not fit)
	ovCardWaveW      = 96 // a conversation's waveform

	// A conversation's bubbles.
	ovBubbleW         = 340
	ovBubblePadX      = 12
	ovBubblePadY      = 10
	ovBubbleGap       = 14
	ovBubbleTitleGap  = 7
	ovBubbleTextSize  = 13
	ovBubbleTitleSize = 11
	ovBubbleRadius    = 14

	ovProblemIcon = 16
	ovProblemGap  = 8
)

// Colours of the card: Voxt's.
var (
	ovCardText = ui.RGBA(255, 255, 255, 0.9)
	ovCardRule = ui.RGBA(255, 255, 255, 0.08)
)

// cardView is what the answer card shows: an ended session's card, or
// the answer the model is writing into it.
type cardView struct {
	kind  store.Kind
	title string
	text  string
	// placeholder shows while there is no text yet (what it does).
	placeholder string
	// streaming: the model writes the answer into it; busy: another
	// language, or a follow-up, is on its way (the spinner, or the
	// conversation's waveform).
	streaming, busy bool
	// recording: a follow-up records (the conversation's waveform moves).
	recording bool
	levels    []float64
	heard     bool
	failed    *session.Problem
	source    []string // a failure: what it worked on
	msg       string   // the small line under the text
	why       string   // why it is in the card (Insert's tip)
	lang      string   // Translate's target
	target    string   // the app it came from
	// conversation: the turns of a Rewrite conversation, as bubbles.
	conversation bool
	bubbles      []cardBubble
	// What the header offers.
	canInsert, canCopy, canFollowUp, canStop, canRetry, canLang bool
	copied                                                      bool
	follow                                                      string // the Rewrite shortcut's glyphs
}

// cardBubble is one turn's side in a conversation.
type cardBubble struct {
	user        bool
	title, text string
	live        bool // being said, or written
}

// cardNow is the card as it shows now.
func (a *app) cardNow() cardView {
	s := a.sess
	if !a.answer.open {
		// A fresh answer the model writes into the card.
		v := cardView{kind: s.Kind, text: s.Draft, streaming: true, lang: s.TargetLanguage, target: s.Target.Name,
			why: whyInCard(s.Delivery, s.Selection != "", s.Target.Name), placeholder: workingText(s)}
		v.title = cardTitle(s.Kind, s.Title)
		if s.Retried {
			v.msg = i18n.T("The first answer was not translated: asking again.")
		}
		return v
	}
	card := a.answer
	own := a.cardSessionLive()
	redo := own && s.ID == card.redo
	follow := own && !redo
	v := cardView{kind: card.kind, text: card.text, failed: card.problem, msg: card.msg, why: card.why, lang: card.lang,
		target: card.target.Name, copied: card.copied, busy: own, conversation: card.conversation || follow}
	v.title = cardTitle(card.kind, card.title)
	if redo {
		v.lang = card.redoLang
	}
	v.canCopy = card.text != ""
	v.canInsert = card.problem == nil && card.text != "" && !own
	v.canLang = card.kind == store.KindTranslate && card.problem == nil && !s.State.Live()
	v.canFollowUp = a.canFollowUp()
	v.canStop = follow && s.State == session.Recording
	v.canRetry = card.problem != nil && !s.State.Live()
	v.follow = a.featureKeys(hotkey.Rewrite)
	if v.failed != nil && !v.conversation {
		// What it worked on: the instruction, then the selection.
		in := card.input
		if card.kind == store.KindRewrite && in.Raw != "" && in.Selection != "" {
			v.source = append(v.source, in.Raw)
		}
		if src := cmpOr(in.Selection, in.Raw); src != "" {
			v.source = append(v.source, src)
		}
	}
	if !v.conversation {
		return v
	}
	v.recording = follow && s.State == session.Recording
	v.levels, v.heard = s.Levels, !s.HeardAt.IsZero()
	for _, t := range card.turns {
		if in := strings.TrimSpace(t.Instruction); in != "" {
			v.bubbles = append(v.bubbles, cardBubble{user: true, title: i18n.T("You"), text: in})
		}
		v.bubbles = append(v.bubbles, cardBubble{title: t.Answer.Title, text: t.Answer.Content})
	}
	switch {
	case follow:
		if said := cmpOr(strings.TrimSpace(s.Raw), strings.TrimSpace(s.Partial)); said != "" {
			v.bubbles = append(v.bubbles, cardBubble{user: true, title: i18n.T("You"), text: said, live: true})
		}
		if s.State != session.Recording {
			v.bubbles = append(v.bubbles, cardBubble{title: s.Title, text: s.Draft, live: true})
		}
	case card.problem != nil:
		// The follow-up that failed: what was said; why, under the card.
		if in := strings.TrimSpace(card.input.Raw); in != "" {
			v.bubbles = append(v.bubbles, cardBubble{user: true, title: i18n.T("You"), text: in})
		}
		if v.msg == "" {
			v.msg, _, _ = sessionProblemText(card.problem)
		}
	}
	return v
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// cardTitle is the card's heading: a Rewrite answer's own title, else
// what the card holds.
func cardTitle(kind store.Kind, title string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	switch kind {
	case store.KindTranslate:
		return i18n.T("Translation")
	case store.KindRewrite:
		return i18n.T("AI Answer") // Voxt's, a rewritten selection's too
	}
	return i18n.T("Transcription")
}

// cardRecording says whether the card shows a follow-up recording (its
// waveform moves).
func (a *app) cardRecording() bool {
	return a.answer.open && a.cardSessionLive() && a.sess.ID != a.answer.redo && a.sess.State == session.Recording
}

// cardBlock is one piece of the card's text, as it lays out: a text
// (wrapped where it draws), a failure's line, or a conversation's bubble.
type cardBlock struct {
	text  ovSized
	color ui.Color
	gap   float32 // before it
	// selectable: the answer (its text can be selected and copied).
	selectable bool
	// problem: a failure's line, its icon before it.
	problem bool
	// bubble: a conversation's turn, title its heading (text "" for
	// none), user whose side, live being said or written.
	bubble     bool
	title      ovSized
	user, live bool
}

// cardBlocks are the pieces of v's text, in order.
func cardBlocks(v cardView) []cardBlock {
	switch {
	case v.conversation:
		var out []cardBlock
		inner := float32(ovBubbleW - 2*ovBubblePadX)
		for i, b := range v.bubbles {
			text := strings.TrimSpace(b.text)
			if text == "" {
				text = "…"
			}
			blk := cardBlock{bubble: true, user: b.user, live: b.live, text: ovSized{text, ovBubbleTextSize, 500, inner}}
			// An answer without a title has no heading (Voxt's).
			if title := strings.TrimSpace(b.title); title != "" {
				blk.title = ovSized{title, ovBubbleTitleSize, 600, 0}
			}
			if i > 0 {
				blk.gap = ovBubbleGap
			}
			out = append(out, blk)
		}
		return out
	case v.failed != nil:
		text, more, _ := sessionProblemText(v.failed)
		out := []cardBlock{{problem: true, color: ovCardText,
			text: ovSized{text, ovCardTextSize, ovCardTextWeight, ovCardBodyW - ovProblemIcon - ovProblemGap}}}
		if more != "" {
			out = append(out, cardBlock{text: ovSized{more, 12, 0, ovCardBodyW}, color: ovText2, gap: 4, selectable: true})
		}
		for _, src := range v.source {
			out = append(out, cardBlock{text: ovSized{strings.TrimSpace(src), 13, 0, ovCardBodyW}, color: ovText2, gap: 10, selectable: true})
		}
		return out
	}
	text, color := strings.TrimSpace(v.text), ovCardText
	if text == "" {
		text, color = v.placeholder, ovText2
	}
	return []cardBlock{{text: ovSized{text, ovCardTextSize, ovCardTextWeight, ovCardBodyW}, color: color, selectable: !v.streaming && v.text != ""}}
}

// cardTexts are the texts the card's height depends on.
func cardTexts(v cardView) []ovSized {
	var out []ovSized
	for _, b := range cardBlocks(v) {
		out = append(out, b.text)
		if b.title.text != "" {
			out = append(out, b.title)
		}
	}
	return out
}

// blockHeight is how high b lays out, and whether that was measured.
func (a *app) blockHeight(b cardBlock) (float32, bool) {
	h, exact := a.ovTextHeight(b.text)
	if b.problem {
		h = max(h, ovProblemIcon)
	}
	if b.bubble {
		h += 2 * ovBubblePadY
		if b.title.text != "" {
			th, ok := a.ovTextHeight(b.title)
			h, exact = h+th+ovBubbleTitleGap, exact && ok
		}
	}
	return h, exact
}

// cardBodyHeight is the height of v's text in the card: all of it up to
// ovCardBodyMax (then it scrolls), and whether it was measured.
func (a *app) cardBodyHeight(v cardView) (int, bool) {
	var h float32
	exact := true
	for _, b := range cardBlocks(v) {
		bh, ok := a.blockHeight(b)
		h += b.gap + bh
		exact = exact && ok
	}
	return min(int(math.Ceil(float64(h))), ovCardBodyMax), exact
}

// cardHeight is the card's height (its width is ovAnswerW), and whether
// its texts were measured.
func (a *app) cardHeight() (int, bool) {
	v := a.cardNow()
	body, exact := a.cardBodyHeight(v)
	h := ovCardChrome + body
	if v.msg != "" {
		h += ovCardMsgGap + ovCardMsgH
	}
	return h, exact
}

// overlayCard is the answer card's content, in the face.
func (a *app) overlayCard(c *ui.Context) {
	v := a.cardNow()
	msgH := 0
	if v.msg != "" {
		msgH = ovCardMsgGap + ovCardMsgH
	}
	bodyH := float32(max(a.ov.h-ovCardChrome-msgH, 0))
	// The border takes ovBorder of the padding (MyGo's border box).
	ui.Column(c).Fill().Padding(ovCardPadTop-ovBorder, ovCardPadX-ovBorder, ovCardPadY-ovBorder).Children(func() {
		ui.Row(c).Height(ovCardHeadH).Shrink(0).Gap(ovCardHeadGap).AlignItems(ui.Center).Children(func() {
			a.cardHeader(c, v)
		})
		ui.Box(c).Height(1).Shrink(0).Margin(ovCardRuleTop, 0, 0, 0).Background(ovCardRule)
		a.cardBody(c, v, bodyH)
		if v.msg != "" {
			ui.Row(c).Height(ovCardMsgH).Shrink(0).Margin(ovCardMsgGap, 0, 0, 0).Gap(6).AlignItems(ui.Center).Label(v.msg).Children(func() {
				ic, col := icons.InfoCircle, ovText2
				if v.failed != nil {
					ic, col = icons.DangerCircle, tok(c.Theme()).Danger
				}
				ui.Icon(c, ic.Linear).Size(12, 12).TextColor(col).Shrink(0)
				ui.Text(c, v.msg).FontSize(12).TextColor(ovText2).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(v.msg)
			})
		}
	})
}

// cardHeader is the card's header: what it is, then what it offers.
func (a *app) cardHeader(c *ui.Context, v cardView) {
	// Voxt's: one answer icon for every single result, the microphone over
	// a conversation (its waveform beside it).
	ic := icons.VoxtAnswer
	if v.conversation {
		ic = icons.VoxtTranscribe
	}
	ui.Icon(c, ic.Bold).Size(ovCardIcon, ovCardIcon).TextColor(ui.RGB(255, 255, 255)).Opacity(ovCardIconA(v)).Shrink(0).Label(kindTitle(v.kind))
	if v.conversation {
		// Voxt's: a small waveform in the title's place, moving while a
		// follow-up records.
		cardWave(c, v, a.overlayMotion(), a.overlayPhase())
		ui.Spacer(c)
	} else {
		ui.Text(c, v.title).FontSize(ovCardTitleSize).FontWeight(600).TextColor(ui.RGB(255, 255, 255)).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		if v.streaming || v.busy {
			// Voxt's spinner: an arc turning once in 0.9 s, 88 %.
			ui.Icon(c, icons.VoxtSpinner.Linear).Size(12, 12).TextColor(ui.RGBA(255, 255, 255, 0.88)).Shrink(0).
				Rotate(float32(math.Mod(a.overlayPhase()*400, 360))).Label(cmpOr(v.placeholder, i18n.T("Translating…")))
		}
		ui.Box(c).Grow(1).MinWidth(0) // Voxt's Spacer(minLength: 12): the gap
	}
	if v.kind == store.KindTranslate && v.failed == nil {
		a.cardLanguage(c, v)
	}
	switch {
	case v.canStop:
		if cardCapsule(c.Key("ov-card-stop"), i18n.T("Done"), i18n.T("Stop recording the follow-up")) {
			a.closeLangMenu()
			a.stopSession()
		}
	case v.canFollowUp:
		tip := i18n.T("Follow up by voice")
		if v.follow != "" {
			tip = i18n.T("Follow up by voice (%s)", v.follow)
		}
		if cardCapsule(c.Key("ov-card-continue"), i18n.T("Continue"), tip) {
			a.closeLangMenu()
			a.followUpAnswer("")
		}
	case v.canRetry:
		if cardCapsule(c.Key("ov-card-retry"), i18n.T("Try again"), "") {
			a.closeLangMenu()
			a.retryAnswer("")
		}
	}
	if v.failed == nil {
		tip := i18n.T("Insert into %s", cmpOr(v.target, i18n.T("the app")))
		if v.why != "" {
			tip = v.why
		}
		if cardIconButton(c.Key("ov-card-insert"), i18n.T("Insert"), tip, icons.Pen, v.canInsert) {
			a.closeLangMenu()
			a.insertAnswer()
		}
		copyIcon, copyLabel := icons.Copy, i18n.T("Copy")
		if v.copied {
			copyIcon, copyLabel = icons.CopyCheck, i18n.T("Copied")
		}
		if cardIconButton(c.Key("ov-card-copy"), copyLabel, copyLabel, copyIcon, v.canCopy) {
			a.closeLangMenu()
			a.copyAnswer(c.Services().WriteClipboard)
		}
	}
	if cardIconButton(c.Key("ov-card-close"), i18n.T("Close"), "", icons.Close, true) {
		a.closeAnswer()
	}
}

// ovCardIconA is how opaque the card's icon is: Voxt's microphone over a
// conversation at 92 %, its answer icon whole (its bubble is dim itself).
func ovCardIconA(v cardView) float32 {
	if v.conversation {
		return 0.92
	}
	return 1
}

// cardSpinning says whether the card shows a spinner (it turns).
func (v cardView) spinning() bool { return !v.conversation && (v.streaming || v.busy) }

// cardIconButton is one of the header's round buttons (Voxt's
// AnswerHeaderActionButton): 24, white 8 % (16 % and a hairline under
// the pointer); dim when it cannot. It reports a click.
func cardIconButton(c *ui.Context, label, tip string, icon icons.Icon, enabled bool) bool {
	b := ui.ButtonBase(c).Size(ovCardBtn, ovCardBtn).Radius(ovCardBtn / 2).Center().Shrink(0).Label(label).Disabled(!enabled)
	if tip != "" {
		b.Tooltip(tip)
	}
	hover := enabled && b.Hovered()
	bg, line := ui.RGBA(255, 255, 255, 0.08), ui.RGBA(255, 255, 255, 0)
	switch {
	case !enabled:
		bg = ui.RGBA(255, 255, 255, 0.04)
	case hover:
		bg, line = ui.RGBA(255, 255, 255, 0.16), ui.RGBA(255, 255, 255, 0.18)
	}
	b.Background(bg).Border(1, line)
	alpha := float32(0.92)
	if !enabled {
		alpha = 0.42 * 0.92
	}
	return b.Children(func() {
		ui.Icon(c, icon.Linear).Size(ovCardBtnIcon, ovCardBtnIcon).TextColor(ui.RGBA(255, 255, 255, alpha))
	}).Clicked()
}

// cardCapsule is the header's text button (Voxt's AnswerContinueButton):
// 24 high, 11 semibold. It reports a click.
func cardCapsule(c *ui.Context, label, tip string) bool {
	b := ui.ButtonBase(c).Height(ovCardBtn).Padding(0, 10-ovBorder).Radius(ovCardBtn / 2).Center().Shrink(0).Label(label)
	if tip != "" {
		b.Tooltip(tip)
	}
	bg, line := ui.RGBA(255, 255, 255, 0.08), ui.RGBA(255, 255, 255, 0.1)
	if b.Hovered() {
		bg, line = ui.RGBA(255, 255, 255, 0.16), ui.RGBA(255, 255, 255, 0.18)
	}
	b.Background(bg).Border(1, line)
	return b.Children(func() {
		ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(ui.RGBA(255, 255, 255, 0.94)).SingleLine()
	}).Clicked()
}

// cardLanguage is Translate's target in the card (Voxt's capsule): its
// name and a chevron; a click opens our own menu of the languages
// (overlay_lang.go), where another translates the card's text again,
// into the card.
func (a *app) cardLanguage(c *ui.Context, v cardView) {
	open := a.answer.langOpen && v.canLang
	b := ui.ButtonBase(c.Key("ov-card-lang")).Height(ovCardBtn).Padding(0, 10-ovBorder).Radius(ovCardBtn / 2).Shrink(0).
		Label(i18n.T("Translate into")).Disabled(!v.canLang).Checked(open)
	line, bg := ui.RGBA(255, 255, 255, 0.12), ui.RGBA(255, 255, 255, 0.08)
	switch {
	case open:
		line, bg = tok(c.Theme()).Accent.Alpha(0.5), ui.RGBA(255, 255, 255, 0.14)
	case v.canLang && b.Hovered():
		line = tok(c.Theme()).Accent.Alpha(0.28)
	}
	b.Background(bg).Border(1, line)
	alpha := float32(1)
	if !v.canLang && !v.busy {
		alpha = 0.6
	}
	chevron := icons.AltArrowDown
	if open && a.prefs.Overlay.Position != "top" {
		chevron = icons.AltArrowUp // it opens upward
	}
	defer func() {
		if b.Clicked() {
			a.toggleLangMenu()
		}
	}()
	b.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, targetLanguageLabel(v.lang)).FontSize(11).FontWeight(600).TextColor(ui.RGBA(255, 255, 255, 0.92*alpha)).
				SingleLine().Ellipsis("…").MaxWidth(ovCardLangMaxW).MinWidth(0)
			ui.Icon(c, chevron.Bold).Size(10, 10).TextColor(ui.RGBA(255, 255, 255, 0.72*alpha)).Shrink(0)
		})
	})
}

// cardWave is a conversation's waveform (Voxt's AnswerConversationWaveView:
// its 16 bars at 0.84 × 0.82), 96 wide: the levels while a follow-up
// records, a quiet pattern otherwise.
func cardWave(c *ui.Context, v cardView, motion bool, phase float64) {
	const sx, sy = 0.84, 0.82
	bars := waveBars(v.levels)
	quiet := [...]float32{3.2, 3.9, 4.6, 5.1, 4.2, 3.5, 4.4, 4.9}
	label := i18n.T("Input level")
	ui.Row(c.Key("ov-card-wave")).Width(ovCardWaveW).Height(ovCardIcon).Shrink(0).Gap(waveBarGap*sx).AlignItems(ui.Center).Label(label).Children(func() {
		for i, lv := range bars {
			h := quiet[i%len(quiet)] * sy
			if v.recording && v.heard {
				h = (waveBarMinH + (waveBarMaxH-waveBarMinH)*float32(lv)) * sy
				if motion {
					h += float32(waveIdle(i, lv, phase)) * sy
				}
			}
			ui.Box(c).Width(waveBarW*sx).Height(h).Radius(waveBarR).Gradient(ui.RGBA(255, 255, 255, 0.98), ui.RGBA(255, 255, 255, 0.8), 180).Shrink(0)
		}
	})
}

// cardBody is the card's text, bodyH high: it scrolls past that. A
// conversation follows its newest turn while it shows the end.
func (a *app) cardBody(c *ui.Context, v cardView, bodyH float32) {
	st := &a.ov.cardScroll
	key := a.answer.id + "/" + a.sess.ID
	if v.conversation {
		key = a.answer.id + "/conversation"
	}
	if key != a.ov.cardScrollKey {
		a.ov.cardScrollKey = key
		st.X, st.Y = 0, 0
		if v.conversation {
			st.Y = math.MaxFloat32
		}
	} else if v.conversation && st.Y >= st.MaxY-2 {
		st.Y = math.MaxFloat32
	}
	ui.Scroll(c.Key("ov-card-body")).Height(bodyH).Shrink(0).Margin(ovCardRuleGap, 0, 0, 0).TrackScroll(st).Children(func() {
		ui.Column(c).Padding(0, ovCardInset, 0, 0).Children(func() {
			for i, b := range cardBlocks(v) {
				a.cardBlockView(c.Key(i), b)
			}
		})
	})
}

// cardBlockView draws one piece of the card's text.
func (a *app) cardBlockView(c *ui.Context, b cardBlock) {
	switch {
	case b.bubble:
		a.cardBubbleView(c, b)
	case b.problem:
		ui.Row(c).Gap(ovProblemGap).AlignItems(ui.Start).Margin(b.gap, 0, 0, 0).Children(func() {
			ui.Icon(c, icons.DangerCircle.Bold).Size(ovProblemIcon, ovProblemIcon).TextColor(tok(c.Theme()).Danger).Shrink(0)
			ui.Text(c, b.text.text).FontSize(b.text.size).FontWeight(b.text.weight).TextColor(b.color).Grow(1).MinWidth(0)
		})
	default:
		t := ui.Text(c, b.text.text).FontSize(b.text.size).TextColor(b.color).Margin(b.gap, 0, 0, 0)
		if b.text.weight != 0 {
			t.FontWeight(b.text.weight)
		}
		if b.selectable {
			t.Selectable()
		}
	}
}

// cardBubbleView is a conversation's turn (Voxt's
// RewriteConversationBubble): 340 wide, the user's on the right; an
// answer shows Copy under the pointer.
func (a *app) cardBubbleView(c *ui.Context, b cardBlock) {
	justify := ui.Start
	bg, line := ui.RGBA(255, 255, 255, 0.06), ui.RGBA(255, 255, 255, 0.08)
	titleA, textA := float32(0.64), float32(0.92)
	if b.user {
		justify, bg, line, titleA = ui.End, ui.RGBA(255, 255, 255, 0.1), ui.RGBA(255, 255, 255, 0.12), 0.7
	}
	if b.live {
		textA = 0.84
		if !b.user {
			titleA = 0.54
		}
	}
	ui.Row(c).Justify(justify).Margin(b.gap, 0, 0, 0).Children(func() {
		col := ui.Column(c.Key("bubble")).Width(ovBubbleW).Shrink(0).Padding(ovBubblePadY-ovBorder, ovBubblePadX-ovBorder).Gap(ovBubbleTitleGap).
			Radius(ovBubbleRadius).Background(bg).Border(1, line)
		if b.user {
			col.AlignItems(ui.End)
		}
		hover := !b.user && !b.live && col.Hovered()
		col.Children(func() {
			if b.title.text != "" {
				ui.Text(c, b.title.text).FontSize(ovBubbleTitleSize).FontWeight(600).TextColor(ui.RGBA(255, 255, 255, titleA)).SingleLine().Ellipsis("…").MaxWidth(ovBubbleW - 2*ovBubblePadX)
			}
			t := ui.Text(c, b.text.text).FontSize(ovBubbleTextSize).FontWeight(500).TextColor(ui.RGBA(255, 255, 255, textA))
			if b.user {
				t.TextAlign(ui.End)
			}
			if !b.live {
				t.Selectable()
			}
			if hover {
				copied := a.ov.bubbleCopied == b.text.text
				label, ic := i18n.T("Copy"), icons.Copy
				if copied {
					label, ic = i18n.T("Copied"), icons.CopyCheck
				}
				cb := ui.ButtonBase(c.Key("copy")).Absolute().Top(8).Right(8).Height(22).Padding(0, 8-ovBorder).Radius(11).
					Background(ui.RGBA(0, 0, 0, 0.74)).Border(1, ui.RGBA(255, 255, 255, 0.12)).Label(label)
				if cb.Children(func() {
					ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, ic.Linear).Size(12, 12).TextColor(ui.RGBA(255, 255, 255, 0.94))
						ui.Text(c, label).FontSize(10).FontWeight(600).TextColor(ui.RGBA(255, 255, 255, 0.94)).SingleLine()
					})
				}).Clicked() {
					a.copyBubble(c.Services().WriteClipboard, b)
				}
			}
		})
	})
}

// bubbleMark is how long a bubble's Copy shows its mark (Voxt's).
const bubbleMark = 1200 * time.Millisecond

// copyBubble copies an answer's bubble: its title, a blank line, its
// text (Voxt's).
func (a *app) copyBubble(copyText func(string), b cardBlock) {
	text := b.text.text
	if b.title.text != "" {
		text = b.title.text + "\n\n" + text
	}
	copyText(text)
	a.ov.bubbleCopied = b.text.text
	a.ov.bubbleSeq++
	seq := a.ov.bubbleSeq
	a.later(bubbleMark, func() {
		if a.ov.bubbleSeq == seq {
			a.ov.bubbleCopied = ""
			if a.ov.win != nil {
				a.ov.win.Invalidate()
			}
		}
	})
}

// targetLanguageLabel names a target language in the UI's language.
func targetLanguageLabel(code string) string {
	return compose.LanguageName(code, i18n.Locale())
}

// featureKeys are the glyphs of a feature's first shortcut ("" for none).
func (a *app) featureKeys(feature string) string {
	hs := hotkey.HotkeysOf(a.prefs.Features, feature)
	if len(hs) == 0 {
		return ""
	}
	return hotkeyText(hs[0].Keys)
}

// whyInCard says why a result is in the card and not in place, for
// Insert's tip ("" when the user asked for the card, or it went there
// for its length).
func whyInCard(r deliver.Reason, selection bool, name string) string {
	if name == "" {
		name = i18n.T("the app")
	}
	switch r {
	case deliver.ReasonNotFront, deliver.ReasonNoTarget:
		return i18n.T("%s is not in front any more: insert it there, or copy it.", name)
	case deliver.ReasonNotEditable:
		if selection {
			return i18n.T("The selection in %s cannot be edited: copy the result, or click where it should go and insert.", name)
		}
		return i18n.T("Nothing in %s takes text: click where it should go, then insert.", name)
	case deliver.ReasonNoAccess:
		return i18n.T("Allow Accessibility to insert results where you type.")
	}
	return ""
}
