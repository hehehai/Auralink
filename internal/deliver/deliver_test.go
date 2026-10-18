package deliver

import (
	"context"
	"errors"
	"testing"

	"github.com/hehehai/Auralink/internal/helper/proto"
)

type fakeIO struct {
	focus    []proto.FocusResult // one per call; the last repeats
	focusErr error
	front    *proto.App // app.frontmost (nil: the focused app, named by apps)
	frontErr error
	paste    proto.PasteResult
	pasteErr error
	pasted   []proto.PasteParams
	calls    int
	last     int // the pid of the last focus read
}

func (f *fakeIO) Focus(context.Context) (proto.FocusResult, error) {
	if f.focusErr != nil {
		return proto.FocusResult{}, f.focusErr
	}
	r := f.focus[min(f.calls, len(f.focus)-1)]
	f.calls++
	f.last = r.PID
	return r, nil
}

func (f *fakeIO) Frontmost(context.Context) (proto.App, error) {
	if f.frontErr != nil {
		return proto.App{}, f.frontErr
	}
	if f.front != nil {
		return *f.front, nil
	}
	return apps[f.last], nil
}

var apps = map[int]proto.App{812: slack, 530: textEdit, 77: self, 640: finder}

func (f *fakeIO) Paste(_ context.Context, p proto.PasteParams) (proto.PasteResult, error) {
	f.pasted = append(f.pasted, p)
	return f.paste, f.pasteErr
}

var (
	slack    = proto.App{PID: 812, BundleID: "com.tinyspeck.slackmacgap", Name: "Slack"}
	textEdit = proto.App{PID: 530, BundleID: "com.apple.TextEdit", Name: "TextEdit"}
	self     = proto.App{PID: 77, BundleID: "dev.hehehai.auralink", Name: "Auralink"}
	finder   = proto.App{PID: 640, BundleID: "com.apple.finder", Name: "Finder"}
)

// TestInsert is the rule table: where the focus is at the end decides
// (the app the session started in, Auralink, another app), then what it
// is.
func TestInsert(t *testing.T) {
	textArea := proto.FocusResult{PID: 812, Role: "AXTextArea", Editable: true}
	other := proto.FocusResult{PID: 530, Role: "AXTextArea", Editable: true}
	password := proto.FocusResult{PID: 530, Role: "AXTextField", Subrole: "AXSecureTextField", Editable: true}
	posted := proto.PasteResult{Posted: true, Restored: true}
	yes := func(int) bool { return true }
	for _, tc := range []struct {
		name     string
		req      Request
		io       fakeIO
		inserted bool
		reason   Reason
		into     int // the pid pasted to (TargetPID) or reported
		pastes   int
		activate bool
	}{
		// The app the session started in has the focus.
		{name: "pastes", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{textArea}, paste: posted}, inserted: true, into: 812, pastes: 1},
		{name: "empty", req: Request{Target: slack}, io: fakeIO{focus: []proto.FocusResult{textArea}}, reason: ReasonEmpty},
		{name: "nothing focused", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812}}}, reason: ReasonNotEditable, into: 812},
		{name: "a button", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXButton"}}}, reason: ReasonNotEditable, into: 812},
		{name: "a button, the user chose", req: Request{Text: "hi", Target: slack, AnyEditable: true}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXButton"}}, paste: posted}, inserted: true, into: 812, pastes: 1},
		{name: "a web area may be an editor", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXWebArea"}}, paste: posted}, inserted: true, into: 812, pastes: 1},
		{name: "a selection needs an editable element", req: Request{Text: "hi", Target: slack, NeedEditable: true}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXWebArea"}}}, reason: ReasonNotEditable, into: 812},
		{name: "an editable selection", req: Request{Text: "hi", Target: slack, NeedEditable: true}, io: fakeIO{focus: []proto.FocusResult{textArea}, paste: posted}, inserted: true, into: 812, pastes: 1},
		{name: "AX could not tell", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Error: "no focused element"}}, paste: posted}, inserted: true, into: 812, pastes: 1},
		{name: "a password field", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXTextField", Subrole: "AXSecureTextField", Editable: true}}}, reason: ReasonSecure, into: 812},
		{name: "a password field, the user chose", req: Request{Text: "hi", Target: slack, AnyEditable: true}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Subrole: "AXSecureTextField", Editable: true}}}, reason: ReasonSecure, into: 812},

		// Another app has it: the user moved on.
		{name: "another app's field", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{other}, paste: posted}, inserted: true, into: 530, pastes: 1},
		{name: "another app's web area", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 530, Role: "AXWebArea"}}, paste: posted}, inserted: true, into: 530, pastes: 1},
		{name: "unknown target, another app's field", req: Request{Text: "hi"}, io: fakeIO{focus: []proto.FocusResult{other}, paste: posted}, inserted: true, into: 530, pastes: 1},
		{name: "another app: a list (Finder)", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 640, Role: "AXOutline"}}}, reason: ReasonNotEditable, into: 640},
		{name: "another app: nothing focused", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 640}}}, reason: ReasonNotEditable, into: 640},
		{name: "another app: AX could not tell", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 640, Error: "no focused element"}}}, reason: ReasonNotEditable, into: 640},
		{name: "another app: AX could not tell, the user chose", req: Request{Text: "hi", Target: slack, AnyEditable: true}, io: fakeIO{focus: []proto.FocusResult{{PID: 530, Error: "no focused element"}}, paste: posted}, inserted: true, into: 530, pastes: 1},
		{name: "another app: a button, the user chose", req: Request{Text: "hi", Target: slack, AnyEditable: true}, io: fakeIO{focus: []proto.FocusResult{{PID: 530, Role: "AXButton"}}}, reason: ReasonNotEditable, into: 530},
		{name: "another app: no Accessibility", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 530, Error: proto.FocusNoAccess}}}, reason: ReasonNoAccess, into: 530},
		{name: "another app: a password field", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{password}}, reason: ReasonSecure, into: 530},
		{name: "another app: a selection needs an editable element", req: Request{Text: "hi", Target: slack, NeedEditable: true}, io: fakeIO{focus: []proto.FocusResult{{PID: 530, Role: "AXWebArea"}}}, reason: ReasonNotEditable, into: 530},
		{name: "another app: an editable element takes a selection's result", req: Request{Text: "hi", Target: slack, NeedEditable: true}, io: fakeIO{focus: []proto.FocusResult{other}, paste: posted}, inserted: true, into: 530, pastes: 1},
		{name: "another app: the focus is in a panel of an app not in front", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{other}, front: &finder}, reason: ReasonNotFront, into: 530},
		{name: "another app: app.frontmost failed", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{other}, frontErr: errors.New("old helper"), paste: posted}, inserted: true, into: 530, pastes: 1},
		{name: "nothing in front", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{}}}, reason: ReasonNotEditable},

		// The focus moved again between the check and the paste.
		{name: "front changed before the paste", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{textArea}, paste: proto.PasteResult{Front: &textEdit}}, reason: ReasonNotFront, into: 812, pastes: 1},
		{name: "another app, front changed before the paste", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{other}, paste: proto.PasteResult{Front: &finder}}, reason: ReasonNotFront, into: 530, pastes: 1},

		// Failures.
		{name: "no accessibility", req: Request{Text: "hi", Target: slack}, io: fakeIO{focusErr: &proto.Error{Code: proto.CodePermissionDenied, Message: "no"}}, reason: ReasonNoAccess},
		{name: "paste refused", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{textArea}, pasteErr: &proto.Error{Code: proto.CodePermissionDenied}}, reason: ReasonNoAccess, into: 812, pastes: 1},
		{name: "helper gone", req: Request{Text: "hi", Target: slack}, io: fakeIO{focusErr: errors.New("helper exited")}, reason: ReasonFailed},

		// Auralink has the focus: back to the app the session started in.
		{name: "Auralink in front: activate the target", req: Request{Text: "hi", Target: slack, Self: 77, Activate: yes},
			io: fakeIO{focus: []proto.FocusResult{{PID: 77, Role: "AXWindow"}, textArea}, paste: posted}, inserted: true, into: 812, pastes: 1, activate: true},
		{name: "Auralink in front, activation failed", req: Request{Text: "hi", Target: slack, Self: 77, Activate: func(int) bool { return false }},
			io: fakeIO{focus: []proto.FocusResult{{PID: 77}}}, reason: ReasonNotFront, into: 812},
		{name: "Auralink in front, another app came up instead", req: Request{Text: "hi", Target: slack, Self: 77, Activate: yes},
			io: fakeIO{focus: []proto.FocusResult{{PID: 77}, other}}, reason: ReasonNotFront, into: 530, activate: true},
		{name: "Auralink in front, no target", req: Request{Text: "hi", Self: 77, Activate: yes}, io: fakeIO{focus: []proto.FocusResult{{PID: 77, Role: "AXTextField", Editable: true}}}, reason: ReasonNoTarget},
		{name: "Auralink in front, the target is Auralink", req: Request{Text: "hi", Target: self, Self: 77, Activate: yes}, io: fakeIO{focus: []proto.FocusResult{{PID: 77, Role: "AXTextField", Editable: true}}}, reason: ReasonNoTarget},
		{name: "without Self, Auralink is an app like the others", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 77, Role: "AXTextField", Editable: true}}, paste: posted}, inserted: true, into: 77, pastes: 1},
	} {
		io := tc.io
		res := Insert(context.Background(), &io, tc.req)
		if res.Inserted != tc.inserted || res.Reason != tc.reason || len(io.pasted) != tc.pastes || res.Activated != tc.activate || res.Into.PID != tc.into {
			t.Errorf("%s: %+v (pastes %d)", tc.name, res, len(io.pasted))
		}
		if len(io.pasted) > 0 && (io.pasted[0].TargetPID != tc.into || io.pasted[0].Text != "hi") {
			t.Errorf("%s: pasted %+v", tc.name, io.pasted[0])
		}
		if want, ok := apps[tc.into]; ok && tc.into != 0 && tc.into != 812 && !tc.activate && io.front == nil && io.frontErr == nil && res.Into != want {
			t.Errorf("%s: into %+v, want %+v", tc.name, res.Into, want)
		}
	}
}

// TestInsertPressOnlyInTarget: the auto key is the starting app's group's,
// so pasting into another app presses nothing.
func TestInsertPressOnlyInTarget(t *testing.T) {
	enter := []proto.KeyPress{{Key: 36}}
	io := &fakeIO{focus: []proto.FocusResult{{PID: 812, Editable: true}}, paste: proto.PasteResult{Posted: true}}
	Insert(context.Background(), io, Request{Text: "x", Target: slack, Press: enter})
	io2 := &fakeIO{focus: []proto.FocusResult{{PID: 530, Role: "AXTextArea", Editable: true}}, paste: proto.PasteResult{Posted: true}}
	res := Insert(context.Background(), io2, Request{Text: "x", Target: slack, Press: enter})
	if len(io.pasted[0].Press) != 1 || len(io2.pasted[0].Press) != 0 || !res.Elsewhere(slack) || res.Into != textEdit {
		t.Errorf("press in the target %+v, elsewhere %+v (%+v)", io.pasted[0].Press, io2.pasted[0].Press, res.Into)
	}
}

func TestInsertPassesPolicy(t *testing.T) {
	io := &fakeIO{focus: []proto.FocusResult{{PID: 812, Editable: true}}, paste: proto.PasteResult{Posted: true}}
	Insert(context.Background(), io, Request{Text: "x", Target: slack, Press: []proto.KeyPress{{Key: 36}}, PressDelayMS: 200, KeepClipboard: true, RestoreDelayMS: 800})
	p := io.pasted[0]
	if len(p.Press) != 1 || p.Press[0].Key != 36 || p.PressDelayMS != 200 || !p.KeepClipboard || p.RestoreDelayMS != 800 {
		t.Errorf("params %+v", p)
	}
}

func TestCheck(t *testing.T) {
	io := &fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXTextArea", Editable: true}}}
	if res := Check(context.Background(), io, Request{Text: "x", Target: slack}); res.Reason != ReasonNone || res.Inserted || len(io.pasted) != 0 {
		t.Errorf("check %+v, pasted %d", res, len(io.pasted))
	}
	activated := false
	io = &fakeIO{focus: []proto.FocusResult{{PID: 77}}}
	res := Check(context.Background(), io, Request{Text: "x", Target: slack, Self: 77, Activate: func(int) bool { activated = true; return true }})
	if res.Reason != ReasonNotFront || activated || len(io.pasted) != 0 {
		t.Errorf("check with Auralink in front %+v (activated %v)", res, activated)
	}
	io = &fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXButton"}}}
	if res := Check(context.Background(), io, Request{Text: "x", Target: slack}); res.Reason != ReasonNotEditable {
		t.Errorf("check a button %+v", res)
	}
	// The same rule as Insert: another app's field would take it, Finder's
	// list would not.
	io = &fakeIO{focus: []proto.FocusResult{{PID: 530, Role: "AXTextArea", Editable: true}}}
	if res := Check(context.Background(), io, Request{Text: "x", Target: slack}); res.Reason != ReasonNone || res.Into != textEdit || len(io.pasted) != 0 {
		t.Errorf("check another app's field %+v", res)
	}
	io = &fakeIO{focus: []proto.FocusResult{{PID: 640, Role: "AXOutline"}}}
	if res := Check(context.Background(), io, Request{Text: "x", Target: slack}); res.Reason != ReasonNotEditable || res.Into != finder {
		t.Errorf("check Finder %+v", res)
	}
}
