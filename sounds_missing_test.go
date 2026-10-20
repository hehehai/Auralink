package main

import (
	"slices"
	"testing"

	"github.com/hehehai/actnowkit/sounds"
)

// TestNoFamilySounds: a build without resources/sounds (from source,
// without scripts/sync-sounds.sh) has no catalogue, and its cues are
// macOS's system sounds: Pop and Tink by default, also for a family
// sound chosen in another build; the picker offers the system sounds,
// and Try plays them.
func TestNoFamilySounds(t *testing.T) {
	if cat := cueSoundsIn(t.TempDir()); cat != nil {
		t.Fatalf("a folder without sounds/ has a catalogue: %v", cat)
	}
	r := newSoundsRig(t)
	r.soundCat = nil
	r.cuePlayer = sounds.NewPlayer(nil, sounds.Silent{})
	for _, c := range []struct {
		cue  cue
		want string
	}{{startCue, "Pop"}, {stopCue, "Tink"}} {
		if got := r.cueTarget(c.cue); got.Kind != sounds.KindSystem || got.Name != c.want {
			t.Errorf("cue %v: %+v, want the system sound %s", c.cue, got, c.want)
		}
	}
	// A family sound kept from a build that had them.
	r.prefs.Audio.StartSound = "tally"
	if got := r.cueTarget(startCue); got.Kind != sounds.KindSystem || got.Name != "Pop" {
		t.Errorf("tally without the family's sounds: %+v", got)
	}
	r.prefs.Audio.StartSound = ""
	frames(r.tt)
	for _, s := range []string{"Pop", "Tink"} {
		if !has(r.tt, s) {
			t.Fatalf("no %q on Settings → Audio: %q", s, firstTexts(r.tt))
		}
	}
	if has(r.tt, "Tally") {
		t.Error("Tally shows without the family's sounds")
	}
	if !slices.Contains(r.systemSounds(), "Pop") {
		t.Errorf("system sounds: %q", r.systemSounds())
	}
	click(t, r.tt, "Try the start sound")
	frames(r.tt)
	if p := r.takePlays(); len(p) == 0 || p[0] != "Pop" {
		t.Errorf("Try played %q, want Pop", p)
	}
	// The picker, without a catalogue: the system sounds only.
	click(t, r.tt, "Pop")
	frames(r.tt)
	for _, s := range []string{"Basso", "Submarine"} {
		if !has(r.tt, s) {
			t.Errorf("no %q in the picker: %q", s, firstTexts(r.tt))
		}
	}
}
