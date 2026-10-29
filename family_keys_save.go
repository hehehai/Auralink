package main

// Saving a service's key from its dialog (providers_dialog.go; spec §8.3).
// Auralink's own Keychain item (asr.<id>, llm.<id>) is always written
// first: it is the authority that reads use (family_keys_read.go). The
// family's key is written only when the key is shared: the checkbox, which
// is shown when another ActNow app is installed (keyShared). A service
// that keeps no key of its own but names a family key gets it copied into
// its own item when the user chose it or agreed to it.

import (
	"context"
	"errors"
	"log"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/logs"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/credentials"
)

// keyPlan is what saving the dialog does with the service's key. Its
// decisions are read on the main thread (dialogKeyPlan); run does the
// Keychain and family writes on a goroutine and returns the credentialRef
// the saved setup keeps.
type keyPlan struct {
	acc, label string
	p          settings.Provider // the setup being saved (its App ID's options)
	own        keyring
	cs         *credentials.Store // nil outside the family
	logs       *logs.Ring
	oldRef     string // the saved setup's credentialRef
	oldVendor  string // … its vendor
	oldInUse   bool   // another service of Auralink uses oldVendor's key (it keeps its own item)
	vendor     string // the family vendor of the dialog's service ("" for none)
	share      bool   // the key is shared with the family
	typed      string // the key typed
	remove     bool   // the saved key is removed
	removeAll  bool   // … and deleted for every app (when no other service uses it)
	chosen     string // the family key the user chose in the dialog ("" for none)
}

// dialogKeyPlan is the plan of saving p from the dialog.
func (a *app) dialogKeyPlan(p settings.Provider, key string, remove, removeAll bool, label string) keyPlan {
	d := &a.provDlg
	f := d.fam
	saved, _ := providers.Setup(a.prefs, d.kind, p.ID)
	oldVendor := actnow.RefVendor(saved)
	acc := providers.Account(d.kind, p.ID)
	return keyPlan{
		acc: acc, label: label, p: p, own: a.keys, cs: a.famCreds.Load(), logs: a.logs,
		oldRef: saved.CredentialRef, oldVendor: oldVendor, oldInUse: oldVendor != "" && a.vendorInUse(oldVendor, acc),
		vendor: f.vendor, share: a.keyShared(f, saved.CredentialRef),
		typed: key, remove: remove, removeAll: removeAll, chosen: f.secret,
	}
}

// keyShared reports whether the key is shared with the family. The
// checkbox shows only when another ActNow app is installed; without it,
// a service keeps the sharing it had and a new key is not shared.
func (a *app) keyShared(f dialogFamily, oldRef string) bool {
	switch {
	case a.famReadOnly || a.famCreds.Load() == nil || f.vendor == "":
		return false
	case !a.familyOthers():
		return oldRef != ""
	}
	return f.share
}

// ref is the credentialRef of the shared key, "" when not shared.
func (pl keyPlan) ref() string {
	if pl.share && pl.vendor != "" {
		return actnow.Ref(pl.vendor)
	}
	return ""
}

func (pl keyPlan) opts() credentials.SaveOptions {
	return actnow.SaveOptions(pl.vendor, pl.p)
}

// run does the plan (any goroutine) and returns the credentialRef to keep.
func (pl keyPlan) run() (string, error) {
	ctx := context.Background()
	ref := pl.ref()
	switch {
	case pl.typed != "":
		if err := pl.own.Set(pl.acc, pl.label, pl.typed); err != nil {
			return "", err
		}
		pl.readBack()
		return pl.settle(ctx, ref, pl.typed, true)
	case pl.remove:
		if err := pl.own.Set(pl.acc, pl.label, ""); err != nil {
			return "", err
		}
		if pl.removeAll && pl.oldVendor != "" && pl.cs != nil {
			// The family's copy goes for every app; another service of
			// Auralink keeps its own item (the authority).
			return "", pl.cs.DeleteForAll(ctx, pl.oldVendor)
		}
		return "", pl.stopOld(ctx)
	}
	own, err := pl.own.Get(pl.acc)
	if err != nil {
		return "", err
	}
	if own != "" {
		if ref != "" && ref == pl.oldRef {
			return ref, nil // shared already, the key unchanged
		}
		return pl.settle(ctx, ref, own, true)
	}
	if pl.oldRef == "" && pl.chosen == "" {
		return "", nil // no key: nothing to keep
	}
	// A family key the service uses (or the user chose) becomes its own too.
	secret := pl.chosen
	if secret == "" {
		if pl.cs == nil {
			return pl.oldRef, nil // outside the family: nothing to copy from
		}
		s, err := pl.cs.Secret(ctx, pl.oldVendor)
		switch {
		case errors.Is(err, credentials.ErrNotConsented):
			return pl.oldRef, nil // not agreed to yet: the offer stays
		case err != nil:
			return "", err
		}
		secret = s
	}
	if err := pl.own.Set(pl.acc, pl.label, secret); err != nil {
		return "", err
	}
	pl.readBack()
	return pl.settle(ctx, ref, secret, ref != pl.oldRef)
}

// settle makes the family side match ref: the shared key is written when
// changed (or when ref is new), and a family key the service no longer
// uses is left (StopUsing, unless another service of Auralink uses it).
func (pl keyPlan) settle(ctx context.Context, ref, secret string, changed bool) (string, error) {
	if ref != "" && (changed || ref != pl.oldRef) {
		if err := pl.cs.Save(ctx, pl.vendor, secret, pl.opts()); err != nil {
			return "", err
		}
	}
	if pl.oldVendor != "" && pl.oldVendor != pl.vendor || ref == "" {
		if err := pl.stopOld(ctx); err != nil {
			return "", err
		}
	}
	return ref, nil
}

// stopOld leaves the family key the service used before, when no other
// service of Auralink uses it.
func (pl keyPlan) stopOld(ctx context.Context) error {
	if pl.oldVendor == "" || pl.oldInUse || pl.cs == nil {
		return nil
	}
	return pl.cs.StopUsing(ctx, pl.oldVendor)
}

// readBack reads Auralink's item back after a write and logs what it
// found (never the key).
func (pl keyPlan) readBack() {
	v, err := pl.own.Get(pl.acc)
	sev, line := keyReadBackLine(pl.acc, v, err)
	appendLine(pl.logs, sev, diagSource, line)
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
