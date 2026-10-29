package main

import (
	"strings"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/store"
)

func TestRefusalLine(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 42, 0, time.UTC)
	cur := session.Snapshot{State: session.Recording, ID: "01HXYZ", StartedAt: now.Add(-42 * time.Second)}
	if got := refusalLine(store.KindTranscribe, cur, now); got != "start transcribe refused: a session is recording (id 01HXYZ, started 42s ago)" {
		t.Errorf("got %q", got)
	}
	if got := refusalLine(store.KindTranslate, session.Snapshot{State: session.Reading}, now); !strings.Contains(got, "started unknown ago") {
		t.Errorf("no start time: %q", got)
	}
}

func TestShortcutLine(t *testing.T) {
	trig := proto.HotkeyEventParams{Binding: "transcribe/0", Phase: proto.PhaseTrigger}
	up := proto.HotkeyEventParams{Binding: "transcribe/0", Phase: proto.PhaseUp}
	cases := []struct {
		name string
		e    proto.HotkeyEventParams
		d    hotkey.Decision
		want string
	}{
		{"ignored while busy", trig, hotkey.Decision{}, "shortcut transcribe/0 ignored: a session is composing"},
		{"release is silent", up, hotkey.Decision{}, ""},
		{"start is silent here", trig, hotkey.Decision{Action: hotkey.Start}, ""},
		{"stop", trig, hotkey.Decision{Action: hotkey.Stop}, "shortcut transcribe/0: stop the recording"},
		{"paste", trig, hotkey.Decision{Action: hotkey.Paste}, "shortcut transcribe/0: paste the last result"},
	}
	for _, c := range cases {
		if got := shortcutLine(c.e, c.d, session.Composing); got != c.want {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}

// A start while a session records is refused: the manager's ErrBusy
// writes the warning with the running session's state, id and age.
func TestRefusedStartIsLogged(t *testing.T) {
	chat := &fakeChat{text: "unused"}
	set, keys, _ := fileSetup(t, "hello", chat)
	ev := script(t, hotkeyLine(0, "transcribe/0", proto.PhaseTrigger))
	a := newSessionApp(t, ev, set, keys)
	a.pump("the recording", func() bool { return a.sess.State == session.Recording })
	cur := a.mgr.Current()
	a.sess = session.Snapshot{} // the page's copy is behind: the manager refuses
	if id, n := a.startSessionID(session.StartRequest{Kind: store.KindTranscribe}); id != "" || n != nil {
		t.Fatalf("started %q %v", id, n)
	}
	want := "start transcribe refused: a session is recording (id " + cur.ID + ","
	var found bool
	for _, e := range a.logs.Tail(1000, sessionSource) {
		if e.Level == "warning" && strings.HasPrefix(e.Message, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("no refusal line; logs:\n%s", logText(a.app))
	}
	a.sess = a.mgr.Current()
	a.cancelSession()
}
