package main

// AURALINK_TRY=1: a development mode to check the helper on a Mac by hand
// before sessions exist (M3): the helper watches a fixed set of shortcuts
// and the logs panel shows what fired (and in which app); fn shows a
// non-activating test panel (mygo patch 0003) whose button pastes a test
// line into the app that was in front through the helper (textio.paste).
// The project's handoff of the helper thread lists the steps.
//
//	open --env AURALINK_TRY=1 build/darwin-arm64/Auralink.app

import (
	"fmt"
	"os"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
)

// tryState is the try mode's: the test panel, the app it pastes into and
// what the last paste did.
type tryState struct {
	on     bool
	win    *mygo.Window
	shown  bool
	target proto.App
	result string
}

func tryMode() bool { return os.Getenv("AURALINK_TRY") != "" }

func keyCode(k int) *int { return &k }

// tryBindings are the default shortcuts (settings.Defaults) and right ⌘
// in its three behaviours.
var tryBindings = []proto.HotkeyBinding{
	{ID: "transcribe/0", Modifiers: map[string]string{proto.ModFn: proto.SideAny}, Behavior: proto.BehaviorTap},
	{ID: "translate/0", Modifiers: map[string]string{proto.ModFn: proto.SideAny, proto.ModShift: proto.SideAny}, Behavior: proto.BehaviorTap},
	{ID: "rewrite/0", Modifiers: map[string]string{proto.ModFn: proto.SideAny, proto.ModControl: proto.SideAny}, Behavior: proto.BehaviorTap},
	{ID: "paste-last", Modifiers: map[string]string{proto.ModControl: proto.SideAny, proto.ModCommand: proto.SideAny}, Key: keyCode(9), Behavior: proto.BehaviorTap},
	{ID: "try/right-command-tap", Modifiers: map[string]string{proto.ModCommand: proto.SideRight}, Behavior: proto.BehaviorTap},
	{ID: "try/right-command-double", Modifiers: map[string]string{proto.ModCommand: proto.SideRight}, Behavior: proto.BehaviorDoubleTap},
	{ID: "try/right-command-long", Modifiers: map[string]string{proto.ModCommand: proto.SideRight}, Behavior: proto.BehaviorLongPress},
}

// tryLog writes a line of the try mode to the logs (source helper).
func (a *app) tryLog(msg string) {
	a.logs.Append(core.LogEntry{Time: time.Now(), Level: core.SeverityInfo, Source: helper.LogSource, Message: "try: " + msg})
}

// tryConfigure binds the try shortcuts (once the helper is ready).
func (a *app) tryConfigure() {
	helperCall(a, proto.MethodHotkeyConfigure, proto.HotkeyConfigureParams{Bindings: tryBindings}, 5*time.Second,
		func(st proto.HotkeyStatus, err error) {
			if err != nil {
				a.tryLog("hotkey.configure: " + err.Error())
				return
			}
			a.hotkeys = st
			a.tryLog(fmt.Sprintf("hotkey.configure: installed %v, accessibility %v, %d bindings %s", st.Installed, st.Accessibility, st.Bindings, st.Error))
		})
}

// tryHotkey shows the test panel on fn.
func (a *app) tryHotkey(e proto.HotkeyEventParams) {
	if e.Binding != "transcribe/0" || e.Phase != proto.PhaseTrigger || e.Front == nil {
		return
	}
	a.try.target, a.try.result = *e.Front, ""
	if a.win == nil { // tests
		a.try.shown = true
		return
	}
	const w, h = 320, 56
	if a.try.win == nil {
		a.try.win = mygo.NewWindow(floatingWindowOptions("Auralink try", w, h, ui.View(a.tryView)))
	}
	d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	wa := d.WorkArea
	showFloating(a.try.win, wa.X+(wa.Width-w)/2, wa.Y+wa.Height-h-120)
	a.try.shown = true
	a.try.win.Update(func() {})
}

// tryPaste pastes a test line into the app that was in front.
func (a *app) tryPaste() {
	target := a.try.target
	helperCall(a, proto.MethodTextPaste, proto.PasteParams{Text: "Auralink ✓ ", TargetPID: target.PID}, 5*time.Second,
		func(r proto.PasteResult, err error) {
			switch {
			case err != nil:
				a.try.result = err.Error()
			case r.Posted:
				a.try.result = i18n.T("Pasted into %s", target.Name)
			case r.Front != nil:
				a.try.result = i18n.T("Not pasted: %s is in front", r.Front.Name)
			}
			a.tryLog(fmt.Sprintf("paste into %s: posted %v, restored %v, clipboard taken %v, %.0f ms, error %v", target.BundleID, r.Posted, r.Restored, r.ClipboardTaken, r.MS, err))
			if a.try.win != nil {
				a.try.win.Update(func() {})
			}
		})
}

func (a *app) tryClose() {
	a.try.shown = false
	if a.try.win != nil {
		a.try.win.Hide()
	}
}

// tryView is the test panel: dark, one button that never takes the
// keyboard (the panel cannot); hovering it while another app is active
// shows the hover (NSTrackingActiveAlways).
func (a *app) tryView(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Grow(1).Padding(px(t, sp2), px(t, sp3)).Gap(px(t, sp2)).AlignItems(ui.Center).Radius(px(t, radiusCard)).
		Background(tok(t).Sidebar).Border(1, tok(t).Line).Children(func() {
		text := a.try.result
		if text == "" {
			text = a.try.target.Name
		}
		ui.Text(c, text).FontSize(px(t, fsCaption)).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		ui.Spacer(c)
		if primaryButton(c.Key("try-paste"), i18n.T("Paste a test line")).Clicked() {
			a.tryPaste()
		}
		if iconButton(c.Key("try-close"), icons.Close, i18n.T("Close")).Clicked() {
			a.tryClose()
		}
	})
}
