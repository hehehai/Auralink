package macx

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// TestActivate: off macOS nothing happens; on a Mac (AURALINK_MAC=1) the
// app in front is reactivated at once.
func TestActivate(t *testing.T) {
	run := func(fn func()) { fn() }
	if runtime.GOOS != "darwin" {
		if ok, _ := Activate(1, time.Millisecond, run); ok || Frontmost() != (App{}) {
			t.Error("activated off macOS")
		}
		return
	}
	if os.Getenv("AURALINK_MAC") == "" {
		t.Skip("set AURALINK_MAC=1 on a Mac")
	}
	front := Frontmost()
	t.Logf("front: %+v", front)
	if front.PID == 0 {
		t.Skip("no app in front (no window server?)")
	}
	if ok, d := Activate(front.PID, 500*time.Millisecond, run); !ok {
		t.Errorf("could not activate the app in front (%s)", d)
	}
	if ok, _ := Activate(0, time.Millisecond, run); ok {
		t.Error("activated pid 0")
	}
}

// TestPlay: off macOS a no-op; on a Mac (AURALINK_MAC=1, it is heard)
// both cues start, Stop cuts a sound short and PlayWait waits for the end.
func TestPlay(t *testing.T) {
	if runtime.GOOS != "darwin" {
		if err := Play(SoundStart); err != nil {
			t.Fatal(err)
		}
		if err := PlayWait(SoundStart); err != nil {
			t.Fatal(err)
		}
		p, err := Start(SoundStart)
		if err != nil {
			t.Fatal(err)
		}
		p.Stop()
		<-p.Done()
		return
	}
	if os.Getenv("AURALINK_MAC") == "" {
		t.Skip("set AURALINK_MAC=1 on a Mac")
	}
	for _, s := range []Sound{SoundStart, SoundStop} {
		if err := Play(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	// PlayWait returns when the file ended: Pop is 1.63 s long.
	t0 := time.Now()
	if err := PlayWait(SoundStart); err != nil || time.Since(t0) < 500*time.Millisecond || time.Since(t0) > 3*time.Second {
		t.Errorf("PlayWait: %v after %v", err, time.Since(t0))
	}
	// Stop ends it at once.
	p, err := Start("Submarine")
	if err != nil {
		t.Fatal(err)
	}
	t0 = time.Now()
	p.Stop()
	select {
	case <-p.Done():
		if d := time.Since(t0); d > 500*time.Millisecond {
			t.Errorf("stopped after %v", d)
		}
	case <-time.After(2 * time.Second):
		t.Error("not stopped")
	}
	p.Stop() // again: nothing
}

// TestSoundFiles: on a Mac every preset's sounds are there (nothing is
// played).
func TestSoundFiles(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS's sounds")
	}
	for _, p := range Presets() {
		for _, s := range []Sound{p.Start, p.Stop} {
			if _, err := os.Stat("/System/Library/Sounds/" + string(s) + ".aiff"); err != nil {
				t.Errorf("%s: %v", p.ID, err)
			}
		}
	}
}

// TestPresets: Voxt's ten presets, in its order, Soft for an empty or
// unknown ID, and every sound's audible length known.
func TestPresets(t *testing.T) {
	want := []Preset{
		{"soft", "Pop", "Tink"}, {"glass", "Ping", "Ping"}, {"funk", "Morse", "Morse"},
		{"submarine", "Submarine", "Submarine"}, {"basso", "Basso", "Basso"}, {"bottle", "Bottle", "Bottle"},
		{"frog", "Frog", "Frog"}, {"hero", "Hero", "Hero"}, {"purr", "Purr", "Purr"}, {"sosumi", "Sosumi", "Sosumi"},
	}
	got := Presets()
	if len(got) != len(want) {
		t.Fatalf("%d presets", len(got))
	}
	for i, p := range want {
		if got[i] != p {
			t.Errorf("preset %d: %+v, want %+v", i, got[i], p)
		}
		if PresetFor(p.ID) != p {
			t.Errorf("PresetFor(%q): %+v", p.ID, PresetFor(p.ID))
		}
		for _, s := range []Sound{p.Start, p.Stop} {
			if d := s.Audible(); d <= 0 || d > time.Second {
				t.Errorf("%s: audible %v", s, d)
			}
		}
	}
	got[0].Start = "Basso" // a copy
	if Presets()[0].Start != SoundStart {
		t.Error("Presets shares its table")
	}
	for _, id := range []string{"", "Soft", "glas", "unknown"} {
		if p := PresetFor(id); p.ID != DefaultPreset || p.Start != SoundStart || p.Stop != SoundStop {
			t.Errorf("PresetFor(%q): %+v", id, p)
		}
	}
	if d := Sound("Blow").Audible(); d != 0 {
		t.Errorf("an unknown sound: %v", d)
	}
}
