package settings

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestDefaultsAreNormal(t *testing.T) {
	d := Defaults()
	if !reflect.DeepEqual(normalize(clone(d)), d) {
		t.Fatalf("normalize changes the defaults:\n%+v\n%+v", normalize(d), d)
	}
	f := d.Features
	if f.Transcribe.Hotkeys[0].Keys != "Fn" || f.Translate.Hotkeys[0].Keys != "Fn+Shift" || f.Rewrite.Hotkeys[0].Keys != "Fn+Control" || f.PasteLast.Keys != "Control+Command+V" {
		t.Errorf("default shortcuts (plan D2): %+v", f)
	}
}

func TestMigrate(t *testing.T) {
	s, err := Migrate(nil)
	if err != nil || !reflect.DeepEqual(s, Defaults()) {
		t.Fatalf("empty: %+v %v", s, err)
	}
	// A partial document keeps what it has and gains the defaults.
	s, err = Migrate([]byte(`{"version":1,"general":{"language":"zh-Hans","menuBar":false},"privacy":{"keepHistory":false},"unknown":1}`))
	if err != nil || s.General.Language != "zh-Hans" || s.General.MenuBar || s.Privacy.KeepHistory || s.Appearance.Scale != 1 ||
		s.Appearance.Theme != "system" || len(s.Features.Transcribe.Hotkeys) != 1 {
		t.Fatalf("partial: %+v %v", s, err)
	}
	data, _ := json.Marshal(s)
	again, err := Migrate(data)
	if err != nil || !reflect.DeepEqual(again, s) {
		t.Fatalf("round trip: %+v %v", again, err)
	}
	if _, err := Migrate([]byte(`{`)); err == nil {
		t.Error("bad JSON accepted")
	}
}

// docs is a Documents in memory that can fail.
type docs struct {
	memDocs
	fail error
}

func (d *docs) Put(ctx context.Context, key string, doc []byte) error {
	if d.fail != nil {
		return d.fail
	}
	return d.memDocs.Put(ctx, key, doc)
}

func TestStore(t *testing.T) {
	ctx := context.Background()
	d := &docs{}
	var events []Changed
	s, err := Open(ctx, d, func(c Changed) { events = append(events, c) })
	if err != nil || !reflect.DeepEqual(s.Get(), Defaults()) {
		t.Fatalf("fresh: %+v %v", s.Get(), err)
	}
	if err := s.Update(ctx, func(s *Settings) {
		s.General.Language = "zh-Hans"
		s.Appearance.Scale = 1.12
		s.Appearance.Theme = "sepia"
		s.Appearance.SidebarWidth = 900
		s.General.OtherLanguages = []string{" en ", "en", ""}
		s.Features.Translate.Hotkeys = append(s.Features.Translate.Hotkeys, Hotkey{Keys: " "}, Hotkey{Keys: "Option+T", Trigger: "weird"})
		s.Overlay.Opacity = 0.1
	}); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.General.Language != "zh-Hans" || got.Appearance.Scale != 1.1 || got.Appearance.Theme != "system" ||
		got.Appearance.SidebarWidth != MaxSidebarWidth || !reflect.DeepEqual(got.General.OtherLanguages, []string{"en"}) ||
		len(got.Features.Translate.Hotkeys) != 2 || got.Features.Translate.Hotkeys[1].Trigger != TriggerTap || got.Overlay.Opacity != 0.5 {
		t.Fatalf("normalized: %+v", got)
	}
	if len(events) != 1 || events[0].Old.General.Language != "" || events[0].New.General.Language != "zh-Hans" {
		t.Fatalf("events %+v", events)
	}
	// A copy: changing it changes nothing.
	got.General.OtherLanguages[0] = "x"
	got.Features.Translate.Hotkeys[0].Keys = "x"
	if s.Get().General.OtherLanguages[0] != "en" || s.Get().Features.Translate.Hotkeys[0].Keys != "Fn+Shift" {
		t.Error("Get returned shared state")
	}
	// No change: nothing published.
	s.Update(ctx, func(s *Settings) { s.General.Language = "zh-Hans" })
	if len(events) != 1 {
		t.Errorf("no-op published: %d", len(events))
	}
	// Reopened from the documents.
	again, err := Open(ctx, d, nil)
	if err != nil || !reflect.DeepEqual(again.Get(), s.Get()) {
		t.Fatalf("reopened %+v %v", again.Get(), err)
	}
	// A failed save changes nothing.
	d.fail = context.DeadlineExceeded
	if err := s.Update(ctx, func(s *Settings) { s.General.Language = "en" }); err == nil || s.Get().General.Language != "zh-Hans" {
		t.Errorf("failed save: %v %q", err, s.Get().General.Language)
	}
	// A broken document: defaults, and an error to log.
	d.fail = nil
	d.memDocs.Put(ctx, Key, []byte("{"))
	broken, err := Open(ctx, d, nil)
	if err == nil || !reflect.DeepEqual(broken.Get(), Defaults()) {
		t.Errorf("broken: %+v %v", broken.Get(), err)
	}
}

func TestMemory(t *testing.T) {
	s := NewMemory()
	s.Update(context.Background(), func(s *Settings) { s.Audio.Sounds = false })
	if s.Get().Audio.Sounds {
		t.Error("not updated")
	}
}

func TestProvidersNormalized(t *testing.T) {
	s := NewMemory()
	err := s.Update(context.Background(), func(v *Settings) {
		v.Providers.ASR = []Provider{
			{ID: " doubao ", Preset: "doubao", Enabled: true, AppID: " 123 ", Model: " volc.seedasr.sauc.duration "},
			{ID: "doubao", Model: "second"},
			{ID: ""},
		}
		v.Providers.LLM = []Provider{{ID: "custom-2", Preset: "custom", Endpoint: " https://x.test/v1 "}}
		v.Features.Transcribe.ASR = " doubao "
	})
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	want := []Provider{{ID: "doubao", Enabled: true, AppID: "123", Model: "volc.seedasr.sauc.duration"}}
	if !reflect.DeepEqual(got.Providers.ASR, want) {
		t.Errorf("ASR %+v", got.Providers.ASR)
	}
	if l := got.Providers.LLM; len(l) != 1 || l[0].Preset != "custom" || l[0].Endpoint != "https://x.test/v1" {
		t.Errorf("LLM %+v", l)
	}
	if got.Features.Transcribe.ASR != "doubao" {
		t.Errorf("feature ASR %q", got.Features.Transcribe.ASR)
	}
}

// TestDeviceNames: names are kept for the listed devices only, and a
// copy never shares its map.
func TestDeviceNames(t *testing.T) {
	s := Defaults()
	s.Audio.Devices = []string{"a", " a ", "b"}
	s.Audio.DeviceNames = map[string]string{"a": "Mic A", "gone": "Old mic"}
	n := normalize(clone(s))
	if !reflect.DeepEqual(n.Audio.Devices, []string{"a", "b"}) || !reflect.DeepEqual(n.Audio.DeviceNames, map[string]string{"a": "Mic A"}) {
		t.Fatalf("normalized: %+v", n.Audio)
	}
	c := clone(n)
	c.Audio.DeviceNames["a"] = "changed"
	if n.Audio.DeviceNames["a"] != "Mic A" {
		t.Error("clone shares the names")
	}
	s.Audio.Devices, s.Audio.DeviceNames = nil, map[string]string{"x": "X"}
	if n := normalize(clone(s)); n.Audio.DeviceNames != nil {
		t.Errorf("names without devices: %v", n.Audio.DeviceNames)
	}
}
