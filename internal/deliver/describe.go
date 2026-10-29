package deliver

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/hehehai/Auralink/internal/helper/proto"
)

// reasonText says a reason in the log, in a few words.
var reasonText = map[Reason]string{
	ReasonEmpty:       "empty",
	ReasonNoTarget:    "no start app",
	ReasonNotFront:    "not in front",
	ReasonNotEditable: "no text field",
	ReasonSecure:      "password field",
	ReasonNoAccess:    "no accessibility",
	ReasonFailed:      "failed",
	ReasonCard:        "card choice",
	ReasonLong:        "too long",
	ReasonNotLanded:   "not confirmed",
}

// Describe says in one line, for the log, what a delivery found and did:
// the focused element's role, whether it is editable, its app and its
// subrole (a password field), and the outcome. It never has the text.
// Check results read "would insert" when nothing blocks the paste.
func (r Result) Describe(target proto.App) string {
	f := r.Focus
	var b strings.Builder
	switch {
	case f.PID == 0 && f.Role == "" && f.Error == "" && f.App == nil:
		b.WriteString("focus not read")
	case f.Error != "":
		fmt.Fprintf(&b, "focus unknown (%s) app=%s", f.Error, bundleOf(focusApp(f, target)))
	default:
		fmt.Fprintf(&b, "focus role=%s editable=%t app=%s", cmp.Or(f.Role, "-"), f.Editable, bundleOf(focusApp(f, target)))
		if f.Subrole != "" {
			fmt.Fprintf(&b, " subrole=%s", f.Subrole)
		}
	}
	b.WriteString(" -> ")
	if r.Pasted && f.Error != "" {
		b.WriteString("tried paste, ")
	}
	confirmed := r.Landed == proto.LandedYes
	switch {
	case r.Inserted && r.Moved(target) && confirmed:
		fmt.Fprintf(&b, "inserted into %s (the user switched apps)", bundleOf(r.To))
	case r.Inserted && r.Moved(target):
		fmt.Fprintf(&b, "pasted into %s, not confirmed (the user switched apps)", bundleOf(r.To))
	case r.Inserted && confirmed:
		b.WriteString("inserted")
	case r.Inserted:
		b.WriteString("pasted, not confirmed")
	case r.Reason == ReasonNotLanded && r.Paste.LeftOnClipboard:
		b.WriteString("card (not confirmed, text left on the clipboard)")
	case r.Reason == ReasonNotLanded:
		b.WriteString("card (not confirmed)")
	case r.Reason == ReasonNone:
		b.WriteString("would insert")
	case r.Reason == ReasonNotFront && r.Front.BundleID != "":
		fmt.Fprintf(&b, "card (not in front: %s)", r.Front.BundleID)
	case r.Reason == ReasonFailed:
		fmt.Fprintf(&b, "card (failed: %v)", r.Err)
	default:
		fmt.Fprintf(&b, "card (%s)", cmp.Or(reasonText[r.Reason], string(r.Reason)))
	}
	return b.String()
}

// bundleOf is an app's bundle id for the log ("-" when unknown).
func bundleOf(a proto.App) string { return cmp.Or(a.BundleID, "-") }
