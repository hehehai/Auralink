package session

import (
	"testing"

	"github.com/hehehai/Auralink/internal/appctx"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

var (
	textEdit = proto.App{PID: 530, BundleID: "com.apple.TextEdit", Name: "TextEdit"}
	finder   = proto.App{PID: 640, BundleID: "com.apple.finder", Name: "Finder"}
)

// TestDeliverFollowsFocus: a Transcribe session started in Slack, whose
// user moved on while it recorded, delivers where the focus is at the
// end: another app's field takes it (without Slack's group's auto key;
// the scene stays Slack's), Finder's list, a password field and an
// element Accessibility cannot read send it to the card, Auralink's
// own window gives the front back to Slack. The history keeps both apps.
func TestDeliverFollowsFocus(t *testing.T) {
	field := proto.FocusResult{PID: textEdit.PID, App: &textEdit, Role: "AXTextArea", Editable: true}
	for _, tc := range []struct {
		name     string
		focus    proto.FocusResult
		refuse   *proto.App
		into     proto.App // pasted into (zero: not pasted)
		reason   deliver.Reason
		activate bool
	}{
		{name: "still in Slack", focus: proto.FocusResult{PID: slack.PID, Role: "AXTextArea", Editable: true}, into: slack},
		{name: "TextEdit's field", focus: field, into: textEdit},
		{name: "a web editor in another app (AXEditableAncestor)", focus: proto.FocusResult{PID: textEdit.PID, App: &textEdit, Role: "AXGroup", Editable: true}, into: textEdit},
		{name: "a web page being read in another app", focus: proto.FocusResult{PID: textEdit.PID, App: &textEdit, Role: "AXWebArea"}, reason: deliver.ReasonNotEditable},
		{name: "Finder's list", focus: proto.FocusResult{PID: finder.PID, App: &finder, Role: "AXOutline"}, reason: deliver.ReasonNotEditable},
		{name: "nothing focused in Finder", focus: proto.FocusResult{PID: finder.PID, App: &finder, Error: "no focused element"}, reason: deliver.ReasonNotEditable},
		{name: "a password field", focus: proto.FocusResult{PID: textEdit.PID, App: &textEdit, Role: "AXTextField", Subrole: "AXSecureTextField", Editable: true}, reason: deliver.ReasonSecure},
		{name: "moved again before the paste", focus: field, refuse: &finder, reason: deliver.ReasonNotFront},
		{name: "Auralink's window", focus: proto.FocusResult{PID: 77, Role: "AXWindow"}, into: slack, activate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			activated := 0
			var h *harness
			h = newHarness(t, &fakeStreamer{final: "hello aura link"}, nil, func(c *Config, s *settings.Settings) {
				c.Scene = &SceneConfig{Rules: func() (appctx.Rules, bool) { return sceneRules(), true }}
				c.Self = 77
				c.Activate = func(pid int) bool {
					activated++
					if pid == slack.PID {
						h.text.setFocus(proto.FocusResult{PID: slack.PID, Role: "AXTextArea", Editable: true})
					}
					return true
				}
			})
			h.text.refuse = tc.refuse
			id := h.start()
			h.rec.waitFor(t, "recording", func(s Snapshot) bool { return s.State == Recording })
			h.text.setFocus(tc.focus) // the user moves on while it records
			h.speak(id, 3)
			end := h.rec.wait(t)

			h.text.mu.Lock()
			pasted, presses, targets := h.text.pasted, h.text.presses, h.text.targets
			h.text.mu.Unlock()
			if (activated > 0) != tc.activate {
				t.Errorf("activated %d times", activated)
			}
			if end.Scene == nil || end.Scene.GroupID() != "g-chat" || end.Target != slack {
				t.Errorf("the scene is Slack's: %+v %+v", end.Scene, end.Target)
			}
			e := h.hist.all()
			if len(e) != 1 || e[0].AppBundle != slack.BundleID || e[0].RuleID != "g-chat" {
				t.Fatalf("entry %+v", e)
			}
			if tc.into.PID == 0 {
				if len(pasted) != 0 || end.Inserted || !end.Card || end.Delivery != tc.reason || end.InsertedInto.PID != 0 {
					t.Errorf("card: pasted %q, %+v", pasted, end)
				}
				if e[0].Delivery != "card" || e[0].InsertedBundle != "" {
					t.Errorf("entry %+v", e[0])
				}
				return
			}
			if len(pasted) != 1 || targets[0] != tc.into.PID || !end.Inserted || end.Card || end.InsertedInto != tc.into {
				t.Fatalf("pasted %q into %v, %+v", pasted, targets, end)
			}
			if tc.into.PID != slack.PID {
				// Slack's group presses Return: not in TextEdit.
				if len(presses[0]) != 0 || e[0].Delivery != "insert" || e[0].InsertedBundle != tc.into.BundleID || e[0].InsertedName != tc.into.Name {
					t.Errorf("presses %+v, entry %+v", presses, e[0])
				}
				if !h.logged("inserted into com.apple.TextEdit, not com.tinyspeck.slackmacgap") {
					t.Errorf("logs %q", h.logs)
				}
			} else if len(presses[0]) != 1 || e[0].InsertedBundle != "" || e[0].InsertedName != "" {
				t.Errorf("in Slack: presses %+v, entry %+v", presses, e[0])
			}
		})
	}
}

// TestWriteFollowsFocus: a translation goes where the focus is when it
// is ready, as Transcribe's result does, and is decided only then (the
// user may still switch apps while the model writes): a speech
// translation whose user moved to TextEdit's field goes there, one whose
// user moved to Finder ends in the card (without streaming into it).
// A selection is replaced only in the app it was made in: checked before
// the answer, which streams into the card at once when another app is in
// front.
func TestWriteFollowsFocus(t *testing.T) {
	inSlack := proto.FocusResult{PID: slack.PID, App: &slack, Role: "AXTextArea", Editable: true}
	inTextEdit := proto.FocusResult{PID: textEdit.PID, App: &textEdit, Role: "AXTextArea", Editable: true}
	for _, tc := range []struct {
		name   string
		sel    string
		focus  proto.FocusResult
		into   int
		reason deliver.Reason
	}{
		{name: "speech, TextEdit's field", focus: inTextEdit, into: textEdit.PID},
		{name: "speech, Finder", focus: proto.FocusResult{PID: finder.PID, App: &finder, Role: "AXOutline"}, reason: deliver.ReasonNotEditable},
		{name: "a selection, TextEdit in front", sel: "Good morning", focus: inTextEdit, reason: deliver.ReasonNotFront},
		{name: "a selection, still in Slack", sel: "Good morning", focus: inSlack, into: slack.PID},
		{name: "a selection on a web page in Slack", sel: "Good morning", focus: proto.FocusResult{PID: slack.PID, App: &slack, Role: "AXWebArea"}, reason: deliver.ReasonNotEditable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, &fakeFile{text: "good morning"}, &fakeLLM{text: "早上好"}, func(c *Config, s *settings.Settings) {
				c.Select = &fakeSelector{text: tc.sel}
			})
			if tc.sel != "" {
				h.text.setFocus(tc.focus) // moved on as soon as it started
			}
			id, err := h.m.Start(StartRequest{Kind: store.KindTranslate, Target: slack})
			if err != nil {
				t.Fatal(err)
			}
			if tc.sel == "" {
				h.rec.waitFor(t, "recording", func(s Snapshot) bool { return s.State == Recording })
				h.text.setFocus(tc.focus)
				h.speak(id, 3)
			}
			end := h.rec.wait(t)
			h.text.mu.Lock()
			targets := h.text.targets
			h.text.mu.Unlock()
			if tc.into != 0 {
				if len(targets) != 1 || targets[0] != tc.into || !end.Inserted || end.InsertedInto.PID != tc.into {
					t.Errorf("pasted into %v, %+v", targets, end)
				}
				return
			}
			streamed := false // into the card while the model wrote
			for _, s := range h.rec.all() {
				streamed = streamed || s.State == Composing && s.Card
			}
			if len(targets) != 0 || end.Inserted || !end.Card || end.Delivery != tc.reason || streamed != (tc.sel != "") {
				t.Errorf("card: pasted into %v, %+v (streamed into the card %v)", targets, end, streamed)
			}
		})
	}
}
