package main

import (
	"context"
	"encoding/json"
	"runtime"
	"slices"
	"testing"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/helper/proto"
)

// TestFloatingWindowOptions: floating windows never activate Auralink
// (mygo patch 0003), and start hidden above other windows.
func TestFloatingWindowOptions(t *testing.T) {
	o := floatingWindowOptions("pill", 220, 48, nil)
	if !o.NonActivating || !o.AlwaysOnTop || !o.Hidden || !o.SkipTaskbar || !o.Frameless || o.Width != 220 || o.Height != 48 {
		t.Errorf("options %+v", o)
	}
	// Off macOS nothing can be activated.
	if runtime.GOOS != "darwin" {
		if ok, _ := activateTarget(1); ok {
			t.Error("activated off macOS")
		}
	}
}

// TestTryMode: AURALINK_TRY binds the try shortcuts once the helper is
// ready; fn shows the test panel for the app in front, whose button
// pastes through the helper into that app.
func TestTryMode(t *testing.T) {
	a, fs, _ := newTestApp(t)
	a.try.on = true
	var pasted []proto.PasteParams
	fs.handlers[proto.MethodHotkeyConfigure] = func(raw json.RawMessage) (any, error) {
		var p proto.HotkeyConfigureParams
		json.Unmarshal(raw, &p)
		return proto.HotkeyStatus{Installed: true, Accessibility: true, Bindings: len(p.Bindings)}, nil
	}
	fs.handlers[proto.MethodTextPaste] = func(raw json.RawMessage) (any, error) {
		var p proto.PasteParams
		json.Unmarshal(raw, &p)
		pasted = append(pasted, p)
		if p.TargetPID != 812 {
			return proto.PasteResult{Front: &proto.App{PID: 812, Name: "Slack"}}, nil
		}
		return proto.PasteResult{Posted: true, Restored: true}, nil
	}
	fs.Stop()
	fs.Start(context.Background()) // ready again: binds the try shortcuts
	if !slices.Contains(fs.methods(), proto.MethodHotkeyConfigure) || a.hotkeys.Bindings != len(tryBindings) {
		t.Fatalf("not bound: %v %+v", fs.methods(), a.hotkeys)
	}
	note := func(e proto.HotkeyEventParams) {
		m, _ := proto.NewNotification(proto.MethodHotkeyEvent, e)
		a.helperNotified(m)
	}
	note(proto.HotkeyEventParams{Binding: "translate/0", Phase: proto.PhaseTrigger, Behavior: proto.BehaviorTap, T: 1})
	if a.try.shown {
		t.Error("shown for translate")
	}
	note(proto.HotkeyEventParams{Binding: "transcribe/0", Phase: proto.PhaseTrigger, Behavior: proto.BehaviorTap, T: 2,
		Front: &proto.App{PID: 812, BundleID: "com.tinyspeck.slackmacgap", Name: "Slack"}})
	if !a.try.shown || a.try.target.PID != 812 {
		t.Fatalf("panel %+v", a.try)
	}
	a.tryPaste()
	if len(pasted) != 1 || pasted[0].TargetPID != 812 || a.try.result != "Pasted into Slack" {
		t.Errorf("paste %+v → %q", pasted, a.try.result)
	}
	a.try.target.PID = 1
	a.tryPaste()
	if a.try.result != "Not pasted: Slack is in front" {
		t.Errorf("elsewhere: %q", a.try.result)
	}
	if !slices.ContainsFunc(a.logs.Tail(50, helper.LogSource), func(e core.LogEntry) bool {
		return e.Message == "hotkey transcribe/0 trigger in com.tinyspeck.slackmacgap" && e.Level == core.SeverityInfo
	}) {
		t.Error("the event is not in the log")
	}
	a.tryClose()
	if a.try.shown {
		t.Error("still shown")
	}
}
