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
// 440 wide (Voxt's card is 560, measured on the Mac: it fills its 560
// window; the user found it too wide, then 480 too, 10-15), 18 of padding on the sides and 16 under the text: as high as its text,
// so a short answer leaves no empty space (Voxt's card is always 220 of
// text high, 295 in all). It grows and shrinks about the overlay's anchor,
// as the pill does (overlay.go). It does not move, and nothing in it takes
// the keyboard (a non-activating window): Escape (the helper's event tap)
// closes it, the Rewrite shortcut follows up.

import (
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/compose"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/store"
)

// Sizes of the answer card, in DIPs: Voxt's (WaveformAnswerCard.swift,
// WaveformView.swift).
const (
	// ovAnswerW is the card's width, every card's: 440, the least that
	// holds the widest header (Translation, the language's name at its
	// widest, Insert, Copy, Close). Voxt's card fills its 560 window
	// (measured on the Mac, 10-15); the user found it too wide, then 480
	// too (10-15, third round).
	ovAnswerW  = 440
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
	// Where the rule is.
	ovCardRuleY   = ovCardPadTop + ovCardHeadH + ovCardRuleTop
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
	ovCardLangMaxW   = 128 // the language's name in its capsule, at most (Voxt 92: "Simplified Chinese" did not fit)
	ovCardWaveW      = 96  // a conversation's waveform

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
	// busy: another language, or a follow-up, is on its way (the
	// spinner, or the conversation's waveform). A first answer is written
	// in the pill: the card comes with it.
	busy bool
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
		return cardView{} // no card: the pill shows the session
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
	text := strings.TrimSpace(v.text)
	return []cardBlock{{text: ovSized{text, ovCardTextSize, ovCardTextWeight, ovCardBodyW}, color: ovCardText, selectable: text != ""}}
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
