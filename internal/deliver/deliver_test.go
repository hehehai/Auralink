package deliver

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hehehai/Auralink/internal/helper/proto"
)

type fakeIO struct {
	focus    []proto.FocusResult // one per call; the last repeats
	focusErr error
	paste    proto.PasteResult
	pasteErr error
	pasted   []proto.PasteParams
	calls    int
}

func (f *fakeIO) Focus(context.Context) (proto.FocusResult, error) {
	if f.focusErr != nil {
		return proto.FocusResult{}, f.focusErr
	}
	r := f.focus[min(f.calls, len(f.focus)-1)]
	f.calls++
	return r, nil
}

func (f *fakeIO) Paste(_ context.Context, p proto.PasteParams) (proto.PasteResult, error) {
	f.pasted = append(f.pasted, p)
	return f.paste, f.pasteErr
}

var (
	slack    = proto.App{PID: 812, BundleID: "com.tinyspeck.slackmacgap", Name: "Slack"}
	arc      = proto.App{PID: 900, BundleID: "company.thebrowser.Browser", Name: "Arc"}
	textEdit = proto.App{PID: 530, BundleID: "com.apple.TextEdit", Name: "TextEdit"}
)

func TestInsert(t *testing.T) {
	textArea := proto.FocusResult{PID: 812, Role: "AXTextArea", Editable: true}
	elsewhere := proto.FocusResult{PID: 530, App: &textEdit, Role: "AXTextArea", Editable: true}
	posted := proto.PasteResult{Posted: true, Restored: true}
	for _, tc := range []struct {
		name     string
		req      Request
		io       fakeIO
		inserted bool
		reason   Reason
		pastes   int
		activate bool
		to       int // the paste's targetPid (812 when 0)
	}{
		{name: "pastes", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{textArea}, paste: posted}, inserted: true, pastes: 1},
		{name: "empty", req: Request{Target: slack}, io: fakeIO{focus: []proto.FocusResult{textArea}}, reason: ReasonEmpty},
		{name: "start app unknown: where the user types", req: Request{Text: "hi"}, io: fakeIO{focus: []proto.FocusResult{elsewhere}, paste: posted}, inserted: true, pastes: 1, to: 530},
		{name: "switched to another app's text field", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{elsewhere}, paste: posted}, inserted: true, pastes: 1, to: 530},
		{name: "switched to an app with nothing focused", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 530, Error: "no focused element"}}}, reason: ReasonNotEditable},
		{name: "switched to a web page being read", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 530, Role: "AXWebArea"}}}, reason: ReasonNotEditable},
		{name: "a password field", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXTextField", Subrole: "AXSecureTextField", Editable: true}}}, reason: ReasonSecure},
		{name: "nothing focused", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812}}}, reason: ReasonNotEditable},
		{name: "a button", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXButton"}}}, reason: ReasonNotEditable},
		{name: "a button, the card's Insert too", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXButton", App: &slack}}}, reason: ReasonNotEditable},
		{name: "the start app's web area (Arc's page, not editable) pastes", req: Request{Text: "hi", Target: arc}, io: fakeIO{focus: []proto.FocusResult{{PID: 900, App: &arc, Role: "AXWebArea"}}, paste: posted}, inserted: true, pastes: 1, to: 900},
		{name: "a group of the start app pastes", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXGroup"}}, paste: posted}, inserted: true, pastes: 1},
		{name: "another app's group goes to the card", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 530, App: &textEdit, Role: "AXGroup"}}}, reason: ReasonNotEditable},
		{name: "a password field in the start app is refused", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXGroup", Subrole: "AXSecureTextField"}}}, reason: ReasonSecure},
		{name: "a button in the start app goes to the card", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXButton"}}}, reason: ReasonNotEditable},
		{name: "static text goes to the card", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXStaticText"}}}, reason: ReasonNotEditable},
		{name: "an unknown role goes to the card", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXSomethingNew"}}}, reason: ReasonNotEditable},
		{name: "a web page's editable region pastes", req: Request{Text: "hi", Target: arc}, io: fakeIO{focus: []proto.FocusResult{{PID: 900, App: &arc, Role: "AXWebArea", Editable: true}}, paste: posted}, inserted: true, pastes: 1, to: 900},
		{name: "a contenteditable group pastes", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXGroup", Editable: true}}, paste: posted}, inserted: true, pastes: 1},
		{name: "a selection needs an editable element", req: Request{Text: "hi", Target: slack, Replace: true}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXWebArea"}}}, reason: ReasonNotEditable},
		{name: "an editable selection", req: Request{Text: "hi", Target: slack, Replace: true}, io: fakeIO{focus: []proto.FocusResult{textArea}, paste: posted}, inserted: true, pastes: 1},
		{name: "a selection, another app in front", req: Request{Text: "hi", Target: slack, Replace: true}, io: fakeIO{focus: []proto.FocusResult{elsewhere}}, reason: ReasonNotFront},
		{name: "a selection, start app unknown", req: Request{Text: "hi", Replace: true}, io: fakeIO{focus: []proto.FocusResult{elsewhere}}, reason: ReasonNoTarget},
		{name: "AX could not tell: tried", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{{PID: 812, Error: "-25212"}}, paste: posted}, inserted: true, pastes: 1},
		{name: "front changed before the paste", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{textArea}, paste: proto.PasteResult{Front: &proto.App{PID: 530, Name: "TextEdit"}}}, reason: ReasonNotFront, pastes: 1},
		{name: "no accessibility", req: Request{Text: "hi", Target: slack}, io: fakeIO{focusErr: &proto.Error{Code: proto.CodePermissionDenied, Message: "no"}}, reason: ReasonNoAccess},
		{name: "paste refused", req: Request{Text: "hi", Target: slack}, io: fakeIO{focus: []proto.FocusResult{textArea}, pasteErr: &proto.Error{Code: proto.CodePermissionDenied}}, reason: ReasonNoAccess, pastes: 1},
		{name: "helper gone", req: Request{Text: "hi", Target: slack}, io: fakeIO{focusErr: errors.New("helper exited")}, reason: ReasonFailed},
		{name: "Auralink in front: activate the target", req: Request{Text: "hi", Target: slack, Self: 77, Activate: func(int) bool { return true }},
			io: fakeIO{focus: []proto.FocusResult{{PID: 77, Role: "AXTextField", Editable: true}, textArea}, paste: posted}, inserted: true, pastes: 1, activate: true},
		{name: "Auralink in front, activation failed: never into its own field", req: Request{Text: "hi", Target: slack, Self: 77, Activate: func(int) bool { return false }},
			io: fakeIO{focus: []proto.FocusResult{{PID: 77, Role: "AXTextField", Editable: true}}}, reason: ReasonNotFront},
		{name: "Auralink in front, no start app", req: Request{Text: "hi", Self: 77, Activate: func(int) bool { return true }},
			io: fakeIO{focus: []proto.FocusResult{{PID: 77, Role: "AXTextField", Editable: true}}}, reason: ReasonNotFront},
	} {
		io := tc.io
		res := Insert(context.Background(), &io, tc.req)
		if res.Inserted != tc.inserted || res.Reason != tc.reason || len(io.pasted) != tc.pastes || res.Activated != tc.activate {
			t.Errorf("%s: %+v (pastes %d)", tc.name, res, len(io.pasted))
		}
		to := cmp.Or(tc.to, 812)
		if len(io.pasted) > 0 && (io.pasted[0].TargetPID != to || io.pasted[0].Text != "hi") {
			t.Errorf("%s: pasted %+v", tc.name, io.pasted[0])
		}
		if tc.inserted && res.To.PID != to {
			t.Errorf("%s: went to %+v", tc.name, res.To)
		}
	}
}

// TestInsertElsewhere: text that goes into another app is recorded with
// that app's name, and the start app's auto key is not pressed there.
func TestInsertElsewhere(t *testing.T) {
	io := &fakeIO{focus: []proto.FocusResult{{PID: 530, App: &textEdit, Role: "AXTextArea", Editable: true}}, paste: proto.PasteResult{Posted: true}}
	res := Insert(context.Background(), io, Request{Text: "x", Target: slack, Press: []proto.KeyPress{{Key: 36}}})
	if !res.Inserted || res.To != textEdit || !res.Moved(slack) || len(io.pasted[0].Press) != 0 {
		t.Errorf("elsewhere %+v, pasted %+v", res, io.pasted)
	}
	io = &fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXTextArea", Editable: true}}, paste: proto.PasteResult{Posted: true}}
	res = Insert(context.Background(), io, Request{Text: "x", Target: slack, Press: []proto.KeyPress{{Key: 36}}})
	if res.To != slack || res.Moved(slack) || len(io.pasted[0].Press) != 1 {
		t.Errorf("same app %+v, pasted %+v", res, io.pasted)
	}
	io = &fakeIO{focus: []proto.FocusResult{{PID: 530, Error: "no focused element"}}}
	res = Insert(context.Background(), io, Request{Text: "x", Target: slack})
	if res.Front.PID != 530 || res.To.PID != 0 {
		t.Errorf("not inserted %+v", res)
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
}

// TestInsertAnotherApp: in another app the element must say it takes
// text: a web editor's element does (the helper reads AXEditableAncestor
// into Editable), Finder's list does not; a panel of an app not in front
// is refused by the helper's paste.
func TestInsertAnotherApp(t *testing.T) {
	finder := proto.App{PID: 640, BundleID: "com.apple.finder", Name: "Finder"}
	posted := proto.PasteResult{Posted: true}
	for _, tc := range []struct {
		name   string
		focus  proto.FocusResult
		paste  proto.PasteResult
		reason Reason
	}{
		{"a contenteditable editor (Notion, Slack's web app)", proto.FocusResult{PID: 530, App: &textEdit, Role: "AXGroup", Editable: true}, posted, ReasonNone},
		{"Finder's list", proto.FocusResult{PID: 640, App: &finder, Role: "AXOutline"}, posted, ReasonNotEditable},
		{"a launcher's panel, its app not in front", proto.FocusResult{PID: 530, App: &textEdit, Role: "AXTextField", Editable: true}, proto.PasteResult{Front: &finder}, ReasonNotFront},
	} {
		io := &fakeIO{focus: []proto.FocusResult{tc.focus}, paste: tc.paste}
		res := Insert(context.Background(), io, Request{Text: "x", Target: slack})
		if res.Reason != tc.reason || res.Inserted != (tc.reason == ReasonNone) {
			t.Errorf("%s: %+v", tc.name, res)
		}
	}
	// Check: another app's field would take an answer, not a replaced
	// selection.
	io := &fakeIO{focus: []proto.FocusResult{{PID: 530, App: &textEdit, Role: "AXTextArea", Editable: true}}}
	if res := Check(context.Background(), io, Request{Text: "x", Target: slack}); res.Reason != ReasonNone || res.To != textEdit {
		t.Errorf("check another app's field %+v", res)
	}
	if res := Check(context.Background(), io, Request{Text: "x", Target: slack, Replace: true}); res.Reason != ReasonNotFront || len(io.pasted) != 0 {
		t.Errorf("check a selection's replacement in another app %+v", res)
	}
}

// TestWritableRule: only an editable element (or one that could not be
// read) takes text in the start app.
func TestWritableRule(t *testing.T) {
	for _, tc := range []struct {
		f    proto.FocusResult
		want bool
	}{
		{proto.FocusResult{Role: "AXTextArea", Editable: true}, true},
		{proto.FocusResult{Role: "AXWebArea", Editable: true}, true},
		{proto.FocusResult{Role: "AXWebArea"}, false},
		{proto.FocusResult{Role: "AXGroup"}, false},
		{proto.FocusResult{Role: "AXStaticText"}, false},
		{proto.FocusResult{Role: "AXButton"}, false},
		{proto.FocusResult{}, false},
		{proto.FocusResult{Error: "no focused element"}, true},
	} {
		if got := Writable(tc.f); got != tc.want {
			t.Errorf("Writable(%+v) = %v", tc.f, got)
		}
	}
}

// TestTargetWritable: the start app's focus takes a result when it is
// editable or a generic container; never a password field, never a control.
func TestTargetWritable(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    proto.FocusResult
		want bool
	}{
		{"text area", proto.FocusResult{Role: "AXTextArea", Editable: true}, true},
		{"web area", proto.FocusResult{Role: "AXWebArea"}, true},
		{"group", proto.FocusResult{Role: "AXGroup"}, true},
		{"unknown", proto.FocusResult{Role: "AXUnknown"}, true},
		{"unreadable focus is tried", proto.FocusResult{Error: "-25212"}, true},
		{"password field", proto.FocusResult{Role: "AXTextField", Subrole: "AXSecureTextField", Editable: true}, false},
		{"password field in a group", proto.FocusResult{Role: "AXGroup", Subrole: "AXSecureTextField"}, false},
		{"button", proto.FocusResult{Role: "AXButton"}, false},
		{"static text", proto.FocusResult{Role: "AXStaticText"}, false},
		{"nothing focused", proto.FocusResult{}, false},
	} {
		if got := targetWritable(tc.f); got != tc.want {
			t.Errorf("%s: targetWritable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestDescribe: the log line says the focus and the outcome, never the
// text.
func TestDescribe(t *testing.T) {
	arcWeb := proto.FocusResult{PID: 900, App: &arc, Role: "AXWebArea"}
	for _, tc := range []struct {
		name string
		res  Result
		want string
	}{
		{"Arc's page text goes to the card", Result{Focus: arcWeb, Reason: ReasonNotEditable},
			"focus role=AXWebArea editable=false app=company.thebrowser.Browser -> card (no text field)"},
		{"inserted", Result{Focus: proto.FocusResult{PID: 812, App: &slack, Role: "AXTextArea", Editable: true}, Inserted: true, Landed: proto.LandedYes, To: slack, Pasted: true},
			"focus role=AXTextArea editable=true app=com.tinyspeck.slackmacgap -> inserted"},
		{"a password field", Result{Focus: proto.FocusResult{PID: 812, App: &slack, Role: "AXTextField", Subrole: "AXSecureTextField"}, Reason: ReasonSecure},
			"focus role=AXTextField editable=false app=com.tinyspeck.slackmacgap subrole=AXSecureTextField -> card (password field)"},
		{"unknown focus, tried", Result{Focus: proto.FocusResult{PID: 812, Error: "-25212", App: &slack}, Pasted: true, Inserted: true, Landed: proto.LandedYes, To: slack},
			"focus unknown (-25212) app=com.tinyspeck.slackmacgap -> tried paste, inserted"},
		{"switched", Result{Focus: proto.FocusResult{PID: 530, App: &textEdit, Role: "AXTextArea", Editable: true}, Inserted: true, Landed: proto.LandedYes, To: textEdit, Pasted: true},
			"focus role=AXTextArea editable=true app=com.apple.TextEdit -> inserted into com.apple.TextEdit (the user switched apps)"},
		{"check", Result{Focus: proto.FocusResult{PID: 812, App: &slack, Role: "AXTextArea", Editable: true}},
			"focus role=AXTextArea editable=true app=com.tinyspeck.slackmacgap -> would insert"},
		{"no focus read", Result{Reason: ReasonCard}, "focus not read -> card (card choice)"},
	} {
		if got := tc.res.Describe(slack); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// TestPostedIsNotConfirmed: ⌘V posted is not the text in the field. The
// helper's Confirmed says what the field showed; "no" must not count as
// inserted (the history said "insert" while nothing had landed: 10-29).
func TestPostedIsNotConfirmed(t *testing.T) {
	textArea := proto.FocusResult{PID: 812, Role: "AXTextArea", Editable: true}
	for _, tc := range []struct {
		name      string
		confirmed string
		inserted  bool
		landed    string
		reason    Reason
	}{
		{"field showed the text", proto.LandedYes, true, proto.LandedYes, ReasonNone},
		{"field did not change", proto.LandedNo, false, proto.LandedNo, ReasonNotLanded},
		{"no value could be read", proto.LandedUnknown, true, proto.LandedUnknown, ReasonNone},
		{"an older helper says nothing", "", true, proto.LandedUnknown, ReasonNone},
	} {
		io := &fakeIO{focus: []proto.FocusResult{textArea}, paste: proto.PasteResult{Posted: true, Confirmed: tc.confirmed}}
		res := Insert(context.Background(), io, Request{Text: "hi", Target: slack})
		if res.Inserted != tc.inserted || res.Reason != tc.reason || res.Landed != tc.landed {
			t.Errorf("%s: inserted %v reason %q landed %q, want %v %q %q", tc.name, res.Inserted, res.Reason, res.Landed, tc.inserted, tc.reason, tc.landed)
		}
	}
}

// TestNotLandedKeepsTheCard: a paste that did not land goes to the card,
// and its log line says so.
func TestNotLandedKeepsTheCard(t *testing.T) {
	io := &fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXTextArea", Editable: true}}, paste: proto.PasteResult{Posted: true, Confirmed: proto.LandedNo}}
	res := Insert(context.Background(), io, Request{Text: "hi", Target: slack})
	if got := res.Describe(slack); !strings.HasSuffix(got, "card (not confirmed)") {
		t.Errorf("describe: %q", got)
	}
}

// TestNotLandedOnClipboard: the log says the text is still on the
// clipboard when the helper left it there.
func TestNotLandedOnClipboard(t *testing.T) {
	io := &fakeIO{focus: []proto.FocusResult{{PID: 812, Role: "AXTextArea", Editable: true}}, paste: proto.PasteResult{Posted: true, Confirmed: proto.LandedNo, LeftOnClipboard: true}}
	res := Insert(context.Background(), io, Request{Text: "hi", Target: slack})
	if res.Reason != ReasonNotLanded || !res.Paste.LeftOnClipboard {
		t.Fatalf("reason %q, paste %+v", res.Reason, res.Paste)
	}
	if got := res.Describe(slack); !strings.HasSuffix(got, "card (not confirmed, text left on the clipboard)") {
		t.Errorf("describe: %q", got)
	}
}
