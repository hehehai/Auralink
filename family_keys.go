package main

// API keys shared with the ActNow family (internal/actnow; spec §8). A
// service's own Keychain item (asr.<id>, llm.<id>) is the authority; the
// family's key of its vendor (the login Keychain's "ActNow" / "vendor.<id>",
// with credentials.json beside it) is a copy made when the user shares a key,
// or a key a service names (settings.Provider.CredentialRef) while it has no
// key of its own. A service's dialog offers the family's key without reading
// it, and reads it only once the user chose "Use this key" (credentials.Store.Use:
// not even a connection test reads it before). This file has the family's
// dialog state and the sharing helpers; reading is family_keys_read.go and
// providers_keys.go, saving family_keys_save.go, the dialog's parts
// family_keys_view.go.

import (
	"context"
	"log"
	"slices"
	"strings"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/actnowkit/credentials"
	"github.com/hehehai/actnowkit/family"
)

// sharedKeysChanged answers a change of the family's keys (another app
// saved, replaced or deleted one): the masks of the services on family
// keys are read again, and the dialog's offer.
func (a *app) sharedKeysChanged() {
	var accounts []string
	for _, kind := range []providers.Kind{providers.ASR, providers.LLM} {
		for _, p := range providerList(a.prefs, kind) {
			if actnow.RefVendor(p) != "" {
				accounts = append(accounts, providers.Account(kind, p.ID))
			}
		}
	}
	a.loadKeyMasks(accounts)
	if a.provDlg.open {
		a.loadDialogFamily()
	}
}

// ---- The dialog ----

// dialogFamily is a service dialog's part about the family's keys.
type dialogFamily struct {
	vendor  string             // the family vendor of the service's key, "" for none
	offer   *credentials.Entry // the family's key of the vendor, when the dialog opened
	ownKey  bool               // the service has a key of its own saved
	using   bool               // this app agreed to use the family's key (the offer's users)
	differs bool               // … which is not the family's
	share   bool               // "Let other ActNow apps on this Mac use this key"
	ref     string             // the setup's credentialRef as the dialog has it
	secret  string             // the family's key, read after "Use this key" (the test's)
	joined  bool               // "Use this key" made Auralink a user (undone on Cancel)
	other   bool               // "Enter another key": the offer set aside
	// askRemove: the saved family key is to be removed while other apps
	// use it: the dialog asks how (spec §8.3).
	askRemove bool
	users     []string // the other apps that use the vendor's key, by name
}

// dialogVendor is the family vendor of the dialog's service, "" for none
// or outside the family.
func (a *app) dialogVendor() string {
	d := &a.provDlg
	if a.fam == nil {
		return ""
	}
	p := d.setup()
	v, ok := actnow.Vendor(providers.Account(d.kind, p.ID), p.Endpoint)
	if !ok {
		return ""
	}
	return v.ID
}

// loadDialogFamily reads what the family has of the dialog's vendor, in
// the background: its key's hint and users (never the key), and whether
// the service's own key is the same one.
func (a *app) loadDialogFamily() {
	d := &a.provDlg
	d.fam.vendor = a.dialogVendor()
	cs := a.famCreds.Load()
	if d.fam.vendor == "" || cs == nil {
		return
	}
	kind, id, vendor, own, ref := d.kind, d.id, d.fam.vendor, a.keys, d.fam.ref
	acc := providers.Account(kind, id)
	a.async(func() {
		e, offered := cs.Offer(vendor)
		users := a.installedUsers(cs.OtherUsers(vendor))
		e0, _ := cs.Offer(vendor)
		using := slices.Contains(e0.Users, cs.BundleID)
		mine, _ := own.Get(acc)
		differs := false
		if offered && mine != "" {
			_, same := cs.Compare(vendor, mine)
			differs = !same
		}
		a.post(func() {
			d := &a.provDlg
			if !d.open || d.kind != kind || d.id != id || d.fam.vendor != vendor {
				return
			}
			d.fam.offer = nil
			if offered {
				d.fam.offer = &e
			}
			d.fam.ownKey, d.fam.differs, d.fam.using = mine != "", differs, using
			d.fam.users = a.appNames(users)
			if ref == "" && !d.fam.joined {
				d.fam.share = mine == "" // a key saved before is shared only when asked
			}
		})
	})
}

// appNames names family apps by bundle id: the name the family found,
// else the family table's.
func (a *app) appNames(ids []string) []string {
	var out []string
	for _, id := range ids {
		name := ""
		for _, f := range a.famFound {
			if f.BundleID == id {
				name = f.Name
			}
		}
		if name == "" {
			if app, _, err := family.ParseBundleID(id); err == nil {
				name = app.Name
			} else {
				name = id
			}
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// offerShown reports whether the dialog offers the family's key in place
// of the key field (spec §8.3): the service has no key of its own, the
// family has one, and the service either names none yet or names one this
// app has not agreed to use (its key is shown otherwise); the user did not
// set the offer aside or type a key.
func (a *app) offerShown() bool {
	f := a.provDlg.fam
	return f.offer != nil && (f.ref == "" || !f.using) && !f.joined && !f.ownKey && !f.other && strings.TrimSpace(a.provDlg.key) == ""
}

// installedUsers keeps the family apps that are installed among ids: a
// user left behind by an app that was removed does not count (its
// metadata may stay in credentials.json).
func (a *app) installedUsers(ids []string) []string {
	var out []string
	for _, id := range ids {
		for _, f := range a.famFound {
			if f.BundleID == id && f.Installed {
				out = append(out, id)
			}
		}
	}
	return out
}

// useSharedKey is "Use this key": Auralink joins the key's users and
// reads it (for the test before saving); the service uses it once saved.
func (a *app) useSharedKey() {
	d := &a.provDlg
	cs, vendor, kind, id := a.famCreds.Load(), d.fam.vendor, d.kind, d.id
	if cs == nil || vendor == "" {
		return
	}
	wasUsing := d.fam.offer != nil && slices.Contains(d.fam.offer.Users, cs.BundleID)
	a.async(func() {
		key, err := cs.Use(context.Background(), vendor)
		a.post(func() {
			d := &a.provDlg
			if !d.open || d.kind != kind || d.id != id {
				return
			}
			if err != nil {
				log.Printf("family: using the shared %s key: %v", vendor, err)
				d.fam.offer = nil
				d.err, d.errMore = sharedKeyError(), err.Error()
				return
			}
			d.fam.ref, d.fam.secret, d.fam.share = actnow.Ref(vendor), key, true
			d.fam.joined = d.fam.joined || !wasUsing
			d.key, d.removeKey = "", false
			if e := d.fam.offer; e != nil && e.AppID != "" && d.appID == "" {
				d.appID = e.AppID // Doubao speech: its access token's App ID
			}
		})
	})
}

// dialogClosed undoes "Use this key" when the dialog closes unsaved.
func (a *app) dialogClosed(f dialogFamily) {
	cs := a.famCreds.Load()
	if !f.joined || cs == nil || a.vendorInUse(f.vendor, "") {
		return
	}
	a.async(func() {
		if err := cs.StopUsing(context.Background(), f.vendor); err != nil {
			log.Printf("family: %v", err)
		}
	})
}

// vendorInUse reports whether a saved service other than account uses the
// family's key of vendor (DashScope's speech and language services share
// one).
func (a *app) vendorInUse(vendor, account string) bool {
	for _, kind := range []providers.Kind{providers.ASR, providers.LLM} {
		for _, p := range providerList(a.prefs, kind) {
			if actnow.RefVendor(p) == vendor && providers.Account(kind, p.ID) != account {
				return true
			}
		}
	}
	return false
}

// sharedVendors are the family keys the services set up use.
func (a *app) sharedVendors() []string {
	var out []string
	for _, kind := range []providers.Kind{providers.ASR, providers.LLM} {
		for _, p := range providerList(a.prefs, kind) {
			if v := actnow.RefVendor(p); v != "" && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}
