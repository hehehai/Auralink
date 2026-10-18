// Package deliver puts a result where the user is typing (plan §4.1):
// one insert transaction through the helper's text I/O. It follows the
// focus at the end, not the app the session started in (Voxt's: it
// pastes into the app in front):
//
//   - Auralink itself has the focus (its window, after a click on it):
//     the app the session started in (Target) is activated again first
//     (macx.Activate), and the text goes there;
//   - the app the session started in has it: the text goes there when its
//     focused element may take text (Writable; Accessibility's "cannot
//     tell" included);
//   - another app has it (the user moved on while the session worked):
//     the text goes there when Accessibility sees an element that takes
//     text (Writable, and an answer: "cannot tell" is not enough where
//     the user did not start);
//   - otherwise (nothing takes text, a password field, the focus moved
//     again before the paste) nothing is typed and the caller shows the
//     answer card, which offers Insert and Copy.
//
// The paste goes through textio.paste with the pid of the app checked
// (TargetPID): the helper posts nothing when another app came to the
// front in between. Keys after the paste (app enhancement's auto key)
// are pressed only in the app the session started in: they are its
// group's.
//
// Policy is here, the mechanics in the helper: the restore delay, the
// keys after the paste and whether the clipboard keeps the text are
// params of textio.paste.
//
// Imports: helper/proto.
package deliver

import (
	"context"
	"errors"
	"time"

	"github.com/hehehai/Auralink/internal/helper/proto"
)

// TextIO is the helper's text I/O (textio.focus, textio.paste,
// app.frontmost).
type TextIO interface {
	Focus(ctx context.Context) (proto.FocusResult, error)
	Paste(ctx context.Context, p proto.PasteParams) (proto.PasteResult, error)
	Frontmost(ctx context.Context) (proto.App, error)
}

// Reason says why a result was not inserted.
type Reason string

const (
	ReasonNone Reason = ""
	// ReasonEmpty: there was no text.
	ReasonEmpty Reason = "empty"
	// ReasonNoTarget: Auralink has the focus and the app the session
	// started in is not known (or is Auralink).
	ReasonNoTarget Reason = "noTarget"
	// ReasonNotFront: the place checked is gone: Auralink has the focus and
	// the app the session started in could not be brought back, or the
	// app in front is not the one with the focus (a floating panel of
	// another app), or it changed before the paste.
	ReasonNotFront Reason = "notFront"
	// ReasonNotEditable: the focused element takes no text (or nothing
	// has the focus, or, in another app, Accessibility cannot tell).
	ReasonNotEditable Reason = "notEditable"
	// ReasonSecure: the focus is in a password field.
	ReasonSecure Reason = "secure"
	// ReasonNoAccess: Auralink may not post keys (Accessibility).
	ReasonNoAccess Reason = "noAccess"
	// ReasonFailed: the helper failed (Result.Err).
	ReasonFailed Reason = "failed"
	// ReasonCard: the result goes to the answer card by choice (Translate's
	// "Show the result in a card"), or always (Rewrite, Voxt's); ReasonLong
	// was 0.1.0's: an answer longer than the card's limit. The session sets
	// these, not Insert.
	ReasonCard Reason = "card"
	ReasonLong Reason = "long" // 0.1.0's (the history may hold it)
)

// Request is one insert.
type Request struct {
	Text string
	// Target is the app the session started in (hotkey.event's front):
	// the one activated again when Auralink has the focus, and the only
	// one Press is pressed in.
	Target proto.App
	// Press are keys posted after the paste (app enhancement's auto key,
	// M5), PressDelayMS after it (0: the helper's 120).
	Press        []proto.KeyPress
	PressDelayMS int
	// KeepClipboard leaves the text on the clipboard; RestoreDelayMS
	// waits before restoring it (0: the helper's 500).
	KeepClipboard  bool
	RestoreDelayMS int
	// Self is Auralink's pid. When Auralink has the focus, Activate (nil:
	// none) gives the front back to Target before pasting. Without Self,
	// Auralink is an app like the others (Paste the last result into one
	// of its fields).
	Self     int
	Activate func(pid int) bool
	// AnyEditable is the answer card's Insert: the user looked and chose.
	// In Target any focused element will do; in another app one that may
	// take text (Writable, "cannot tell" included). NeedEditable asks for
	// an element Accessibility calls editable, not one that may be (text
	// selected on a web page must not be "replaced" by a paste that goes
	// nowhere).
	AnyEditable  bool
	NeedEditable bool
}

// Result is what happened.
type Result struct {
	Inserted bool
	Reason   Reason
	// Into is the app the text went to (or, for Check, would go to; when
	// not inserted, the one that had the focus): Target, or another app.
	Into proto.App
	// Front is the app in front when the helper posted nothing.
	Front     proto.App
	Focus     proto.FocusResult
	Paste     proto.PasteResult
	Activated bool // the target was activated again first
	Err       error
	Elapsed   time.Duration
}

// Elsewhere says whether the text went (or would go) to an app other than
// the one the session started in.
func (r Result) Elsewhere(target proto.App) bool {
	return r.Into.PID != 0 && r.Into.PID != target.PID
}

// nonText are focused roles that never take text.
var nonText = map[string]bool{
	"AXButton": true, "AXCheckBox": true, "AXRadioButton": true, "AXPopUpButton": true, "AXMenuButton": true,
	"AXMenuItem": true, "AXMenuBarItem": true, "AXImage": true, "AXList": true, "AXOutline": true, "AXTable": true,
	"AXRow": true, "AXCell": true, "AXSlider": true, "AXTabGroup": true, "AXToolbar": true, "AXDisclosureTriangle": true,
	"AXIncrementor": true, "AXColorWell": true, "AXSplitter": true, "AXStaticText": true, "AXLink": true, "AXHeading": true,
}

// Writable says whether a focused element takes text: an editable one,
// or one Auralink cannot tell (no Accessibility answer, a web area, a
// group: contenteditable editors look so), but not nothing at all, nor a
// button, a list or another control that never does.
func Writable(f proto.FocusResult) bool {
	if f.Editable || f.Error != "" {
		return true
	}
	if f.Role == "" {
		return false
	}
	return !nonText[f.Role]
}

// Secure says whether the focus is in a password field: nothing is typed
// there.
func Secure(f proto.FocusResult) bool {
	return f.Subrole == "AXSecureTextField" || f.Role == "AXSecureTextField"
}

// Insert pastes r.Text where the focus is when it can (see the package
// comment). Call it off the main thread: it waits for the helper.
func Insert(ctx context.Context, io TextIO, r Request) Result {
	start := time.Now()
	res := insert(ctx, io, r)
	res.Elapsed = time.Since(start)
	return res
}

// Check tells, without pasting or activating anything, whether Insert
// would paste now (Result.Reason is "" when it would). Sessions ask it
// before a long answer, to stream it into the answer card at once when
// it cannot be inserted. With Auralink in front it says ReasonNotFront:
// what has the focus in Target is known only once it is activated.
func Check(ctx context.Context, io TextIO, r Request) Result {
	start := time.Now()
	r.Activate = nil
	res := check(ctx, io, r)
	res.Elapsed = time.Since(start)
	return res
}

func insert(ctx context.Context, io TextIO, r Request) Result {
	res := check(ctx, io, r)
	if res.Reason != ReasonNone {
		return res
	}
	press := r.Press
	if res.Elsewhere(r.Target) {
		press = nil // the auto key is the starting app's group's
	}
	p, err := io.Paste(ctx, proto.PasteParams{
		Text: r.Text, KeepClipboard: r.KeepClipboard, RestoreDelayMS: r.RestoreDelayMS,
		TargetPID: res.Into.PID, Press: press, PressDelayMS: r.PressDelayMS,
	})
	if err != nil {
		return failed(res, err)
	}
	res.Paste = p
	if !p.Posted {
		res.Reason = ReasonNotFront
		if p.Front != nil {
			res.Front = *p.Front
		}
		return res
	}
	res.Inserted = true
	return res
}

// check is Insert up to the paste.
func check(ctx context.Context, io TextIO, r Request) Result {
	var res Result
	if r.Text == "" {
		res.Reason = ReasonEmpty
		return res
	}
	focus, err := io.Focus(ctx)
	if err != nil {
		return failed(res, err)
	}
	if r.Self != 0 && focus.PID == r.Self {
		// Auralink has the focus: back to the app the session started in.
		res.Focus = focus
		if r.Target.PID == 0 || r.Target.PID == r.Self {
			res.Reason = ReasonNoTarget
			return res
		}
		if r.Activate == nil || !r.Activate(r.Target.PID) {
			res.Reason, res.Into = ReasonNotFront, r.Target
			return res
		}
		res.Activated = true
		if focus, err = io.Focus(ctx); err != nil {
			return failed(res, err)
		}
		if focus.PID != r.Target.PID {
			res.Focus, res.Reason, res.Into = focus, ReasonNotFront, proto.App{PID: focus.PID}
			return res
		}
	}
	res.Focus = focus
	same := r.Target.PID != 0 && focus.PID == r.Target.PID
	if focus.PID == 0 {
		res.Reason = ReasonNotEditable // nothing in front
		return res
	}
	if same {
		res.Into = r.Target
	} else {
		res.Into = proto.App{PID: focus.PID}
		if front, err := io.Frontmost(ctx); err == nil && front.PID == focus.PID {
			res.Into = front
		} else if err == nil && front.PID != 0 {
			// The focus is in a panel of an app that is not in front (a
			// launcher's): the helper would post nothing.
			res.Reason, res.Front = ReasonNotFront, front
			return res
		}
	}
	switch {
	case Secure(focus):
		res.Reason = ReasonSecure
	case r.NeedEditable && !focus.Editable:
		res.Reason = ReasonNotEditable
	case same && r.AnyEditable:
	case !Writable(focus):
		res.Reason = ReasonNotEditable
	case !same && !r.AnyEditable && focus.Error != "":
		// Another app, and Accessibility cannot tell what has the focus
		// (an app without an accessibility tree, the desktop): the card,
		// where Insert is the user's call.
		res.Reason = ReasonNotEditable
		if focus.Error == proto.FocusNoAccess {
			res.Reason = ReasonNoAccess
		}
	}
	return res
}

// failed records a helper error: Accessibility refused, or another one.
func failed(res Result, err error) Result {
	res.Err = err
	res.Reason = ReasonFailed
	var pe *proto.Error
	if errors.As(err, &pe) && pe.Code == proto.CodePermissionDenied {
		res.Reason = ReasonNoAccess
	}
	return res
}
