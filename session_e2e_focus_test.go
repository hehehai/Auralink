package main

// Whole sessions whose user moves to another app before the end (the
// fakes are in session_e2e_test.go): the result goes where the focus is
// then (internal/deliver).

import (
	"encoding/json"
	"testing"

	"github.com/hehehai/Auralink/internal/helper/helpertest"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
	"github.com/hehehai/Auralink/internal/uitext"
)

// activatedLine is a script's app.activated: the user brings bundle's app
// to the front (the fake helper's front follows).
func activatedLine(afterMS int, bundle string) string {
	b, _ := json.Marshal(helpertest.Script{AfterMS: afterMS, Method: proto.MethodAppActivated, Params: mustJSON(helpertest.Apps[bundle])})
	return string(b)
}

// TestSessionFollowsFocus: Transcribe starts in Slack; while it records
// the user moves to TextEdit. The result is pasted into TextEdit (not
// Slack, which is not in front), the history names both apps, and ⌃⌘V
// pastes into TextEdit too.
func TestSessionFollowsFocus(t *testing.T) {
	chat := &fakeChat{text: "Hello, Auralink."}
	set, keys, _ := fileSetup(t, "hello aura link", chat)
	ev := script(t,
		hotkeyLine(0, "transcribe/0", proto.PhaseTrigger),
		activatedLine(150, "com.apple.TextEdit"),
		hotkeyLine(250, "transcribe/0", proto.PhaseTrigger),
		hotkeyLine(1500, "paste-last", proto.PhaseTrigger),
	)
	a := newSessionApp(t, ev, set, keys)
	a.pump("the end", func() bool { return a.sess.State.Ended() })
	end := a.sess
	if end.State != session.Done || !end.Inserted || end.Target.Name != "Slack" || end.Into.Name != "TextEdit" {
		t.Fatalf("end %+v", end)
	}
	a.pump("the second paste", func() bool { return len(a.pasted()) == 2 })
	if p := a.pasted(); p[0].Text != "Hello, Auralink." || p[0].TargetPID != 530 || p[1].TargetPID != 530 {
		t.Errorf("pasted %+v", p)
	}
	es := a.history()
	if len(es) != 1 || es[0].AppName != "Slack" || es[0].Delivery != "insert" || es[0].InsertedBundle != "com.apple.TextEdit" || es[0].InsertedName != "TextEdit" {
		t.Fatalf("history %+v", es)
	}
	if got := uitext.DeliveryLabel(es[0]); got != "Inserted into TextEdit" {
		t.Errorf("the details say %q", got)
	}
}

// TestSessionFocusNowhere: the user moved to Finder, where nothing takes
// text: the result waits in the card, which says why without naming
// Slack. Once the user is in TextEdit, the card's Insert pastes there
// and the history records it.
func TestSessionFocusNowhere(t *testing.T) {
	chat := &fakeChat{text: "Hello, Auralink."}
	set, keys, _ := fileSetup(t, "hello aura link", chat)
	ev := script(t,
		hotkeyLine(0, "transcribe/0", proto.PhaseTrigger),
		activatedLine(150, "com.apple.finder"),
		hotkeyLine(250, "transcribe/0", proto.PhaseTrigger),
		activatedLine(2000, "com.apple.TextEdit"), // well after the end
	)
	a := newSessionApp(t, "readonly=com.apple.finder,"+ev, set, keys)
	a.pump("the end", func() bool { return a.sess.State.Ended() })
	end := a.sess
	if end.State != session.Done || end.Inserted || !end.Card || !a.answer.open || len(a.pasted()) != 0 {
		t.Fatalf("end %+v, card %v, pasted %+v", end, a.answer.open, a.pasted())
	}
	if a.answer.why != "No place to type was found: click where it should go and insert, or copy it." {
		t.Errorf("why %q", a.answer.why)
	}
	// The user clicks into TextEdit, then Insert.
	a.pump("TextEdit in front", func() bool { return a.front.PID == 530 })
	a.insertAnswer()
	a.pump("the insert", func() bool { return !a.answer.open })
	if p := a.pasted(); len(p) != 1 || p[0].TargetPID != 530 {
		t.Fatalf("pasted %+v", p)
	}
	a.pump("the history", func() bool {
		es := a.history()
		return len(es) == 1 && es[0].Delivery == "insert" && es[0].InsertedName == "TextEdit" && es[0].AppName == "Slack"
	})
}

// TestRewriteCardFollowsFocus: a Rewrite answer waits in the card; the
// user moved to TextEdit meanwhile, and Insert pastes there.
func TestRewriteCardFollowsFocus(t *testing.T) {
	chat := &fakeChat{text: `{"title":"Late","content":"Sorry, I am running late."}`}
	set, keys, _ := fileSetup(t, "write that I am late", chat)
	ev := script(t,
		hotkeyLine(0, "rewrite/0", proto.PhaseTrigger),
		activatedLine(150, "com.apple.TextEdit"),
		hotkeyLine(300, "rewrite/0", proto.PhaseTrigger),
	)
	a := newSessionApp(t, ev, func(s *settings.Settings) { set(s) }, keys)
	a.pump("the card", func() bool { return a.sess.State.Ended() && a.answer.open })
	if a.sess.Kind != store.KindRewrite || a.sess.Result != "Sorry, I am running late." || len(a.pasted()) != 0 {
		t.Fatalf("end %+v", a.sess)
	}
	a.insertAnswer()
	a.pump("the insert", func() bool { return !a.answer.open })
	if p := a.pasted(); len(p) != 1 || p[0].TargetPID != 530 || p[0].Text != "Sorry, I am running late." {
		t.Fatalf("pasted %+v", p)
	}
	a.pump("the history", func() bool {
		es := a.history()
		return len(es) == 1 && es[0].Delivery == "insert" && es[0].InsertedBundle == "com.apple.TextEdit"
	})
}

// TestPasteLastFollowsFocus: ⌃⌘V pastes the last result where the focus
// is; where nothing takes text it says so, without naming an app.
func TestPasteLastFollowsFocus(t *testing.T) {
	a, fs, _ := newTestApp(t)
	var pasted []proto.PasteParams
	focus := proto.FocusResult{PID: 530, Role: "AXTextArea", Editable: true}
	fs.handlers[proto.MethodTextFocus] = func(json.RawMessage) (any, error) { return focus, nil }
	fs.handlers[proto.MethodAppFrontmost] = func(json.RawMessage) (any, error) {
		for _, app := range helpertest.Apps {
			if app.PID == focus.PID {
				return app, nil
			}
		}
		return proto.App{PID: focus.PID}, nil
	}
	fs.handlers[proto.MethodTextPaste] = func(raw json.RawMessage) (any, error) {
		var p proto.PasteParams
		json.Unmarshal(raw, &p)
		pasted = append(pasted, p)
		return proto.PasteResult{Posted: true}, nil
	}
	a.mgr.SetLast("Hi.")
	slack := helpertest.Apps["com.tinyspeck.slackmacgap"]
	a.pasteLast(slack)
	if len(pasted) != 1 || pasted[0].TargetPID != 530 || pasted[0].Text != "Hi." || a.notice != nil {
		t.Errorf("TextEdit's field: pasted %+v, notice %+v", pasted, a.notice)
	}
	for _, tc := range []struct {
		focus proto.FocusResult
		say   string
	}{
		{proto.FocusResult{PID: 640, Role: "AXOutline"}, "No place to type was found: click where the text should go first."},
		{proto.FocusResult{PID: 530, Role: "AXTextField", Subrole: "AXSecureTextField", Editable: true}, "The cursor is in a password field: Auralink does not type there."},
	} {
		focus = tc.focus
		a.pasteLast(slack)
		if len(pasted) != 1 || a.notice == nil || a.notice.text != tc.say {
			t.Errorf("%+v: pasted %+v, notice %+v", tc.focus, pasted, a.notice)
		}
	}
}
