package main

// The API keys' scenarios (the report of 10-29: a key saved in Auralink
// showed "No key saved" after a restart). Each reads the keys the way the
// app does, over a fake Keychain (memKeyring, fakeSecurity): nothing
// reaches the real one. A saved key is Auralink's own item; the family's
// key is read only when a service names it and this app agreed to use it.

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/credentials"
)

const dashVendor = "dashscope"

// setService sets up the service id of kind with credentialRef ref (""
// for none): its own key is whatever the Keychain holds.
func setService(a *app, kind providers.Kind, id, ref string) {
	a.updateSettings(func(s *settings.Settings) {
		providers.Put(s, kind, settings.Provider{ID: id, Enabled: true, CredentialRef: ref})
	})
}

// restart is the app quitting and starting again over the same data and
// the same Keychain.
func restart(t *testing.T, a *app, dir string, keys keyring) *app {
	t.Helper()
	a.quit()
	a.lib.close()
	b, _, _ := newTestAppIn(t, dir)
	b.keys = keys
	b.openProviders()
	return b
}

// TestKeyOnlyAuralinkSurvivesRestart: with only Auralink installed, a key
// saved in the dialog is found after a restart (the report's case).
func TestKeyOnlyAuralinkSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	a, _, tt := newTestAppIn(t, dir)
	fakeTests(t, nil)
	a.openProviderDialog(providers.ASR, "aliyun")
	a.provDlg.key = "sk-dashscope-000000abcd"
	click(t, tt, "Save")
	if a.provDlg.open || a.keyInfos["asr.aliyun"].state != actnow.KeyOwn || a.keyMasks["asr.aliyun"] != "sk-…abcd" {
		t.Fatalf("after save: open %v, %+v", a.provDlg.open, a.keyInfos["asr.aliyun"])
	}
	if !strings.Contains(logText(a), "key asr.aliyun: read back from the Keychain: found") {
		t.Errorf("no read-back line:\n%s", logText(a))
	}
	b := restart(t, a, dir, a.keys)
	if info := b.keyInfos["asr.aliyun"]; info.state != actnow.KeyOwn || !b.hasKey("asr.aliyun") {
		t.Fatalf("after restart: %+v", info)
	}
	if strings.Contains(logText(b), "dashscope-000000abcd") {
		t.Errorf("the key reached the logs:\n%s", logText(b))
	}
}

// TestKeyShareCheckbox: with another ActNow app installed, the checkbox
// decides whether the key also goes to the family; unchecking it leaves
// the family and keeps Auralink's own key.
func TestKeyShareCheckbox(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	fx.Register(audioSmith)
	a, _, tt := newTestApp(t)
	drain := joinFamily(t, a)
	fakeTests(t, nil)
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	a.provDlg.key = "sk-deepseek-0000001234"
	click(t, tt, "Save")
	drain()
	if sec.item("deepseek") == "" || !slices.Equal(users(t, fx, "deepseek"), []string{testBundleID}) {
		t.Fatalf("checked: family %q, users %v", sec.item("deepseek"), users(t, fx, "deepseek"))
	}
	a.openProviderDialog(providers.LLM, "deepseek")
	drain()
	if a.provDlg.fam.ref == "" || a.keyInfos["llm.deepseek"].state != actnow.KeyOwn {
		t.Fatalf("reopened: %+v, %+v", a.provDlg.fam, a.keyInfos["llm.deepseek"])
	}
	a.provDlg.fam.share = false
	click(t, tt, "Save")
	drain()
	if p := llmSetup(a, "deepseek"); p.CredentialRef != "" || slices.Contains(users(t, fx, "deepseek"), testBundleID) {
		t.Errorf("unchecked: setup %+v, users %v", p, users(t, fx, "deepseek"))
	}
	if a.keys.(*memKeyring).m["llm.deepseek"] != "sk-deepseek-0000001234" || !a.hasKey("llm.deepseek") {
		t.Errorf("own key lost: %v", a.keys.(*memKeyring).m)
	}
}

// TestKeyFamilyUnconfirmed: a service that names AudioSmith's key, with
// no key of its own and no agreement, says so and offers it; nothing is
// read before the user agrees, and the dialog's "Use this key" makes the
// key the service's own.
func TestKeyFamilyUnconfirmed(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	fx.Register(audioSmith)
	if err := sec.store(fx, audioSmith).Save(t.Context(), dashVendor, "sk-smith-000000a1b2", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	a, _, tt := newTestApp(t)
	drain := joinFamily(t, a)
	setService(a, providers.ASR, "aliyun", actnow.Ref(dashVendor))
	a.loadKeyMasks([]string{"asr.aliyun"})
	drain()
	info := a.keyInfos["asr.aliyun"]
	if info.state != actnow.KeyUnconfirmed || info.owner != audioSmith || sec.readCount() != 0 {
		t.Fatalf("state %+v, reads %d", info, sec.readCount())
	}
	if got := a.keyCardText(info); got != "Saved in AudioSmith · click to use it" {
		t.Errorf("card: %q", got)
	}
	if !strings.Contains(logText(a), "key asr.aliyun in the ActNow family's key: not confirmed") {
		t.Errorf("no line:\n%s", logText(a))
	}
	a.openProviderDialog(providers.ASR, "aliyun")
	drain()
	if !a.offerShown() {
		t.Fatalf("no offer: %+v", a.provDlg.fam)
	}
	click(t, tt, "Use this key")
	drain()
	click(t, tt, "Save")
	drain()
	if a.keys.(*memKeyring).m["asr.aliyun"] != "sk-smith-000000a1b2" || !a.hasKey("asr.aliyun") {
		t.Errorf("own item after Use: %v", a.keys.(*memKeyring).m)
	}
	if p := providerSetup(a, providers.ASR, "aliyun"); p.CredentialRef != actnow.Ref(dashVendor) {
		t.Errorf("the setup keeps the family reference: %+v", p)
	}
}

// TestKeyFamilyReadFails: a family key that cannot be read is an error
// with its class, not "no key".
func TestKeyFamilyReadFails(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	fx.Register(audioSmith)
	cs := sec.store(fx, testBundleID)
	if err := cs.Save(t.Context(), dashVendor, "sk-mine-0000000a1b2", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	sec.fail = map[string]error{"ActNow/vendor." + dashVendor: errors.New("security: User interaction is not allowed. (-25308)")}
	a, _, _ := newTestApp(t)
	drain := joinFamily(t, a)
	setService(a, providers.ASR, "aliyun", actnow.Ref(dashVendor))
	a.loadKeyMasks([]string{"asr.aliyun"})
	drain()
	info := a.keyInfos["asr.aliyun"]
	if info.state != actnow.KeyFailed || a.hasKey("asr.aliyun") {
		t.Fatalf("state %+v", info)
	}
	if got := a.keyCardText(info); got != "Could not read the key · check Keychain access" {
		t.Errorf("card: %q", got)
	}
	if !strings.Contains(logText(a), "key asr.aliyun in the ActNow family's key: error (") {
		t.Errorf("no error line:\n%s", logText(a))
	}
	if p := providerSetup(a, providers.ASR, "aliyun"); p.CredentialRef == "" {
		t.Error("the family reference was dropped on a read error")
	}
}

// TestKeyOwnReadFails: Auralink's own item that cannot be read says so,
// and a family reference is not tried in its place.
func TestKeyOwnReadFails(t *testing.T) {
	a, _, _ := newTestApp(t)
	setService(a, providers.ASR, "aliyun", "")
	a.keys = failingKeyring{m: map[string]string{}, fail: map[string]error{
		"asr.aliyun": errors.New("security: User interaction is not allowed. (-25308)"),
	}}
	a.openProviders()
	if info := a.keyInfos["asr.aliyun"]; info.state != actnow.KeyFailed || info.family {
		t.Fatalf("state %+v", info)
	}
	if !strings.Contains(logText(a), "key asr.aliyun in the Keychain: error (-25308, user interaction not allowed)") {
		t.Errorf("no error line:\n%s", logText(a))
	}
}

// TestKeyFamilyItemDeleted: a family key deleted in another app (its
// metadata left behind) is "gone", and the service asks for the key again.
func TestKeyFamilyItemDeleted(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	cs := sec.store(fx, testBundleID)
	if err := cs.Save(t.Context(), dashVendor, "sk-mine-0000000a1b2", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	delete(sec.items, "ActNow/vendor."+dashVendor)
	a, _, _ := newTestApp(t)
	drain := joinFamily(t, a)
	setService(a, providers.ASR, "aliyun", actnow.Ref(dashVendor))
	a.loadKeyMasks([]string{"asr.aliyun"})
	drain()
	info := a.keyInfos["asr.aliyun"]
	if info.state != actnow.KeyGone || a.hasKey("asr.aliyun") {
		t.Fatalf("state %+v", info)
	}
	if got := a.keyCardText(info); got != "The shared key is gone · enter it again" {
		t.Errorf("card: %q", got)
	}
	if _, ok := cs.Offer(dashVendor); ok {
		t.Error("the metadata of the deleted key is still there")
	}
}

// TestKeyMigratesIntoOwnItem: the report's stored state (a family
// reference, no own key, the family's key agreed to) is copied into
// Auralink's own item at the start, and the reference goes.
func TestKeyMigratesIntoOwnItem(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	cs := sec.store(fx, testBundleID)
	if err := cs.Save(t.Context(), dashVendor, "sk-mine-0000000a1b2", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := newTestApp(t)
	drain := joinFamily(t, a)
	setService(a, providers.ASR, "aliyun", actnow.Ref(dashVendor))
	a.loadKeyMasks([]string{"asr.aliyun"})
	drain()
	if info := a.keyInfos["asr.aliyun"]; info.state != actnow.KeyOwn || info.mask != "sk-…a1b2" {
		t.Fatalf("after migration: %+v", info)
	}
	if a.keys.(*memKeyring).m["asr.aliyun"] != "sk-mine-0000000a1b2" {
		t.Errorf("own item: %v", a.keys.(*memKeyring).m)
	}
	if p := providerSetup(a, providers.ASR, "aliyun"); p.CredentialRef != "" {
		t.Errorf("reference kept: %+v", p)
	}
	if slices.Contains(users(t, fx, dashVendor), testBundleID) {
		t.Errorf("still a user of the family's key: %v", users(t, fx, dashVendor))
	}
	if !strings.Contains(logText(a), "key asr.aliyun: copied from the ActNow family's key into the Keychain") {
		t.Errorf("no migration line:\n%s", logText(a))
	}
	// Once migrated, a second start reads the own item and copies nothing.
	a.loadKeyMasks([]string{"asr.aliyun"})
	if n := strings.Count(logText(a), "copied from the ActNow family's key"); n != 1 {
		t.Errorf("migrated %d times", n)
	}
}

// TestKeyMigrationWaitsForAgreement: a family key this app has not agreed
// to stays a reference: nothing is copied, nothing is dropped.
func TestKeyMigrationWaitsForAgreement(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	if err := sec.store(fx, audioSmith).Save(t.Context(), dashVendor, "sk-smith-000000a1b2", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := newTestApp(t)
	drain := joinFamily(t, a)
	setService(a, providers.ASR, "aliyun", actnow.Ref(dashVendor))
	a.loadKeyMasks([]string{"asr.aliyun"})
	drain()
	if a.keyInfos["asr.aliyun"].state != actnow.KeyUnconfirmed || a.keys.(*memKeyring).m["asr.aliyun"] != "" {
		t.Errorf("copied before the agreement: %+v, %v", a.keyInfos["asr.aliyun"], a.keys.(*memKeyring).m)
	}
	if p := providerSetup(a, providers.ASR, "aliyun"); p.CredentialRef == "" {
		t.Error("the reference was dropped")
	}
}

// TestKeyFamilyUnknownUntilRead: before the family is read, a service
// that names a family key is not "missing": it is checked, and the page
// is not ready until the family has been read.
func TestKeyFamilyUnknownUntilRead(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	if err := sec.store(fx, testBundleID).Save(t.Context(), dashVendor, "sk-mine-0000000a1b2", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := newTestApp(t)
	a.famSettled = false
	setService(a, providers.ASR, "aliyun", actnow.Ref(dashVendor))
	a.loadKeyMasks([]string{"asr.aliyun"})
	if info := a.keyInfos["asr.aliyun"]; info.state != actnow.KeyUnknown || a.keysKnown {
		t.Fatalf("before the family: %+v, known %v", info, a.keysKnown)
	}
	drain := joinFamily(t, a)
	drain()
	if !a.famSettled || !a.keysKnown || a.keyInfos["asr.aliyun"].state == actnow.KeyUnknown {
		t.Errorf("not settled: settled %v, known %v, %+v", a.famSettled, a.keysKnown, a.keyInfos["asr.aliyun"])
	}
}

// TestKeyResidueOfRemovedApp: metadata of an app that was removed (its
// bundle in the users, its item still there) does not show as another
// app's key, and does not change Auralink's own key.
func TestKeyResidueOfRemovedApp(t *testing.T) {
	fx, sec := withFamilyKeys(t)
	if err := sec.store(fx, "dev.example.removed").Save(t.Context(), dashVendor, "sk-gone-0000000a1b2", credentials.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := newTestApp(t)
	drain := joinFamily(t, a)
	a.keys.Set("asr.aliyun", "", "sk-own-0000000wxyz")
	setService(a, providers.ASR, "aliyun", actnow.Ref(dashVendor))
	a.loadKeyMasks([]string{"asr.aliyun"})
	drain()
	if info := a.keyInfos["asr.aliyun"]; info.state != actnow.KeyOwn || a.keyMasks["asr.aliyun"] != "sk-…wxyz" {
		t.Fatalf("own item: %+v", info)
	}
	if got := a.installedUsers([]string{"dev.example.removed"}); len(got) != 0 {
		t.Errorf("a removed app counted as a user: %v", got)
	}
}

// TestKeyReadBackAfterSave: a saved key is read back, and the line says
// found or missing only.
func TestKeyReadBackAfterSave(t *testing.T) {
	sev, line := keyReadBackLine("asr.aliyun", "sk-x", nil)
	if sev != core.SeverityInfo || line != "key asr.aliyun: read back from the Keychain: found" {
		t.Errorf("found: %s %q", sev, line)
	}
	if _, line = keyReadBackLine("asr.aliyun", "", nil); line != "key asr.aliyun: read back from the Keychain: missing" {
		t.Errorf("missing: %q", line)
	}
}

// providerSetup is the saved setup of kind/id (zero when not set up).
func providerSetup(a *app, kind providers.Kind, id string) settings.Provider {
	p, _ := providers.Setup(a.prefs, kind, id)
	return p
}
