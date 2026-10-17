package main

// API keys shared with the ActNow family (internal/actnow; spec §8). A
// service's key may be the family's key of its vendor (the login
// Keychain's "ActNow" / "vendor.<id>", with credentials.json beside it),
// which the service's setup then names (settings.Provider.CredentialRef).
// A service's dialog offers the family's key without reading it, and
// reads it only once the user chose "Use this key" (credentials.Store.Use:
// not even a connection test reads it before); a key the user saves there
// goes to the family when "Let other ActNow apps on this Mac use this key"
// stays checked. The dialog's parts are providers_dialog.go's.

import (
	"context"
	"errors"
	"log"
	"slices"
	"strings"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/credentials"
	"github.com/hehehai/actnowkit/family"
)

// keySource reads the keys of the services set up, off the main thread:
// a service's own Keychain item, or the family's key it uses.
type keySource struct {
	own    keyring
	shared *credentials.Store // nil outside the family
	refs   map[string]string  // account → vendor, of the services on a family key
}

// keysFor is the keySource of settings s.
func keysFor(s settings.Settings, own keyring, shared *credentials.Store) keySource {
	k := keySource{own: own, shared: shared, refs: map[string]string{}}
	for _, kind := range []providers.Kind{providers.ASR, providers.LLM} {
		for _, p := range providerList(s, kind) {
			if v := actnow.RefVendor(p); v != "" {
				k.refs[providers.Account(kind, p.ID)] = v
			}
		}
	}
	return k
}

// keySource is the app's, as its settings are now (main thread).
func (a *app) keySource() keySource { return keysFor(a.prefs, a.keys, a.famCreds.Load()) }

// Get returns the key of account: the family's when its service uses it
// ("" when Auralink may no longer read it, or it is gone: a missing key),
// else its own item.
func (k keySource) Get(account string) (string, error) {
	vendor, ok := k.refs[account]
	if !ok {
		return k.own.Get(account)
	}
	if k.shared == nil {
		return "", nil
	}
	v, err := k.shared.Secret(context.Background(), vendor)
	if errors.Is(err, credentials.ErrNotConsented) || errors.Is(err, credentials.ErrMissing) {
		return "", nil
	}
	return v, err
}

// Mask is the mask of account's key, "" for none: the family's hint for
// a family key (the secret is not read), else the own item's.
func (k keySource) Mask(account string) (string, error) {
	vendor, ok := k.refs[account]
	if !ok {
		v, err := k.own.Get(account)
		return maskKey(v), err
	}
	if k.shared == nil || !k.shared.Using(vendor) {
		return "", nil
	}
	e, _ := k.shared.Offer(vendor)
	return e.Hint, nil
}

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
		users := cs.OtherUsers(vendor)
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
			d.fam.ownKey, d.fam.differs = mine != "", differs
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
// of the key field (spec §8.3): the service has no key, the family has
// one, the user did not set it aside or type one.
func (a *app) offerShown() bool {
	f := a.provDlg.fam
	return f.offer != nil && f.ref == "" && !f.ownKey && !f.other && strings.TrimSpace(a.provDlg.key) == ""
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

// keyJob is what saving the dialog does with its key, decided on the main
// thread: it sets p's credentialRef, and run (on a goroutine) writes the
// Keychain and the family's file and returns the key's new mask (changed
// false: the mask stays).
type keyJob struct {
	run func() (mask string, changed bool, err error)
}

// dialogKeyJob decides keyJob from the dialog: a typed key goes to the
// family (shared) or the service's own item; Remove removes it (for this
// app or for all apps, removeAll); a family key chosen is used; a saved
// key checked to share is copied to the family (spec §15.2 step 6: read
// back, the old item kept); a family key unchecked becomes the service's
// own.
func (a *app) dialogKeyJob(p *settings.Provider, key string, remove, removeAll bool, label string) keyJob {
	d := &a.provDlg
	f := d.fam
	acc := providers.Account(d.kind, p.ID)
	own, cs := a.keys, a.famCreds.Load()
	oldRef := actnow.RefVendor(func() settings.Provider { q, _ := providers.Setup(a.prefs, d.kind, p.ID); return q }())
	vendor := f.vendor
	shared := vendor != "" && cs != nil && !a.famReadOnly
	opts := actnow.SaveOptions(vendor, *p)
	left := oldRef // the family key the service leaves, if any
	if left == "" && f.joined {
		left = vendor // chosen in this dialog, and not kept
	}
	stop := func() error { // leave the family key no other service uses
		if left == "" || cs == nil || a.vendorInUse(left, acc) {
			return nil
		}
		return cs.StopUsing(context.Background(), left)
	}
	ctx := context.Background()
	switch {
	case key != "" && shared && f.share:
		p.CredentialRef = actnow.Ref(vendor)
		return keyJob{func() (string, bool, error) {
			return credentials.Hint(key), true, cs.Save(ctx, vendor, key, opts)
		}}
	case key != "":
		p.CredentialRef = ""
		return keyJob{func() (string, bool, error) {
			if err := own.Set(acc, label, key); err != nil {
				return "", false, err
			}
			return maskKey(key), true, stop()
		}}
	case remove && oldRef != "":
		p.CredentialRef = ""
		return keyJob{func() (string, bool, error) {
			if removeAll && cs != nil {
				return "", true, cs.DeleteForAll(ctx, oldRef)
			}
			return "", true, stop()
		}}
	case remove:
		return keyJob{func() (string, bool, error) {
			if err := own.Set(acc, label, ""); err != nil {
				return "", false, err
			}
			return "", true, stop()
		}}
	case f.ref != "" && oldRef == "":
		p.CredentialRef = f.ref
		hint := ""
		if f.offer != nil {
			hint = f.offer.Hint
		}
		return keyJob{func() (string, bool, error) { return hint, true, nil }}
	case shared && f.share && oldRef == "" && f.ownKey:
		p.CredentialRef = actnow.Ref(vendor)
		return keyJob{func() (string, bool, error) {
			v, err := own.Get(acc)
			if err == nil && v == "" {
				err = credentials.ErrMissing
			}
			if err == nil {
				err = cs.Save(ctx, vendor, v, opts)
			}
			if err == nil {
				var back string
				if back, err = cs.Secret(ctx, vendor); err == nil && back != strings.TrimSpace(v) {
					err = errors.New("the shared key does not read back as written")
				}
			}
			return credentials.Hint(v), true, err
		}}
	case oldRef != "" && !f.share && cs != nil:
		p.CredentialRef = ""
		return keyJob{func() (string, bool, error) {
			v, err := cs.Secret(ctx, oldRef)
			if err == nil {
				err = own.Set(acc, label, v)
			}
			if err != nil {
				return "", false, err
			}
			return maskKey(v), true, stop()
		}}
	}
	p.CredentialRef = d.fam.ref
	return keyJob{func() (string, bool, error) { return "", false, nil }}
}

// eraseSharedKeys is Settings → Storage's "Erase everything" for the
// family's keys the services use (spec §8.3): Auralink stops using a key
// other apps use, and deletes one only it uses. Off the main thread.
func eraseSharedKeys(cs *credentials.Store, vendors []string) {
	ctx := context.Background()
	for _, v := range vendors {
		var err error
		if len(cs.OtherUsers(v)) > 0 {
			err = cs.StopUsing(ctx, v)
		} else if cs.Using(v) {
			err = cs.DeleteForAll(ctx, v)
		}
		if err != nil {
			log.Printf("family: erasing the shared %s key: %v", v, err)
		}
	}
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
