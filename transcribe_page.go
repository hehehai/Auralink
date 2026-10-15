package main

// The feature pages (plan §3.1): Transcribe (/transcribe), Translate
// (/translate) and Rewrite (/rewrite) share one structure: the feature's
// shortcuts (and, on Transcribe, the shared "paste the last result"), the
// services it uses, its own settings (Transcribe's clean-up layers;
// Translate's target language, clean-up and card; Rewrite's instruction
// clean-up and when an answer goes to the card), the user's rules with a
// preview of the whole prompt, and a Try area that runs a session whose
// result stays on the page.
//
// Every change goes through a.updateSettings, which also sends the
// shortcuts to the helper again. A page keeps only what its controls
// edit (the selects' labels, a custom shortcut being typed, the rules
// before they are saved), set again from the settings when they change
// elsewhere; each feature has its own (featureUI).

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/compose"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// transcribeState is the feature pages' view state, one featureUI per
// feature (hotkey.Transcribe, Translate, Rewrite).
type transcribeState struct {
	features map[string]*featureUI
}

// featureUI is a feature page's view state.
type featureUI struct {
	// rows are the shortcuts' controls, paste paste-last's (Transcribe);
	// seen is what they were last set from.
	rows  []keyRow
	paste keyRow
	seen  string
	// asr and llm are the services' selects; svcSeen what they were
	// last set from.
	asr, llm string
	svcSeen  string
	// lang is Translate's target select.
	lang string
	// prompt is the rules as typed; promptSeen the saved value it was
	// last set from; promptDirty an edit not saved yet, promptSeq which
	// edit (its timer saves only the last one).
	prompt      string
	promptSeen  string
	promptDirty bool
	promptSeq   int
	preview     bool
	// block says why the Try area's Start did not start.
	block *sessionNotice
}

// fui is feature's page state.
func (a *app) fui(feature string) *featureUI {
	if a.features == nil {
		a.features = map[string]*featureUI{}
	}
	u := a.features[feature]
	if u == nil {
		u = &featureUI{}
		a.features[feature] = u
	}
	return u
}

// featureKind is a feature's session kind.
func featureKind(feature string) store.Kind {
	switch feature {
	case hotkey.Translate:
		return store.KindTranslate
	case hotkey.Rewrite:
		return store.KindRewrite
	}
	return store.KindTranscribe
}

// featurePrefix keys a feature page's controls.
func featurePrefix(feature string) string {
	switch feature {
	case hotkey.Translate:
		return "tl"
	case hotkey.Rewrite:
		return "rw"
	}
	return "tr"
}

// keyRow is a shortcut's controls.
type keyRow struct {
	sel    string // the select's label
	custom bool   // "Custom…" chosen: the keys are typed
	text   string // the typed keys
	trig   int    // 0 tap, 1 double tap, 2 long press
	edited bool   // the typed keys are not saved yet
}

// keyPresets are the shortcuts the selects offer, in the grammar of
// internal/hotkey.
var keyPresets = []string{
	"Fn", "Fn+Shift", "Fn+Control", "Fn+Option",
	"RightCommand", "RightOption", "RightShift", "RightControl",
	"Option+Space", "Control+Option+Space",
}

// triggers are the settings' triggers in the segmented control's order.
var triggers = []string{settings.TriggerTap, settings.TriggerDouble, settings.TriggerLong}

func triggerIndex(t string) int {
	if i := slices.Index(triggers, t); i >= 0 {
		return i
	}
	return 0
}

// trySample is the input the prompt preview shows.
func trySample() string {
	return i18n.T("um so the meeting moves to Thursday at three, no, at four")
}

// ---- Text of shortcuts and their problems ----

// hotkeyText shows keys as macOS writes them: "fn ⇧", "Right ⌘",
// "⌃⌥ Space", "⌃⌘V". Keys that do not parse show as written.
func hotkeyText(keys string) string {
	c, err := hotkey.Parse(keys)
	if err != nil {
		return strings.TrimSpace(keys)
	}
	glyph := map[string]string{proto.ModControl: "⌃", proto.ModOption: "⌥", proto.ModShift: "⇧", proto.ModCommand: "⌘"}
	var parts []string
	if _, ok := c.Mods[proto.ModFn]; ok {
		parts = append(parts, "fn")
	}
	for _, f := range []string{proto.ModControl, proto.ModOption, proto.ModShift, proto.ModCommand} {
		side, ok := c.Mods[f]
		if !ok {
			continue
		}
		switch side {
		case proto.SideLeft:
			parts = append(parts, i18n.T("Left %s", glyph[f]))
		case proto.SideRight:
			parts = append(parts, i18n.T("Right %s", glyph[f]))
		default:
			parts = append(parts, glyph[f])
		}
	}
	if c.Key != "" {
		parts = append(parts, keyGlyph(c.Key))
	}
	// Glyphs run together (⌃⌥⇧⌘V); words keep a space around them.
	var b strings.Builder
	for i, p := range parts {
		if i > 0 && (len([]rune(p)) > 1 || len([]rune(parts[i-1])) > 1) {
			b.WriteString(" ")
		}
		b.WriteString(p)
	}
	return b.String()
}

// keyGlyph is a key as macOS shows it.
func keyGlyph(k string) string {
	switch k {
	case "Space":
		return i18n.T("Space")
	case "Return":
		return "↩"
	case "Tab":
		return "⇥"
	case "Escape":
		return "⎋"
	case "Delete":
		return "⌫"
	case "ForwardDelete":
		return "⌦"
	case "Left":
		return "←"
	case "Right":
		return "→"
	case "Up":
		return "↑"
	case "Down":
		return "↓"
	case "PageUp":
		return "⇞"
	case "PageDown":
		return "⇟"
	case "Home":
		return "↖"
	case "End":
		return "↘"
	}
	return k
}

// parseErrorText says why typed keys do not parse.
func parseErrorText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, hotkey.ErrEmpty), errors.Is(err, hotkey.ErrNoModifier):
		return i18n.T("Type the keys, as in Control+Option+Space.")
	case errors.Is(err, hotkey.ErrTwoKeys):
		return i18n.T("Only one key besides the modifiers.")
	case errors.Is(err, hotkey.ErrFnSide):
		return i18n.T("fn has no left or right side.")
	}
	return i18n.T("A key is unknown. Write the modifiers first, then one key: Fn, RightCommand, Control+Option+Space, Fn+F5.")
}

// featureTitle names a feature (or paste-last) in a problem.
func featureTitle(feature string) string {
	switch feature {
	case hotkey.Translate:
		return i18n.T("Translate")
	case hotkey.Rewrite:
		return i18n.T("Rewrite")
	case hotkey.PasteLast:
		return i18n.T("Paste the last result")
	}
	return i18n.T("Transcribe")
}

// systemShortcutName names what macOS uses keys for.
func systemShortcutName(sys string) string {
	switch sys {
	case hotkey.SystemSpotlight:
		return i18n.T("Spotlight")
	case hotkey.SystemFinderSearch:
		return i18n.T("Finder search")
	case hotkey.SystemInputSource:
		return i18n.T("switching input sources")
	case hotkey.SystemAppSwitcher:
		return i18n.T("the app switcher")
	case hotkey.SystemWindows:
		return i18n.T("switching windows")
	}
	return i18n.T("copy, paste and other editing commands")
}

// hotkeyProblemText says what is wrong with a binding.
func hotkeyProblemText(p hotkey.Problem) string {
	switch p.Code {
	case hotkey.ProblemInvalid:
		return parseErrorText(p.Err)
	case hotkey.ProblemDuplicate:
		return i18n.T("The same keys as shortcut %d.", p.OtherIndex+1)
	case hotkey.ProblemConflict:
		return i18n.T("%s has the same keys and trigger: only one of them works.", featureTitle(p.Other))
	case hotkey.ProblemSystem:
		return i18n.T("macOS uses these keys for %s.", systemShortcutName(p.System))
	case hotkey.ProblemBareKey:
		return i18n.T("A key alone would be taken from every app: add ⌃, ⌥, ⇧, ⌘ or fn.")
	}
	return ""
}

// hotkeyProblems are the problems to show under feature's bindings, by
// index: its own, and the conflicts reported on another feature's
// binding that clash with it.
func hotkeyProblems(f settings.Features, feature string) map[int][]string {
	out := map[int][]string{}
	for _, p := range hotkey.Check(f) {
		switch {
		case p.Feature == feature:
			out[p.Index] = append(out[p.Index], hotkeyProblemText(p))
		case p.Code == hotkey.ProblemConflict && p.Other == feature:
			out[p.OtherIndex] = append(out[p.OtherIndex],
				i18n.T("%s has the same keys and trigger: only one of them works.", featureTitle(p.Feature)))
		}
	}
	return out
}

// ---- The page ----

// transcribePage builds the Transcribe page.
func (a *app) transcribePage(c *ui.Context) { a.featurePage(c, hotkey.Transcribe) }

// translatePage builds the Translate page.
func (a *app) translatePage(c *ui.Context) { a.featurePage(c, hotkey.Translate) }

// rewritePage builds the Rewrite page.
func (a *app) rewritePage(c *ui.Context) { a.featurePage(c, hotkey.Rewrite) }

// featurePage builds a feature's page.
func (a *app) featurePage(c *ui.Context, feature string) {
	t := c.Theme()
	p := featurePrefix(feature)
	pageHeader(c, featureTitle(feature), nil)
	if a.sess.Try && a.sess.State == session.Recording {
		c.After(250 * time.Millisecond) // the Try area's clock
	}
	ui.Scroll(c.Key(p + "-scroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(0, px(t, sp6), px(t, sp6)).Gap(px(t, sp3)).MaxWidth(px(t, 880)).Children(func() {
			switch feature {
			case hotkey.Translate:
				caption(c, i18n.T("Select text and press the shortcut to translate it in place; with nothing selected, speak, and the translation is typed where your cursor is."))
			case hotkey.Rewrite:
				caption(c, i18n.T("Select text and say how to change it (\"shorter\", \"more formal\"); with nothing selected, say what to write. The answer goes where your cursor is."))
			default:
				caption(c, i18n.T("Press a shortcut anywhere, speak, and the text is typed where your cursor is."))
			}
			a.featureShortcuts(c, feature)
			a.featureServices(c, feature)
			switch feature {
			case hotkey.Translate:
				a.translateSettings(c)
			case hotkey.Rewrite:
				a.rewriteSettings(c)
			default:
				a.trEnhance(c)
			}
			a.featureRules(c, feature)
			a.featureTry(c, feature)
		})
	})
}

// ---- Shortcuts ----

// featureSync sets a feature's shortcut controls from the settings when
// they changed elsewhere (or the language did: the labels are
// translated).
func (a *app) featureSync(feature string) {
	u := a.fui(feature)
	seen := a.featureKeysKey(feature)
	if seen == u.seen {
		return
	}
	u.seen = seen
	hs := hotkey.HotkeysOf(a.prefs.Features, feature)
	u.rows = make([]keyRow, len(hs))
	for i, h := range hs {
		u.rows[i] = keyRow{sel: chordLabel(h.Keys), text: h.Keys, trig: triggerIndex(h.Trigger)}
	}
	pl := a.prefs.Features.PasteLast.Keys
	u.paste = keyRow{sel: chordLabel(pl), text: pl}
	if pl == "" {
		u.paste.sel = i18n.T("Off")
	}
}

func (a *app) featureKeysKey(feature string) string {
	f := a.prefs.Features
	return fmt.Sprint(hotkey.HotkeysOf(f, feature), f.PasteLast, i18n.Locale())
}

// chordLabel is the select's label of keys: the preset they are, or their
// own glyphs.
func chordLabel(keys string) string {
	if p := presetOf(keys); p != "" {
		return hotkeyText(p)
	}
	return hotkeyText(keys)
}

// presetOf is the preset keys are, "" for none.
func presetOf(keys string) string {
	c, err := hotkey.Parse(keys)
	if err != nil {
		return ""
	}
	for _, p := range keyPresets {
		if pc, _ := hotkey.Parse(p); pc.Equal(c) {
			return p
		}
	}
	return ""
}

// keyOptions are a select's options for a binding whose keys are cur:
// the presets, cur when it is none of them, then "Custom…" (and "Off"
// first with off). byLabel maps each label to its keys.
func keyOptions(cur string, off bool) (opts []string, byLabel map[string]string) {
	byLabel = map[string]string{}
	if off {
		opts = append(opts, i18n.T("Off"))
		byLabel[i18n.T("Off")] = ""
	}
	for _, p := range keyPresets {
		l := hotkeyText(p)
		opts = append(opts, l)
		byLabel[l] = p
	}
	if cur != "" && presetOf(cur) == "" {
		l := hotkeyText(cur)
		if _, dup := byLabel[l]; !dup {
			opts = append(opts, l)
			byLabel[l] = cur
		}
	}
	opts = append(opts, i18n.T("Custom…"))
	return opts, byLabel
}

// setHotkeys changes a feature's shortcuts with fn, on the settings' own
// copy.
func (a *app) setHotkeys(feature string, resync bool, fn func(hs []settings.Hotkey) []settings.Hotkey) {
	a.updateSettings(func(s *settings.Settings) {
		hs := &s.Features.Transcribe.Hotkeys
		switch feature {
		case hotkey.Translate:
			hs = &s.Features.Translate.Hotkeys
		case hotkey.Rewrite:
			hs = &s.Features.Rewrite.Hotkeys
		}
		*hs = fn(slices.Clone(*hs))
	})
	u := a.fui(feature)
	if resync {
		u.seen = ""
	} else {
		u.seen = a.featureKeysKey(feature) // the controls already show it
	}
}

// newHotkey is the shortcut "Add a shortcut" adds: the first preset no
// feature uses.
func newHotkey(f settings.Features) settings.Hotkey {
	for _, p := range keyPresets {
		pc, _ := hotkey.Parse(p)
		used := false
		for _, feat := range append(slices.Clone(hotkey.Features), hotkey.PasteLast) {
			for _, h := range hotkey.HotkeysOf(f, feat) {
				if hc, err := hotkey.Parse(h.Keys); err == nil && hc.Overlaps(pc) {
					used = true
				}
			}
		}
		if !used {
			return settings.Hotkey{Keys: p, Trigger: settings.TriggerTap}
		}
	}
	return settings.Hotkey{Keys: keyPresets[0], Trigger: settings.TriggerDouble}
}

func (a *app) featureShortcuts(c *ui.Context, feature string) {
	t := c.Theme()
	p := featurePrefix(feature)
	u := a.fui(feature)
	a.featureSync(feature)
	sectionTitleTip(c, i18n.T("Shortcuts"), i18n.T("Tap: press to start, press again to stop. Double tap: press twice quickly. Long press: record while you hold the keys. Escape cancels."))
	if !a.hotkeys.Installed {
		notice(c, p+"-no-tap", noticeWarn,
			i18n.T("Without Accessibility only plain shortcuts work (⌃⌥⇧⌘ with a key); fn, a single modifier, double taps and long presses need it."),
			"", func() {
				if ui.Button(c.Key(p+"-open-perms"), i18n.T("Open Permissions")).Clicked() {
					a.navigate("/settings/permissions")
				}
			}, nil)
	}
	problems := hotkeyProblems(a.prefs.Features, feature)
	card(c.Key(p + "-keys")).Gap(px(t, sp2)).Children(func() {
		hs := hotkey.HotkeysOf(a.prefs.Features, feature)
		if len(hs) == 0 {
			if feature == hotkey.Transcribe {
				caption(c, i18n.T("No shortcut: start from the menu bar, or add one."))
			} else {
				caption(c, i18n.T("No shortcut: add one to use this feature."))
			}
		}
		for i := range hs {
			if i >= len(u.rows) {
				break
			}
			if i > 0 {
				ui.Box(c).Height(1).Background(tok(t).Line).Shrink(0)
			}
			a.keyRowView(c, feature, i, hs[i], problems[i])
		}
		ui.Row(c).Children(func() {
			if ghostIconButton(c.Key(p+"-key-add"), icons.Add, i18n.T("Add a shortcut")).Clicked() {
				h := newHotkey(a.prefs.Features)
				a.setHotkeys(feature, true, func(hs []settings.Hotkey) []settings.Hotkey { return append(hs, h) })
			}
		})
	})
	switch feature {
	case hotkey.Transcribe:
		a.trPasteRow(c)
	case hotkey.Rewrite:
		caption(c, i18n.T("While the answer card shows an answer, the shortcut records a follow-up that changes it (\"shorter\", \"in English\")."))
	}
}

// keyRowView is the i-th shortcut: its keys, its trigger, a mark when it
// needs Accessibility, Remove; the typed keys when custom; its problems.
func (a *app) keyRowView(c *ui.Context, feature string, i int, h settings.Hotkey, problems []string) {
	t := c.Theme()
	k := tok(t)
	r := &a.fui(feature).rows[i]
	n := i + 1
	ui.Column(c.Key(fmt.Sprintf("%s-key-%d", featurePrefix(feature), i))).Gap(px(t, 6)).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Wrap().Gap(px(t, sp2)).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
				opts, byLabel := keyOptions(h.Keys, false)
				if !slices.Contains(opts, r.sel) {
					r.sel = chordLabel(h.Keys)
				}
				if r.custom {
					r.sel = i18n.T("Custom…")
				}
				sel := ui.Select(c.Key("sel"), &r.sel, opts).Label(i18n.T("Keys of shortcut %d", n)).Width(px(t, 180)).Shrink(1).MinWidth(px(t, 120))
				if sel.Changed() {
					a.chooseKeys(feature, i, r, byLabel)
				}
				tips := []string{
					i18n.T("Press to start, press again to stop."),
					i18n.T("Press twice quickly to start; once more to stop."),
					i18n.T("Record while you hold the keys; release to stop."),
				}
				seg := segmentedSome(c.Key("trig"), &r.trig, []string{i18n.T("Tap"), i18n.T("Double tap"), i18n.T("Long press")}, nil, tips).
					Label(i18n.T("Trigger of shortcut %d", n))
				if seg.Changed() {
					trig := triggers[r.trig]
					a.setHotkeys(feature, false, func(hs []settings.Hotkey) []settings.Hotkey {
						if i < len(hs) {
							hs[i].Trigger = trig
						}
						return hs
					})
				}
			})
			if hotkey.NeedsTap(h) {
				col := k.Text3
				if !a.hotkeys.Installed {
					col = k.Warning
				}
				iconMark(c, icons.Accessibility, col, i18n.T("Works only while Auralink may use Accessibility."))
			}
			if iconButton(c.Key("remove"), icons.TrashBinMinimalistic, i18n.T("Remove shortcut %d", n)).Clicked() {
				a.setHotkeys(feature, true, func(hs []settings.Hotkey) []settings.Hotkey {
					if i < len(hs) {
						hs = slices.Delete(hs, i, i+1)
					}
					return hs
				})
			}
		})
		if r.custom {
			a.customKeys(c, r, i18n.T("Custom keys of shortcut %d", n), func(keys string) {
				a.setHotkeys(feature, false, func(hs []settings.Hotkey) []settings.Hotkey {
					if i < len(hs) {
						hs[i].Keys = keys
					}
					return hs
				})
			})
		}
		problemLines(c, problems)
	})
}

// chooseKeys applies a choice of a feature's i-th shortcut select.
func (a *app) chooseKeys(feature string, i int, r *keyRow, byLabel map[string]string) {
	if r.sel == i18n.T("Custom…") {
		r.custom = true
		if hs := hotkey.HotkeysOf(a.prefs.Features, feature); i < len(hs) {
			r.text = hs[i].Keys
		}
		return
	}
	keys, ok := byLabel[r.sel]
	if !ok {
		return
	}
	r.custom, r.text = false, keys
	a.setHotkeys(feature, false, func(hs []settings.Hotkey) []settings.Hotkey {
		if i < len(hs) {
			hs[i].Keys = keys
		}
		return hs
	})
}

// customKeys is the field for typed keys: they are saved (in canonical
// spelling) on Return or when the field loses the focus, if they parse;
// until then it says why not. Not on every key: on the way to
// "Control+Option+K" the field says "Control", a shortcut of its own.
func (a *app) customKeys(c *ui.Context, r *keyRow, label string, save func(keys string)) {
	t := c.Theme()
	in := ui.TextInput(c.Key("custom"), &r.text).Placeholder("Control+Option+Space").Label(label).Font("monospace").MaxWidth(px(t, 360))
	if in.Changed() {
		r.edited = true
	}
	ch, err := hotkey.Parse(r.text)
	if r.edited && err == nil && (in.Submitted() || !in.Focused()) {
		r.edited = false
		r.text = ch.String()
		save(r.text)
	}
	switch {
	case err != nil:
		warnLine(c, parseErrorText(err))
	case r.edited:
		caption(c, i18n.T("Press Return to use %s.", hotkeyText(r.text)))
	default:
		caption(c, i18n.T("Modifiers first, then one key: Fn, RightCommand, Control+Option+Space, Fn+F5."))
	}
}

// problemLines are a binding's problems, in the warning colour.
func problemLines(c *ui.Context, problems []string) {
	for _, p := range problems {
		warnLine(c, p)
	}
}

// warnLine is a caption in the warning colour, with its icon.
func warnLine(c *ui.Context, text string) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c).Gap(px(t, 4)).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.DangerTriangle.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Warning).Shrink(0).Margin(px(t, 1), 0, 0, 0)
		ui.Text(c, text).FontSize(px(t, fsCaption)).TextColor(k.Warning).Grow(1).MinWidth(0)
	})
}

// trPasteRow is "Paste the last result": its keys only (always a tap).
func (a *app) trPasteRow(c *ui.Context) {
	t := c.Theme()
	r := &a.fui(hotkey.Transcribe).paste
	cur := a.prefs.Features.PasteLast.Keys
	problems := hotkeyProblems(a.prefs.Features, hotkey.PasteLast)[0]
	ui.Column(c.Key("tr-paste")).Gap(px(t, 6)).Children(func() {
		settingRow(c, "tr-paste-row", i18n.T("Paste the last result"), i18n.T("Pastes the latest result again where you type, whichever feature made it."), func() {
			opts, byLabel := keyOptions(cur, true)
			if !slices.Contains(opts, r.sel) {
				r.sel = chordLabel(cur)
			}
			if r.custom {
				r.sel = i18n.T("Custom…")
			}
			if ui.Select(c.Key("tr-paste-sel"), &r.sel, opts).Label(i18n.T("Keys to paste the last result")).Width(px(t, 180)).Changed() {
				if r.sel == i18n.T("Custom…") {
					r.custom, r.text = true, cur
					return
				}
				keys, ok := byLabel[r.sel]
				if !ok {
					return
				}
				r.custom, r.text = false, keys
				a.setPasteLast(keys)
			}
		})
		if r.custom {
			a.customKeys(c, r, i18n.T("Custom keys to paste the last result"), a.setPasteLast)
		}
		problemLines(c, problems)
	})
}

func (a *app) setPasteLast(keys string) {
	a.updateSettings(func(s *settings.Settings) {
		s.Features.PasteLast = settings.Hotkey{Keys: keys, Trigger: settings.TriggerTap}
	})
	u := a.fui(hotkey.Transcribe)
	u.seen = a.featureKeysKey(hotkey.Transcribe)
}

// ---- Services ----

func (a *app) featureServices(c *ui.Context, feature string) {
	t := c.Theme()
	p := featurePrefix(feature)
	u := a.fui(feature)
	f := providers.FeatureOf(a.prefs, feature)
	enhance := feature != hotkey.Transcribe || a.prefs.Features.Transcribe.Enhance
	readyA := providers.Ready(a.prefs, providers.ASR, a.hasKey)
	readyL := providers.Ready(a.prefs, providers.LLM, a.hasKey)
	optsA, autoA := serviceOptions(providers.ASR, readyA)
	optsL, autoL := serviceOptions(providers.LLM, readyL)
	seen := strings.Join([]string{f.ASR, f.LLM, strings.Join(optsA, "\x00"), strings.Join(optsL, "\x00")}, "\x01")
	if u.svcSeen != seen {
		u.svcSeen = seen
		u.asr, u.llm = serviceLabel(providers.ASR, readyA, f.ASR, autoA), serviceLabel(providers.LLM, readyL, f.LLM, autoL)
	}
	sectionTitleTip(c, i18n.T("Services"), i18n.T("Automatic uses the first service that is ready, in the order of the Model services page."))
	opened := false // one "Open Model services" per page
	openProviders := func() {
		if opened {
			return
		}
		opened = true
		if ui.Button(c.Key(p+"-open-providers"), i18n.T("Open Model services")).Clicked() {
			a.navigate("/providers")
		}
	}
	if len(readyA) == 0 {
		switch feature {
		case hotkey.Translate:
			notice(c, p+"-no-asr", noticeInfo, i18n.T("No speech service is ready: only selected text can be translated."), "", openProviders, nil)
		case hotkey.Rewrite:
			notice(c, p+"-no-asr", noticeWarn, i18n.T("No speech service is ready: set one up to rewrite."), "", openProviders, nil)
		default:
			notice(c, p+"-no-asr", noticeWarn, i18n.T("No speech service is ready: set one up to transcribe."), "", openProviders, nil)
		}
	}
	if len(readyL) == 0 {
		switch feature {
		case hotkey.Translate:
			notice(c, p+"-no-llm", noticeWarn, i18n.T("No language model is ready: set one up to translate."), "", openProviders, nil)
		case hotkey.Rewrite:
			notice(c, p+"-no-llm", noticeWarn, i18n.T("No language model is ready: set one up to rewrite."), "", openProviders, nil)
		}
	}
	var asrDesc, asrLabel, llmDesc, llmLabel string
	switch feature {
	case hotkey.Translate:
		asrDesc, asrLabel = i18n.T("Turns what you say into text, when nothing is selected."), i18n.T("Speech service for Translate")
		llmDesc, llmLabel = i18n.T("Translates the text."), i18n.T("Language model for Translate")
	case hotkey.Rewrite:
		asrDesc, asrLabel = i18n.T("Turns your spoken instruction into text."), i18n.T("Speech service for Rewrite")
		llmDesc, llmLabel = i18n.T("Rewrites the selection, or writes what you ask for."), i18n.T("Language model for Rewrite")
	default:
		asrDesc, asrLabel = i18n.T("Turns your recording into text."), i18n.T("Speech service for Transcribe")
		llmDesc, llmLabel = i18n.T("Cleans up the recognised text when \"Clean up with a language model\" is on."), i18n.T("Language model for Transcribe")
	}
	settingRow(c, p+"-asr", i18n.T("Speech recognition"), asrDesc, func() {
		sel := ui.Select(c.Key(p+"-asr-sel"), &u.asr, optsA).Label(asrLabel).Width(px(t, 260)).Shrink(1).MinWidth(px(t, 160)).Disabled(len(readyA) == 0)
		if sel.Changed() {
			id := serviceID(optsA, readyA, u.asr)
			a.updateSettings(func(s *settings.Settings) { setFeatureProvider(s, providers.ASR, feature, id) })
		}
	})
	settingRow(c, p+"-llm", i18n.T("Language model"), llmDesc, func() {
		sel := ui.Select(c.Key(p+"-llm-sel"), &u.llm, optsL).Label(llmLabel).Width(px(t, 260)).Shrink(1).MinWidth(px(t, 160)).
			Disabled(len(readyL) == 0 || !enhance)
		if sel.Changed() {
			id := serviceID(optsL, readyL, u.llm)
			a.updateSettings(func(s *settings.Settings) { setFeatureProvider(s, providers.LLM, feature, id) })
		}
	})
	if feature == hotkey.Transcribe && len(readyL) == 0 && enhance {
		notice(c, "tr-no-llm", noticeInfo, i18n.T("No language model is ready: the text is typed as recognised, without clean-up."), "", openProviders, nil)
	}
}

// serviceOptions are a service select's labels: "Automatic: X", then
// the ready services.
func serviceOptions(kind providers.Kind, ready []settings.Provider) (opts []string, auto string) {
	auto = i18n.T("Automatic")
	if len(ready) > 0 {
		auto = i18n.T("Automatic: %s", providerName(kind, ready[0]))
	}
	opts = []string{auto}
	for _, p := range ready {
		opts = append(opts, providerName(kind, p))
	}
	return opts, auto
}

// serviceLabel is the select's label of the service id ("" automatic).
func serviceLabel(kind providers.Kind, ready []settings.Provider, id, auto string) string {
	for _, p := range ready {
		if p.ID == id {
			return providerName(kind, p)
		}
	}
	return auto
}

// serviceID is the id a select's label stands for, "" for automatic.
func serviceID(opts []string, ready []settings.Provider, label string) string {
	if j := slices.Index(opts, label); j > 0 {
		return ready[j-1].ID
	}
	return ""
}

// ---- Clean-up ----

func (a *app) trEnhance(c *ui.Context) {
	sectionTitle(c, i18n.T("Clean-up"))
	f := &a.prefs.Features.Transcribe
	settingRow(c, "tr-enhance", i18n.T("Clean up with a language model"),
		i18n.T("Sends the recognised text to the language model with the rules below, and types its answer. Off, the text is typed as recognised. If the model fails or is slow, the recognised text is typed."), func() {
			if ui.Switch(c.Key("tr-enhance-switch"), &f.Enhance).Label(i18n.T("Clean up with a language model")).Changed() {
				on := f.Enhance
				a.updateSettings(func(s *settings.Settings) { s.Features.Transcribe.Enhance = on })
			}
		})
	settingRow(c, "tr-cleanup", i18n.T("Basic clean-up"),
		i18n.T("Removes fillers (um, uh, you know) and false starts, keeps what you said after correcting yourself, fixes punctuation, spacing and capitals, turns spoken punctuation (\"comma\", \"new line\") into marks, writes numbers and dates the usual way, and keeps names, terms and code as said."), func() {
			if ui.Switch(c.Key("tr-cleanup-switch"), &f.Cleanup).Label(i18n.T("Basic clean-up")).Disabled(!f.Enhance).Changed() {
				on := f.Cleanup
				a.updateSettings(func(s *settings.Settings) { s.Features.Transcribe.Cleanup = on })
			}
		})
	settingRow(c, "tr-structure", i18n.T("Structure"),
		i18n.T("Makes a list when you clearly say steps or several items, and splits a long text into paragraphs. Off, the text keeps the form you said it in."), func() {
			if ui.Switch(c.Key("tr-structure-switch"), &f.Structure).Label(i18n.T("Structure")).Disabled(!f.Enhance).Changed() {
				on := f.Structure
				a.updateSettings(func(s *settings.Settings) { s.Features.Transcribe.Structure = on })
			}
		})
}

// ---- Translate and Rewrite ----

// translateSettings are Translate's own: the target language, the
// clean-up of speech, the card.
func (a *app) translateSettings(c *ui.Context) {
	t := c.Theme()
	u := a.fui(hotkey.Translate)
	f := &a.prefs.Features.Translate
	sectionTitle(c, i18n.T("Translation"))
	opts := []string{i18n.T("Automatic: %s", targetLanguageLabel(session.TranslateTarget(settings.Settings{General: a.prefs.General}, i18n.Locale(), "")))}
	for _, code := range compose.TargetLanguages {
		opts = append(opts, targetLanguageLabel(code))
	}
	cur := opts[0]
	if j := slices.Index(compose.TargetLanguages, f.Target); j >= 0 {
		cur = opts[j+1]
	} else if f.Target != "" {
		cur = targetLanguageLabel(f.Target)
		opts = append(opts, cur)
	}
	u.lang = cur // the settings say; a choice saves at once
	settingRow(c, "tl-target", i18n.T("Translate into"),
		i18n.T("Automatic is English, or Chinese when your main language is English (Settings → General). The answer card can translate the same text into another language."), func() {
			if ui.Select(c.Key("tl-target-sel"), &u.lang, opts).Label(i18n.T("Target language")).Width(px(t, 260)).Shrink(1).MinWidth(px(t, 160)).Changed() {
				code := ""
				if j := slices.Index(opts, u.lang); j > 0 && j <= len(compose.TargetLanguages) {
					code = compose.TargetLanguages[j-1]
				}
				a.updateSettings(func(s *settings.Settings) { s.Features.Translate.Target = code })
			}
		})
	settingRow(c, "tl-cleanup", i18n.T("Basic clean-up"),
		i18n.T("Cleans up what you say before translating it: fillers, false starts and corrections go, spoken punctuation becomes marks. Selected text is translated as it is."), func() {
			if ui.Switch(c.Key("tl-cleanup-switch"), &f.Cleanup).Label(i18n.T("Basic clean-up")).Changed() {
				on := f.Cleanup
				a.updateSettings(func(s *settings.Settings) { s.Features.Translate.Cleanup = on })
			}
		})
	settingRow(c, "tl-card", i18n.T("Show the result in a card"),
		i18n.T("Shows the translation in the answer card, to read, copy or insert, instead of typing it (or replacing the selection). Selected text that cannot be edited, such as a web page, always goes to the card."), func() {
			if ui.Switch(c.Key("tl-card-switch"), &f.ShowCard).Label(i18n.T("Show the result in a card")).Changed() {
				on := f.ShowCard
				a.updateSettings(func(s *settings.Settings) { s.Features.Translate.ShowCard = on })
			}
		})
}

// rewriteSettings are Rewrite's own: the instruction's clean-up and when
// an answer goes to the card.
func (a *app) rewriteSettings(c *ui.Context) {
	t := c.Theme()
	f := &a.prefs.Features.Rewrite
	sectionTitle(c, i18n.T("Rewriting"))
	settingRow(c, "rw-cleanup", i18n.T("Clean up the instruction"),
		i18n.T("Reads past fillers, false starts and corrections in what you say (\"shorter, no, more formal\"). It never changes the selected text by itself."), func() {
			if ui.Switch(c.Key("rw-cleanup-switch"), &f.Cleanup).Label(i18n.T("Clean up the instruction")).Changed() {
				on := f.Cleanup
				a.updateSettings(func(s *settings.Settings) { s.Features.Rewrite.Cleanup = on })
			}
		})
	settingRow(c, "rw-card", i18n.T("Long answers in the card"),
		i18n.T("With nothing selected, an answer longer than this many characters shows in the answer card instead of being typed; 0 types every answer. A rewritten selection always replaces it, unless it cannot be edited."), func() {
			v := float64(f.CardChars)
			if numberInput(c.Key("rw-card-chars"), &v, 0, 5000, 50, numOpts{Label: i18n.T("Characters"), Width: px(t, 160)}) {
				n := int(v)
				a.updateSettings(func(s *settings.Settings) { s.Features.Rewrite.CardChars = n })
			}
		})
}

// ---- Your rules ----

// variableInfo says what a prompt variable holds.
func variableInfo(name string) string {
	switch name {
	case "USER_MAIN_LANGUAGE":
		return i18n.T("The language you mostly speak (Settings → General).")
	case "USER_OTHER_LANGUAGES":
		return i18n.T("The other languages you mix in.")
	case "TARGET_LANGUAGE":
		return i18n.T("The language to translate into (Translate only).")
	case "APP_NAME":
		return i18n.T("The app in front (with App enhancement).")
	}
	return ""
}

// promptOf is a feature's saved rules.
func promptOf(s settings.Settings, feature string) string {
	return providers.FeatureOf(s, feature).Prompt
}

// promptSync sets the rules from the settings when they changed
// elsewhere and nothing typed waits to be saved.
func (a *app) promptSync(feature string) {
	u := a.fui(feature)
	saved := promptOf(a.prefs, feature)
	if !u.promptDirty && saved != u.promptSeen {
		u.prompt, u.promptSeen = saved, saved
	}
}

// savePrompt saves the rules as typed.
func (a *app) savePrompt(feature string) {
	u := a.fui(feature)
	u.promptDirty = false
	v := u.prompt
	if strings.TrimSpace(v) == "" {
		v = ""
	}
	u.promptSeen = v
	if v == promptOf(a.prefs, feature) {
		return
	}
	a.updateSettings(func(s *settings.Settings) {
		switch feature {
		case hotkey.Translate:
			s.Features.Translate.Prompt = v
		case hotkey.Rewrite:
			s.Features.Rewrite.Prompt = v
		default:
			s.Features.Transcribe.Prompt = v
		}
	})
}

// promptSaveDelay is how long the rules wait after the last key before
// they are saved (they are saved at once when the field loses focus).
const promptSaveDelay = 800 * time.Millisecond

func (a *app) featureRules(c *ui.Context, feature string) {
	t := c.Theme()
	k := tok(t)
	p := featurePrefix(feature)
	u := a.fui(feature)
	a.promptSync(feature)
	sectionTitle(c, i18n.T("Your rules"))
	var label, placeholder string
	switch feature {
	case hotkey.Translate:
		caption(c, i18n.T("These rules are merged with the built-in translation rules; where they disagree, yours win."))
		label = i18n.T("Your rules for Translate")
		placeholder = i18n.T("For example: use British spelling; keep product names in English; write dates as 15 October 2026.")
	case hotkey.Rewrite:
		caption(c, i18n.T("These rules are merged with the built-in writing rules; where they disagree, yours win."))
		label = i18n.T("Your rules for Rewrite")
		placeholder = i18n.T("For example: write plainly, without emoji; sign mails with my first name.")
	default:
		caption(c, i18n.T("These rules are merged with the built-in clean-up rules; where they disagree, yours win."))
		label = i18n.T("Your rules for Transcribe")
		placeholder = i18n.T("For example: write numbers as digits; keep English product names as they are; end every sentence with a full stop.")
	}
	in := ui.TextArea(c.Key(p+"-prompt"), &u.prompt).Label(label).Placeholder(placeholder).MinHeight(px(t, 88))
	if in.Changed() {
		u.promptDirty = true
		u.promptSeq++
		if seq := u.promptSeq; a.win != nil {
			time.AfterFunc(promptSaveDelay, func() {
				a.post(func() {
					if u.promptDirty && u.promptSeq == seq {
						a.savePrompt(feature)
					}
				})
			})
		}
	} else if u.promptDirty && !in.Focused() {
		a.savePrompt(feature) // it lost the focus
	}
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
		caption(c, i18n.T("Variables (click one to copy it):")).Grow(1).MinWidth(0)
		empty := u.prompt == "" && promptOf(a.prefs, feature) == ""
		if ghostIconButton(c.Key(p+"-prompt-reset"), icons.Refresh, i18n.T("Reset to default")).Label(i18n.T("Reset your rules")).Disabled(empty).Clicked() {
			u.prompt = ""
			a.savePrompt(feature)
		}
	})
	ui.Column(c.Key(p + "-vars")).Gap(px(t, 4)).Children(func() {
		for _, name := range compose.VariableNames {
			token := "{{" + name + "}}"
			ui.Row(c.Key("var-" + name)).Wrap().GapX(px(t, sp2)).GapY(px(t, 2)).AlignItems(ui.Center).Children(func() {
				b := ui.ButtonBase(c.Key("copy")).Padding(px(t, 2), px(t, 6)).Radius(px(t, 4)).Background(k.Hover).Label(token).Tooltip(i18n.T("Copy")).Shrink(0)
				if b.Hovered() {
					b.Background(k.Pressed)
				}
				b.Children(func() {
					ui.Text(c, token).FontSize(px(t, fsCaption)).Font("monospace").SingleLine()
				})
				if b.Clicked() {
					c.Services().WriteClipboard(token)
					a.toast(i18n.T("Copied"))
				}
				caption(c, variableInfo(name)).Grow(1).Basis(px(t, 200)).MinWidth(0)
			})
		}
	})
	collapsible(c.Key(p+"-preview"), i18n.T("Show the full prompt"), &u.preview, nil, func() {
		a.previewBody(c, feature)
	})
}

// rewriteSample is the instruction and the selection Rewrite's preview
// shows.
func rewriteSample() (instruction, selection string) {
	return i18n.T("um make it shorter, no, more formal"), i18n.T("hey, the release slips to thursday because the tests are still running, sorry!")
}

// composedPreview is the request a session would send with the settings
// and the rules as typed, for the sample text.
func (a *app) composedPreview(feature string) (compose.Output, error) {
	g := a.prefs.General
	in := compose.Input{
		TaskPrompt: a.fui(feature).prompt, MainLanguage: session.MainLanguage(a.prefs, i18n.Locale()), OtherLanguages: g.OtherLanguages,
	}
	switch feature {
	case hotkey.Translate:
		f := a.prefs.Features.Translate
		in.Task, in.Cleanup, in.Text = compose.Translate, f.Cleanup, trySample()
		in.TargetLanguage = session.TranslateTarget(a.prefs, i18n.Locale(), "")
	case hotkey.Rewrite:
		in.Task, in.Cleanup = compose.Rewrite, a.prefs.Features.Rewrite.Cleanup
		in.Instruction, in.Text = rewriteSample()
	default:
		f := a.prefs.Features.Transcribe
		in.Task, in.Cleanup, in.Structure, in.Text = compose.Transcribe, f.Cleanup, f.Structure, trySample()
	}
	return compose.Build(in)
}

func (a *app) previewBody(c *ui.Context, feature string) {
	t := c.Theme()
	k := tok(t)
	p := featurePrefix(feature)
	switch feature {
	case hotkey.Transcribe:
		if !a.prefs.Features.Transcribe.Enhance {
			caption(c, i18n.T("Clean-up is off: nothing is sent to a language model."))
		}
	case hotkey.Translate:
		caption(c, i18n.T("Spoken text, as below; selected text is translated without the clean-up and keeps its form."))
	case hotkey.Rewrite:
		caption(c, i18n.T("With a selection, as below; with nothing selected the model writes from the instruction and answers with a title and the text."))
	}
	out, err := a.composedPreview(feature)
	if err != nil {
		caption(c, err.Error())
		return
	}
	caption(c, i18n.T("Your dictionary's terms and App enhancement's scene are added when they apply."))
	block := func(key, title, text string) {
		ui.Column(c.Key(key)).Gap(px(t, 4)).Children(func() {
			ui.Text(c, title).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(k.Text2)
			fillCard(c).Padding(px(t, sp3)).Children(func() {
				ui.Text(c, text).FontSize(px(t, fsCaption)).Font("monospace").Selectable()
			})
		})
	}
	block(p+"-preview-system", i18n.T("System prompt"), out.System)
	block(p+"-preview-user", i18n.T("User message (an example)"), out.User)
}

// ---- Try it ----

// trySessionOf reports whether the current session is a try of feature.
func (a *app) trySessionOf(feature string) bool {
	s := a.sess
	return s.ID != "" && s.Try && s.Kind == featureKind(feature)
}

func (a *app) featureTry(c *ui.Context, feature string) {
	t := c.Theme()
	p := featurePrefix(feature)
	u := a.fui(feature)
	sectionTitle(c, i18n.T("Try it"))
	s := a.sess
	try := a.trySessionOf(feature)
	card(c.Key(p + "-try")).Children(func() {
		switch {
		case try && s.State == session.Recording:
			a.tryRecording(c, s)
			return
		case try && s.State.Live():
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(px(t, 16), px(t, 16))
				ui.Text(c, workingText(s)).Grow(1).MinWidth(0)
				if ghostButton(c.Key(p+"-try-cancel"), i18n.T("Cancel")).Clicked() {
					a.cancelSession()
				}
			})
			if d := strings.TrimSpace(s.Draft); d != "" && s.State == session.Composing {
				fillCard(c.Key(p + "-try-draft")).Padding(px(t, sp3)).Children(func() {
					ui.Text(c, d).Selectable()
				})
			}
			return
		}
		if try && s.State.Ended() {
			a.tryEnded(c, s)
		} else {
			switch feature {
			case hotkey.Translate:
				caption(c, i18n.T("Say a sentence or two: the translation shows here and is not typed anywhere."))
			case hotkey.Rewrite:
				caption(c, i18n.T("Say what to write (\"a short note saying I will be late\"): the answer shows here and is not typed anywhere."))
			default:
				caption(c, i18n.T("Say a sentence or two: the result shows here and is not typed anywhere."))
			}
		}
		if n := u.block; n != nil {
			notice(c, p+"-try-block", noticeError, n.text, n.more, a.noticeButton(c, p+"-try-block-action", n.action, feature), func() { u.block = nil })
		}
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
			label := i18n.T("Start")
			if try && s.State.Ended() {
				label = i18n.T("Try again")
			}
			busy := s.State.Live()
			if recordButton(c.Key(p+"-try-start"), label).Label(i18n.T("Start a try")).Disabled(busy).Clicked() {
				u.block = a.startSession(session.StartRequest{Kind: featureKind(feature), Try: true})
			}
			if busy {
				if feature == hotkey.Transcribe {
					caption(c, i18n.T("A transcription is running."))
				} else {
					caption(c, i18n.T("Another session is running."))
				}
			}
		})
	})
}

// noticeButton is a notice's action button, nil for none.
func (a *app) noticeButton(c *ui.Context, key, action, feature string) func() {
	label := noticeActionLabel(action)
	if label == "" {
		return nil
	}
	return func() {
		if ui.Button(c.Key(key), label).Clicked() {
			a.fui(feature).block = nil
			a.noticeAction(action)
		}
	}
}

// tryRecording is the Try area while it records: the levels, the time,
// the live text, Stop and Cancel.
func (a *app) tryRecording(c *ui.Context, s session.Snapshot) {
	t := c.Theme()
	k := tok(t)
	p := featurePrefix(string(s.Kind))
	ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, kindIcon(s.Kind).Bold).Size(px(t, 18), px(t, 18)).TextColor(k.Record).Shrink(0)
		levelBars(c, s.Levels, !s.HeardAt.IsZero())
		ui.Text(c, clock(s.Elapsed(time.Now()))).FontSize(px(t, fsCaption)).Font("monospace").TextColor(k.Text2).Shrink(0)
	})
	live := strings.TrimSpace(s.Partial)
	if live == "" {
		caption(c, i18n.T("Listening…"))
	} else {
		ui.Text(c, live).MaxLines(4).Ellipsis("…")
	}
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		if recordButton(c.Key(p+"-try-stop"), i18n.T("Stop")).Label(i18n.T("Stop recording")).Clicked() {
			a.stopSession()
		}
		if ghostButton(c.Key(p+"-try-cancel"), i18n.T("Cancel")).Clicked() {
			a.cancelSession()
		}
	})
}

// levelBars draws the input levels as session.Bars bars, newest on the
// right, in the recording colour once the microphone is heard.
func levelBars(c *ui.Context, levels []float64, heard bool) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key("tr-wave")).Grow(1).MinWidth(0).Height(px(t, 24)).Gap(px(t, 3)).AlignItems(ui.Center).Label(i18n.T("Input level")).Children(func() {
		pad := session.Bars - len(levels)
		col := k.Record
		if !heard {
			col = k.G[5]
		}
		for i := range session.Bars {
			v := 0.0
			if i >= pad {
				v = levels[i-pad]
			}
			ui.Box(c).Width(px(t, 3)).Height(px(t, float32(3+min(max(v, 0), 1)*21))).Radius(px(t, 1.5)).Background(col)
		}
	})
}

// tryEnded is a finished try: its result (or why there is none), what
// was recognised, the stage times and Copy.
func (a *app) tryEnded(c *ui.Context, s session.Snapshot) {
	t := c.Theme()
	k := tok(t)
	feature := string(s.Kind)
	p := featurePrefix(feature)
	switch s.State {
	case session.Cancelled:
		caption(c, i18n.T("Cancelled."))
		return
	case session.Failed:
		if s.Problem != nil {
			text, more, action := sessionProblemText(s.Problem)
			if text != "" {
				notice(c, p+"-try-failed", noticeError, text, more, a.noticeButton(c, p+"-try-failed-action", action, feature), nil)
				return
			}
		}
		caption(c, i18n.T("Nothing was heard."))
		return
	}
	if pr := s.Problem; pr != nil {
		if text, more, action := sessionProblemText(pr); text != "" {
			notice(c, p+"-try-warn", noticeWarn, text, more, a.noticeButton(c, p+"-try-warn-action", action, feature), nil)
		}
	}
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("Result")).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(k.Text2).Grow(1)
		if ghostIconButton(c.Key(p+"-try-copy"), icons.Copy, i18n.T("Copy")).Label(i18n.T("Copy the result")).Disabled(s.Result == "").Clicked() {
			c.Services().WriteClipboard(s.Result)
			a.toast(i18n.T("Copied"))
		}
	})
	fillCard(c.Key(p + "-try-result")).Padding(px(t, sp3)).Children(func() {
		ui.Text(c, s.Result).Selectable()
	})
	if raw := strings.TrimSpace(s.Raw); raw != "" && raw != strings.TrimSpace(s.Result) {
		title := i18n.T("As recognised")
		if s.Kind == store.KindRewrite {
			title = i18n.T("Your instruction")
		}
		ui.Column(c).Gap(px(t, 4)).Children(func() {
			ui.Text(c, title).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(k.Text2)
			ui.Text(c, raw).FontSize(px(t, fsCaption)).TextColor(k.Text2).Selectable()
		})
	}
	if line := timingsText(s); line != "" {
		caption(c, line)
	}
}

// timingsText is a try's stage times and services: "Recording 3.2 s ·
// Recognition 0.8 s · Clean-up 1.1 s · Doubao, DeepSeek".
func timingsText(s session.Snapshot) string {
	secs := func(d time.Duration) string { return i18n.T("%.1f s", d.Seconds()) }
	var parts []string
	tm := s.Timings
	if tm.Audio > 0 {
		parts = append(parts, i18n.T("Recording %s", secs(tm.Audio)))
	}
	if tm.ASR > 0 {
		parts = append(parts, i18n.T("Recognition %s", secs(tm.ASR)))
	}
	if tm.LLM > 0 {
		switch s.Kind {
		case store.KindTranslate:
			parts = append(parts, i18n.T("Translation %s", secs(tm.LLM)))
		case store.KindRewrite:
			parts = append(parts, i18n.T("Writing %s", secs(tm.LLM)))
		default:
			parts = append(parts, i18n.T("Clean-up %s", secs(tm.LLM)))
		}
	}
	if tm.Total > 0 {
		parts = append(parts, i18n.T("Total %s", secs(tm.Total)))
	}
	var svc []string
	for _, m := range []session.Model{s.ASR, s.LLM} {
		if m.Name != "" {
			svc = append(svc, m.Name)
		}
	}
	if len(svc) > 0 {
		parts = append(parts, strings.Join(svc, ", "))
	}
	return strings.Join(parts, " · ")
}
