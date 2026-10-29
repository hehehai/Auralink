package main

import (
	"context"
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/conformance"
	"github.com/hehehai/actnowkit/credentials"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/registry"
)

// fakeSecurity is /usr/bin/security over a map, as actnowkit's Keychain
// runs it: the tests never reach a real Keychain. reads lists the items
// read, to tell that none was read before the user agreed.
type fakeSecurity struct {
	mu    sync.Mutex
	items map[string]string // service/account → secret
	reads []string
	fail  map[string]error // service/account → what a read fails with
}

var addRE = regexp.MustCompile(`-s "([^"]*)" -a "([^"]*)" -l "[^"]*" -X ([0-9a-f]+)`)

func (f *fakeSecurity) run(stdin string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	arg := func(flag string) string {
		if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
			return args[i+1]
		}
		return ""
	}
	switch args[0] {
	case "-i":
		m := addRE.FindStringSubmatch(stdin)
		if m == nil {
			return nil, nil, errors.New("fake security: " + stdin)
		}
		b, _ := hex.DecodeString(m[3])
		f.items[m[1]+"/"+m[2]] = string(b)
	case "find-generic-password":
		key := arg("-s") + "/" + arg("-a")
		if err := f.fail[key]; err != nil {
			return nil, nil, err
		}
		v, ok := f.items[key]
		if !ok {
			return nil, nil, credentials.ErrNotFound
		}
		f.reads = append(f.reads, key)
		return nil, []byte("password: 0x" + hex.EncodeToString([]byte(v)) + "  \"\"\n"), nil
	case "delete-generic-password":
		key := arg("-s") + "/" + arg("-a")
		if _, ok := f.items[key]; !ok {
			return nil, nil, credentials.ErrNotFound
		}
		delete(f.items, key)
	}
	return nil, nil, nil
}

// store is an app's access to the family's keys in fx, over f.
func (f *fakeSecurity) store(fx *conformance.Fixture, bundleID string) *credentials.Store {
	return &credentials.Store{Family: fx.Family, BundleID: bundleID, Keychain: credentials.Keychain{Run: f.run}}
}

func (f *fakeSecurity) item(vendor string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items["ActNow/vendor."+vendor]
}

func (f *fakeSecurity) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reads)
}

func users(t *testing.T, fx *conformance.Fixture, vendor string) []string {
	t.Helper()
	file, _, err := credentials.Read(fx.Family)
	if err != nil {
		t.Fatal(err)
	}
	return file.Vendors[vendor].Users
}

func llmSetup(a *app, id string) settings.Provider {
	p, _ := providers.Setup(a.settings.Get(), providers.LLM, id)
	return p
}

// TestFamilyKeySave: a new key is Auralink's own item and, with the box
// checked (the default), the family's too; sessions read the own item.
func TestFamilyKeySave(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	fx.Register(audioSmith)
	a, _, tt := newTestApp(t)
	drain := joinFamily(t, a)
	fakeTests(t, nil)
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	a.provDlg.key = "sk-deepseek-0000001234"
	frames(tt)
	if !a.provDlg.fam.share || !has(tt, "Let other ActNow apps on this Mac use this key") {
		t.Fatalf("no checked box: %+v %q", a.provDlg.fam, firstTexts(tt))
	}
	click(t, tt, "Save")
	drain()
	if sec.item("deepseek") != "sk-deepseek-0000001234" || a.keys.(*memKeyring).m["llm.deepseek"] != "sk-deepseek-0000001234" {
		t.Errorf("shared %q, own %v", sec.item("deepseek"), a.keys.(*memKeyring).m)
	}
	if p := llmSetup(a, "deepseek"); p.CredentialRef != "actnow:vendor.deepseek" {
		t.Errorf("credentialRef %q", p.CredentialRef)
	}
	if u := users(t, fx, "deepseek"); !slices.Equal(u, []string{testBundleID}) {
		t.Errorf("users %v", u)
	}
	if m := a.keyMasks["llm.deepseek"]; m != "sk-…1234" {
		t.Errorf("mask %q", m)
	}
	if v, _ := a.keySource().Get("llm.deepseek"); v != "sk-deepseek-0000001234" {
		t.Errorf("the session's key %q", v)
	}
	// Unchecked, a key stays Auralink's own (Alibaba's speech service).
	a.openProviderDialog(providers.ASR, "aliyun")
	drain()
	a.provDlg.key = "sk-dashscope-000000abcd"
	a.provDlg.fam.share = false
	click(t, tt, "Save")
	drain()
	if a.keys.(*memKeyring).m["asr.aliyun"] != "sk-dashscope-000000abcd" || sec.item("dashscope") != "" {
		t.Errorf("own %v, shared %q", a.keys.(*memKeyring).m, sec.item("dashscope"))
	}
}

// TestFamilyKeyAlone: with no other ActNow app installed, the dialog has
// no sharing checkbox (user 10-17), and a new key is not shared: it is
// Auralink's own, and no family item or metadata is left behind.
func TestFamilyKeyAlone(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	a, _, tt := newTestApp(t)
	drain := joinFamily(t, a)
	fakeTests(t, nil)
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	a.provDlg.key = "sk-deepseek-0000001234"
	frames(tt)
	if has(tt, "Let other ActNow apps on this Mac use this key") {
		t.Fatalf("a sharing checkbox without another ActNow app: %q", firstTexts(tt))
	}
	click(t, tt, "Save")
	drain()
	if sec.item("deepseek") != "" || len(users(t, fx, "deepseek")) != 0 || a.keys.(*memKeyring).m["llm.deepseek"] != "sk-deepseek-0000001234" {
		t.Errorf("shared %q, users %v, own %v", sec.item("deepseek"), users(t, fx, "deepseek"), a.keys.(*memKeyring).m)
	}
}

// TestFamilyKeyOffer: AudioSmith's DeepSeek key is offered, not read,
// until "Use this key" (spec §8.3, §16.1 scenario 6); the connection test
// before it reads nothing either.
func TestFamilyKeyOffer(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	fx.Register(audioSmith)
	smith := sec.store(fx, audioSmith)
	if err := smith.Save(context.Background(), "deepseek", "sk-smith-000000a1b2", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	a, _, tt := newTestApp(t)
	drain := joinFamily(t, a)
	seen := fakeTests(t, nil)
	a.navigate("/providers")
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	frames(tt)
	if !has(tt, "Use the DeepSeek key from AudioSmith (sk-…a1b2)") {
		t.Fatalf("no offer: %q", tt.Texts())
	}
	click(t, tt, "Test connection")
	drain()
	if sec.readCount() != 0 || len(*seen) != 1 || (*seen)[0] != "llm.deepseek=" {
		t.Fatalf("read before the user agreed: %v, tests %v", sec.reads, *seen)
	}
	click(t, tt, "Use this key")
	drain()
	if sec.readCount() != 1 || a.provDlg.fam.ref != "actnow:vendor.deepseek" {
		t.Fatalf("reads %v, dialog %+v", sec.reads, a.provDlg.fam)
	}
	click(t, tt, "Test connection")
	drain()
	if (*seen)[1] != "llm.deepseek=sk-smith-000000a1b2" {
		t.Errorf("the test after Use: %v", *seen)
	}
	click(t, tt, "Save")
	drain()
	if p := llmSetup(a, "deepseek"); p.CredentialRef != "actnow:vendor.deepseek" || a.keyMasks["llm.deepseek"] != "sk-…a1b2" {
		t.Errorf("setup %+v, mask %q", p, a.keyMasks["llm.deepseek"])
	}
	if u := users(t, fx, "deepseek"); !slices.Contains(u, testBundleID) || !slices.Contains(u, audioSmith) {
		t.Errorf("users %v", u)
	}
	// Replacing it says it changes AudioSmith's too, and does.
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	a.provDlg.key = "sk-new-000000000c3d4"
	frames(tt)
	if !has(tt, "Also updates it in AudioSmith.") {
		t.Errorf("no note: %q", tt.Texts())
	}
	click(t, tt, "Save")
	drain()
	if v, err := smith.Secret(context.Background(), "deepseek"); err != nil || v != "sk-new-000000000c3d4" {
		t.Errorf("AudioSmith reads %q %v", v, err)
	}
	// Removing it asks; "Stop using it here" leaves it to AudioSmith.
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	click(t, tt, "Remove the saved key")
	click(t, tt, "Save")
	if !has(tt, "AudioSmith also uses this key.") {
		t.Fatalf("no question: %q", tt.Texts())
	}
	click(t, tt, "Stop using it here")
	drain()
	if p := llmSetup(a, "deepseek"); p.CredentialRef != "" || sec.item("deepseek") == "" || slices.Contains(users(t, fx, "deepseek"), testBundleID) {
		t.Errorf("after Stop using: %+v, item %q, users %v", p, sec.item("deepseek"), users(t, fx, "deepseek"))
	}
}

// TestFamilyKeyCancelAndDelete: "Use this key" then Cancel leaves the key;
// "Delete for all apps" deletes it.
func TestFamilyKeyCancelAndDelete(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	fx.Register(audioSmith)
	smith := sec.store(fx, audioSmith)
	if err := smith.Save(context.Background(), "stepfun", "sk-step-0000000009f0", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	a, _, tt := newTestApp(t)
	drain := joinFamily(t, a)
	a.openProviderDialog(providers.LLM, "stepfun")
	drain()
	click(t, tt, "Use this key")
	drain()
	click(t, tt, "Cancel")
	frames(tt)
	drain()
	if slices.Contains(users(t, fx, "stepfun"), testBundleID) {
		t.Fatalf("still a user after Cancel: %v", users(t, fx, "stepfun"))
	}
	// Both StepFun services take it; deleting it for all apps from one.
	for _, kind := range []providers.Kind{providers.ASR, providers.LLM} {
		a.openProviderDialog(kind, "stepfun")
		drain()
		click(t, tt, "Use this key")
		drain()
		click(t, tt, "Save")
		drain()
	}
	if p, _ := providers.Setup(a.settings.Get(), providers.ASR, "stepfun"); p.CredentialRef != "actnow:vendor.stepfun" {
		t.Fatalf("speech service %+v", p)
	}
	a.openProviderDialog(providers.LLM, "stepfun")
	drain()
	click(t, tt, "Remove the saved key")
	click(t, tt, "Save")
	click(t, tt, "Delete for all apps")
	drain()
	if sec.item("stepfun") != "" {
		t.Error("the key is still there")
	}
	if _, ok := smith.Offer("stepfun"); ok {
		t.Error("the family still lists it")
	}
	// The speech service kept its own copy: Auralink's own item is the
	// authority, so the deletion for all apps does not take it away.
	a.sharedKeysChanged()
	drain()
	if !a.hasKey("asr.stepfun") || sec.item("stepfun") != "" {
		t.Error("the speech service lost its own key")
	}
}

// TestFamilyKeyLegacy: a key Auralink saved before the family is not
// copied until the user checks the box (spec §15.2 step 6); then the
// shared item holds it, read back, and the old one stays.
func TestFamilyKeyLegacy(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	fx.Register(audioSmith)
	a, _, tt := newTestApp(t)
	a.keys.Set("llm.deepseek", "", "sk-old-00000000beef")
	a.updateSettings(func(s *settings.Settings) {
		providers.Put(s, providers.LLM, settings.Provider{ID: "deepseek", Enabled: true})
	})
	drain := joinFamily(t, a)
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	frames(tt)
	if a.provDlg.fam.share || !has(tt, "Let other ActNow apps on this Mac use this key") {
		t.Fatalf("a saved key is shared by default: %+v", a.provDlg.fam)
	}
	click(t, tt, "Save")
	drain()
	if sec.item("deepseek") != "" || llmSetup(a, "deepseek").CredentialRef != "" {
		t.Fatal("copied without being asked")
	}
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	a.provDlg.fam.share = true
	click(t, tt, "Save")
	drain()
	if sec.item("deepseek") != "sk-old-00000000beef" || a.keys.(*memKeyring).m["llm.deepseek"] != "sk-old-00000000beef" {
		t.Errorf("shared %q, own %v", sec.item("deepseek"), a.keys.(*memKeyring).m)
	}
	if llmSetup(a, "deepseek").CredentialRef == "" || !slices.Equal(users(t, fx, "deepseek"), []string{testBundleID}) {
		t.Errorf("setup %+v", llmSetup(a, "deepseek"))
	}
	// Unchecked again: the key becomes the service's own; the family's
	// item, which no other app uses, stays until deleted there.
	a.keys.Set("llm.deepseek", "", "")
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	a.provDlg.fam.share = false
	click(t, tt, "Save")
	drain()
	if llmSetup(a, "deepseek").CredentialRef != "" || a.keys.(*memKeyring).m["llm.deepseek"] != "sk-old-00000000beef" {
		t.Errorf("unshared: %+v %v", llmSetup(a, "deepseek"), a.keys.(*memKeyring).m)
	}
	if slices.Contains(users(t, fx, "deepseek"), testBundleID) {
		t.Error("still a user")
	}
}

// TestFamilyKeyDifferent: Auralink's own key and another app's differ:
// the dialog says so (spec §15.2 step 6) and offers theirs.
func TestFamilyKeyDifferent(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	fx.Register(audioSmith)
	if err := sec.store(fx, audioSmith).Save(context.Background(), "deepseek", "sk-smith-000000a1b2", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	a, _, tt := newTestApp(t)
	a.keys.Set("llm.deepseek", "", "sk-mine-00000000ffff")
	a.updateSettings(func(s *settings.Settings) {
		providers.Put(s, providers.LLM, settings.Provider{ID: "deepseek", Enabled: true})
	})
	drain := joinFamily(t, a)
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	frames(tt)
	if !has(tt, "AudioSmith has a different DeepSeek key (sk-…a1b2).") || has(tt, "Use the DeepSeek key from AudioSmith (sk-…a1b2)") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if n := sec.readCount(); n != 0 {
		t.Errorf("%d reads of the family's key", n)
	}
}

// TestFamilyKeyErase: "Erase everything" with the keys deletes the family
// keys only Auralink uses and leaves those another app uses.
func TestFamilyKeyErase(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	fx.Register(audioSmith)
	smith := sec.store(fx, audioSmith)
	ctx := context.Background()
	if err := smith.Save(ctx, "deepseek", "sk-smith-000000a1b2", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := newTestApp(t)
	drain := joinFamily(t, a)
	mine := sec.store(fx, testBundleID)
	if _, err := mine.Use(ctx, "deepseek"); err != nil {
		t.Fatal(err)
	}
	if err := mine.Save(ctx, "openai", "sk-proj-0000000000aa", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	a.updateSettings(func(s *settings.Settings) {
		providers.Put(s, providers.LLM, settings.Provider{ID: "deepseek", Enabled: true, CredentialRef: actnow.Ref("deepseek")})
		providers.Put(s, providers.LLM, settings.Provider{ID: "openai", Enabled: true, CredentialRef: actnow.Ref("openai")})
	})
	sec.reads = nil
	eraseSharedKeys(a.famCreds.Load(), a.sharedVendors())
	drain()
	if sec.item("deepseek") == "" || slices.Contains(users(t, fx, "deepseek"), testBundleID) {
		t.Errorf("deepseek: %q %v", sec.item("deepseek"), users(t, fx, "deepseek"))
	}
	if sec.item("openai") != "" {
		t.Error("openai is still there")
	}
	if strings.Contains(strings.Join(sec.reads, " "), "deepseek") {
		t.Errorf("erasing read a key: %v", sec.reads)
	}
}

// familyKeyShots are the service dialog's parts about the family's keys
// (TestSnapshotsProviders, last): AudioSmith's key offered; a key typed,
// with the box that shares it; removing a key AudioSmith also uses.
func familyKeyShots(t *testing.T, a *app, save func(string)) {
	a.fam = &familyLink{self: actnow.Self{BundleID: testBundleID}}
	a.famCreds.Store(&credentials.Store{Family: family.At(filepath.Join(testDir(t), "ActNow")), BundleID: testBundleID})
	a.famFound = []registry.Found{{BundleID: audioSmith, App: family.Apps[0], Variant: family.Release, Name: "AudioSmith", Installed: true}}
	offer := credentials.Entry{AddedBy: audioSmith, Hint: "sk-…a1b2", Users: []string{audioSmith}}
	a.provDlg = providerDialog{}
	a.openProviderDialog(providers.LLM, "moonshot")
	a.provDlg.fam.vendor, a.provDlg.fam.offer = "moonshot", &offer
	save("providers-dialog-family-offer")
	a.provDlg.fam.other, a.provDlg.key, a.provDlg.fam.share = true, "sk-typed-000000000", true
	save("providers-dialog-family-share")
	a.provDlg = providerDialog{}
	a.updateSettings(func(s *settings.Settings) {
		providers.Put(s, providers.LLM, settings.Provider{ID: "deepseek", Enabled: true, CredentialRef: actnow.Ref("deepseek")})
	})
	a.keyMasks["llm.deepseek"] = "sk-…a1b2"
	a.openProviderDialog(providers.LLM, "deepseek")
	a.provDlg.fam.vendor, a.provDlg.fam.users = "deepseek", []string{"AudioSmith"}
	a.provDlg.removeKey, a.provDlg.fam.askRemove = true, true
	save("providers-dialog-family-remove")
	a.provDlg = providerDialog{}
	a.fam, a.famFound = nil, nil
	a.famCreds.Store(nil)
}
