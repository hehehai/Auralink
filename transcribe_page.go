package main

// The Transcribe page (/transcribe, plan §3.1): its shortcuts (and the
// shared "paste the last result"), the services it uses, the clean-up
// layers, the user's rules with a preview of the whole prompt, and a Try
// area that runs a session whose result stays on the page.
//
// Every change goes through a.updateSettings, which also sends the
// shortcuts to the helper again. The page keeps only what its controls
// edit (the selects' labels, a custom shortcut being typed, the rules
// before they are saved), set again from the settings when they change
// elsewhere.

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
	"github.com/hehehai/Auralink/internal/lang"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// transcribeState is the Transcribe page's view state.
type transcribeState struct {
	// trRows are the shortcuts' controls, trPaste paste-last's; trSeen is
	// what they were last set from.
	trRows  []keyRow
	trPaste keyRow
	trSeen  string
	// trASR and trLLM are the services' selects; trSvcSeen what they
	// were last set from.
	trASR, trLLM string
	trSvcSeen    string
	// trPrompt is the rules as typed; trPromptSeen the saved value it was
	// last set from; trPromptDirty an edit not saved yet, trPromptSeq
	// which edit (its timer saves only the last one).
	trPrompt      string
	trPromptSeen  string
	trPromptDirty bool
	trPromptSeq   int
	trPreview     bool
	// trBlock says why the Try area's Start did not start.
	trBlock *sessionNotice
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

// transcribePage builds the page.
func (a *app) transcribePage(c *ui.Context) {
	t := c.Theme()
	pageHeader(c, i18n.T("Transcribe"), nil)
	if a.sess.Try && a.sess.State == session.Recording {
		c.After(250 * time.Millisecond) // the Try area's clock
	}
	ui.Scroll(c.Key("tr-scroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(0, px(t, sp6), px(t, sp6)).Gap(px(t, sp3)).MaxWidth(px(t, 880)).Children(func() {
			caption(c, i18n.T("Press a shortcut anywhere, speak, and the text is typed where your cursor is."))
			a.trShortcuts(c)
			a.trServices(c)
			a.trEnhance(c)
			a.trRules(c)
			a.trTryArea(c)
		})
	})
}

// ---- Shortcuts ----

// trSync sets the shortcuts' controls from the settings when they
// changed elsewhere (or the language did: the labels are translated).
func (a *app) trSync() {
	seen := a.trKeysKey()
	if seen == a.trSeen {
		return
	}
	a.trSeen = seen
	hs := a.prefs.Features.Transcribe.Hotkeys
	a.trRows = make([]keyRow, len(hs))
	for i, h := range hs {
		a.trRows[i] = keyRow{sel: chordLabel(h.Keys), text: h.Keys, trig: triggerIndex(h.Trigger)}
	}
	pl := a.prefs.Features.PasteLast.Keys
	a.trPaste = keyRow{sel: chordLabel(pl), text: pl}
	if pl == "" {
		a.trPaste.sel = i18n.T("Off")
	}
}

func (a *app) trKeysKey() string {
	f := a.prefs.Features
	return fmt.Sprint(f.Transcribe.Hotkeys, f.PasteLast, i18n.Locale())
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

// setTranscribeHotkeys changes the shortcuts with fn, on the settings'
// own copy.
func (a *app) setTranscribeHotkeys(resync bool, fn func(hs []settings.Hotkey) []settings.Hotkey) {
	a.updateSettings(func(s *settings.Settings) {
		s.Features.Transcribe.Hotkeys = fn(slices.Clone(s.Features.Transcribe.Hotkeys))
	})
	if resync {
		a.trSeen = ""
	} else {
		a.trSeen = a.trKeysKey() // the controls already show it
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

func (a *app) trShortcuts(c *ui.Context) {
	t := c.Theme()
	a.trSync()
	sectionTitleTip(c, i18n.T("Shortcuts"), i18n.T("Tap: press to start, press again to stop. Double tap: press twice quickly. Long press: record while you hold the keys. Escape cancels."))
	if !a.hotkeys.Installed {
		notice(c, "tr-no-tap", noticeWarn,
			i18n.T("Without Accessibility only plain shortcuts work (⌃⌥⇧⌘ with a key); fn, a single modifier, double taps and long presses need it."),
			"", func() {
				if ui.Button(c.Key("tr-open-perms"), i18n.T("Open Permissions")).Clicked() {
					a.navigate("/settings/permissions")
				}
			}, nil)
	}
	problems := hotkeyProblems(a.prefs.Features, hotkey.Transcribe)
	card(c.Key("tr-keys")).Gap(px(t, sp2)).Children(func() {
		hs := a.prefs.Features.Transcribe.Hotkeys
		if len(hs) == 0 {
			caption(c, i18n.T("No shortcut: start from the menu bar, or add one."))
		}
		for i := range hs {
			if i >= len(a.trRows) {
				break
			}
			if i > 0 {
				ui.Box(c).Height(1).Background(tok(t).Line).Shrink(0)
			}
			a.trKeyRow(c, i, hs[i], problems[i])
		}
		ui.Row(c).Children(func() {
			if ghostIconButton(c.Key("tr-key-add"), icons.Add, i18n.T("Add a shortcut")).Clicked() {
				h := newHotkey(a.prefs.Features)
				a.setTranscribeHotkeys(true, func(hs []settings.Hotkey) []settings.Hotkey { return append(hs, h) })
			}
		})
	})
	a.trPasteRow(c)
}

// trKeyRow is the i-th shortcut: its keys, its trigger, a mark when it
// needs Accessibility, Remove; the typed keys when custom; its problems.
func (a *app) trKeyRow(c *ui.Context, i int, h settings.Hotkey, problems []string) {
	t := c.Theme()
	k := tok(t)
	r := &a.trRows[i]
	n := i + 1
	ui.Column(c.Key(fmt.Sprintf("tr-key-%d", i))).Gap(px(t, 6)).Children(func() {
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
					a.trChooseKeys(i, r, byLabel)
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
					a.setTranscribeHotkeys(false, func(hs []settings.Hotkey) []settings.Hotkey {
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
				a.setTranscribeHotkeys(true, func(hs []settings.Hotkey) []settings.Hotkey {
					if i < len(hs) {
						hs = slices.Delete(hs, i, i+1)
					}
					return hs
				})
			}
		})
		if r.custom {
			a.customKeys(c, r, i18n.T("Custom keys of shortcut %d", n), func(keys string) {
				a.setTranscribeHotkeys(false, func(hs []settings.Hotkey) []settings.Hotkey {
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

// trChooseKeys applies a choice of the i-th shortcut's select.
func (a *app) trChooseKeys(i int, r *keyRow, byLabel map[string]string) {
	if r.sel == i18n.T("Custom…") {
		r.custom = true
		if i < len(a.prefs.Features.Transcribe.Hotkeys) {
			r.text = a.prefs.Features.Transcribe.Hotkeys[i].Keys
		}
		return
	}
	keys, ok := byLabel[r.sel]
	if !ok {
		return
	}
	r.custom, r.text = false, keys
	a.setTranscribeHotkeys(false, func(hs []settings.Hotkey) []settings.Hotkey {
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
	r := &a.trPaste
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
	a.trSeen = a.trKeysKey()
}

// ---- Services ----

func (a *app) trServices(c *ui.Context) {
	t := c.Theme()
	f := a.prefs.Features.Transcribe
	readyA := providers.Ready(a.prefs, providers.ASR, a.hasKey)
	readyL := providers.Ready(a.prefs, providers.LLM, a.hasKey)
	optsA, autoA := serviceOptions(providers.ASR, readyA)
	optsL, autoL := serviceOptions(providers.LLM, readyL)
	seen := strings.Join([]string{f.ASR, f.LLM, strings.Join(optsA, "\x00"), strings.Join(optsL, "\x00")}, "\x01")
	if a.trSvcSeen != seen {
		a.trSvcSeen = seen
		a.trASR, a.trLLM = serviceLabel(providers.ASR, readyA, f.ASR, autoA), serviceLabel(providers.LLM, readyL, f.LLM, autoL)
	}
	sectionTitleTip(c, i18n.T("Services"), i18n.T("Automatic uses the first service that is ready, in the order of the Model services page."))
	openProviders := func() {
		if ui.Button(c.Key("tr-open-providers"), i18n.T("Open Model services")).Clicked() {
			a.navigate("/providers")
		}
	}
	if len(readyA) == 0 {
		notice(c, "tr-no-asr", noticeWarn, i18n.T("No speech service is ready: set one up to transcribe."), "", openProviders, nil)
	}
	settingRow(c, "tr-asr", i18n.T("Speech recognition"), i18n.T("Turns your recording into text."), func() {
		sel := ui.Select(c.Key("tr-asr-sel"), &a.trASR, optsA).Label(i18n.T("Speech service for Transcribe")).Width(px(t, 260)).Shrink(1).MinWidth(px(t, 160)).Disabled(len(readyA) == 0)
		if sel.Changed() {
			id := serviceID(optsA, readyA, a.trASR)
			a.updateSettings(func(s *settings.Settings) { setFeatureProvider(s, providers.ASR, providers.Transcribe, id) })
		}
	})
	settingRow(c, "tr-llm", i18n.T("Language model"), i18n.T("Cleans up the recognised text when \"Clean up with a language model\" is on."), func() {
		sel := ui.Select(c.Key("tr-llm-sel"), &a.trLLM, optsL).Label(i18n.T("Language model for Transcribe")).Width(px(t, 260)).Shrink(1).MinWidth(px(t, 160)).
			Disabled(len(readyL) == 0 || !f.Enhance)
		if sel.Changed() {
			id := serviceID(optsL, readyL, a.trLLM)
			a.updateSettings(func(s *settings.Settings) { setFeatureProvider(s, providers.LLM, providers.Transcribe, id) })
		}
	})
	if len(readyL) == 0 && f.Enhance {
		notice(c, "tr-no-llm", noticeInfo, i18n.T("No language model is ready: the text is typed as recognised, without clean-up."), "",
			func() {
				if len(readyA) > 0 { // one "Open Model services" per page
					openProviders()
				}
			}, nil)
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

// trPromptSync sets the rules from the settings when they changed
// elsewhere and nothing typed waits to be saved.
func (a *app) trPromptSync() {
	saved := a.prefs.Features.Transcribe.Prompt
	if !a.trPromptDirty && saved != a.trPromptSeen {
		a.trPrompt, a.trPromptSeen = saved, saved
	}
}

// saveTrPrompt saves the rules as typed.
func (a *app) saveTrPrompt() {
	a.trPromptDirty = false
	v := a.trPrompt
	if strings.TrimSpace(v) == "" {
		v = ""
	}
	a.trPromptSeen = v
	if v == a.prefs.Features.Transcribe.Prompt {
		return
	}
	a.updateSettings(func(s *settings.Settings) { s.Features.Transcribe.Prompt = v })
}

// promptSaveDelay is how long the rules wait after the last key before
// they are saved (they are saved at once when the field loses focus).
const promptSaveDelay = 800 * time.Millisecond

func (a *app) trRules(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	a.trPromptSync()
	sectionTitle(c, i18n.T("Your rules"))
	caption(c, i18n.T("These rules are merged with the built-in clean-up rules; where they disagree, yours win."))
	in := ui.TextArea(c.Key("tr-prompt"), &a.trPrompt).Label(i18n.T("Your rules for Transcribe")).
		Placeholder(i18n.T("For example: write numbers as digits; keep English product names as they are; end every sentence with a full stop.")).
		MinHeight(px(t, 88))
	if in.Changed() {
		a.trPromptDirty = true
		a.trPromptSeq++
		if seq := a.trPromptSeq; a.win != nil {
			time.AfterFunc(promptSaveDelay, func() {
				a.post(func() {
					if a.trPromptDirty && a.trPromptSeq == seq {
						a.saveTrPrompt()
					}
				})
			})
		}
	} else if a.trPromptDirty && !in.Focused() {
		a.saveTrPrompt() // it lost the focus
	}
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
		caption(c, i18n.T("Variables (click one to copy it):")).Grow(1).MinWidth(0)
		empty := a.trPrompt == "" && a.prefs.Features.Transcribe.Prompt == ""
		if ghostIconButton(c.Key("tr-prompt-reset"), icons.Refresh, i18n.T("Reset to default")).Label(i18n.T("Reset your rules")).Disabled(empty).Clicked() {
			a.trPrompt = ""
			a.saveTrPrompt()
		}
	})
	ui.Column(c.Key("tr-vars")).Gap(px(t, 4)).Children(func() {
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
	collapsible(c.Key("tr-preview"), i18n.T("Show the full prompt"), &a.trPreview, nil, func() {
		a.trPreviewBody(c)
	})
}

// trComposed is the request a session would send with the settings and
// the rules as typed, for the sample text.
func (a *app) trComposed() (compose.Output, error) {
	f := a.prefs.Features.Transcribe
	g := a.prefs.General
	main := g.MainLanguage
	if main == "" {
		main = lang.Canonical(i18n.Locale())
	}
	return compose.Build(compose.Input{
		Task: compose.Transcribe, Cleanup: f.Cleanup, Structure: f.Structure, TaskPrompt: a.trPrompt,
		MainLanguage: main, OtherLanguages: g.OtherLanguages, Text: trySample(),
	})
}

func (a *app) trPreviewBody(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	if !a.prefs.Features.Transcribe.Enhance {
		caption(c, i18n.T("Clean-up is off: nothing is sent to a language model."))
	}
	out, err := a.trComposed()
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
	block("tr-preview-system", i18n.T("System prompt"), out.System)
	block("tr-preview-user", i18n.T("User message (an example)"), out.User)
}

// ---- Try it ----

// trySession reports whether the current session is a Transcribe try.
func (a *app) trySession() bool {
	s := a.sess
	return s.ID != "" && s.Try && s.Kind == store.KindTranscribe
}

func (a *app) trTryArea(c *ui.Context) {
	t := c.Theme()
	sectionTitle(c, i18n.T("Try it"))
	s := a.sess
	try := a.trySession()
	card(c.Key("tr-try")).Children(func() {
		switch {
		case try && s.State == session.Recording:
			a.tryRecording(c, s)
			return
		case try && s.State.Live():
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(px(t, 16), px(t, 16))
				ui.Text(c, workingText(s.State)).Grow(1).MinWidth(0)
				if ghostButton(c.Key("tr-try-cancel"), i18n.T("Cancel")).Clicked() {
					a.cancelSession()
				}
			})
			return
		}
		if try && s.State.Ended() {
			a.tryEnded(c, s)
		} else {
			caption(c, i18n.T("Say a sentence or two: the result shows here and is not typed anywhere."))
		}
		if n := a.trBlock; n != nil {
			notice(c, "tr-try-block", noticeError, n.text, n.more, a.noticeButton(c, "tr-try-block-action", n.action), func() { a.trBlock = nil })
		}
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
			label := i18n.T("Start")
			if try && s.State.Ended() {
				label = i18n.T("Try again")
			}
			busy := s.State.Live()
			if recordButton(c.Key("tr-try-start"), label).Label(i18n.T("Start a try")).Disabled(busy).Clicked() {
				a.trBlock = a.startTranscribe(proto.App{}, "", true)
			}
			if busy {
				caption(c, i18n.T("A transcription is running."))
			}
		})
	})
}

// noticeButton is a notice's action button, nil for none.
func (a *app) noticeButton(c *ui.Context, key, action string) func() {
	label := noticeActionLabel(action)
	if label == "" {
		return nil
	}
	return func() {
		if ui.Button(c.Key(key), label).Clicked() {
			a.trBlock = nil
			a.noticeAction(action)
		}
	}
}

// tryRecording is the Try area while it records: the levels, the time,
// the live text, Stop and Cancel.
func (a *app) tryRecording(c *ui.Context, s session.Snapshot) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, transcribeIcon.Bold).Size(px(t, 18), px(t, 18)).TextColor(k.Record).Shrink(0)
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
		if recordButton(c.Key("tr-try-stop"), i18n.T("Stop")).Label(i18n.T("Stop recording")).Clicked() {
			a.stopSession()
		}
		if ghostButton(c.Key("tr-try-cancel"), i18n.T("Cancel")).Clicked() {
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
	switch s.State {
	case session.Cancelled:
		caption(c, i18n.T("Cancelled."))
		return
	case session.Failed:
		if s.Problem != nil {
			text, more, action := sessionProblemText(s.Problem)
			if text != "" {
				notice(c, "tr-try-failed", noticeError, text, more, a.noticeButton(c, "tr-try-failed-action", action), nil)
				return
			}
		}
		caption(c, i18n.T("Nothing was heard."))
		return
	}
	if p := s.Problem; p != nil {
		if text, more, action := sessionProblemText(p); text != "" {
			notice(c, "tr-try-warn", noticeWarn, text, more, a.noticeButton(c, "tr-try-warn-action", action), nil)
		}
	}
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("Result")).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(k.Text2).Grow(1)
		if ghostIconButton(c.Key("tr-try-copy"), icons.Copy, i18n.T("Copy")).Label(i18n.T("Copy the result")).Disabled(s.Result == "").Clicked() {
			c.Services().WriteClipboard(s.Result)
			a.toast(i18n.T("Copied"))
		}
	})
	fillCard(c.Key("tr-try-result")).Padding(px(t, sp3)).Children(func() {
		ui.Text(c, s.Result).Selectable()
	})
	if raw := strings.TrimSpace(s.Raw); raw != "" && raw != strings.TrimSpace(s.Result) {
		ui.Column(c).Gap(px(t, 4)).Children(func() {
			ui.Text(c, i18n.T("As recognised")).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(k.Text2)
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
		parts = append(parts, i18n.T("Clean-up %s", secs(tm.LLM)))
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
