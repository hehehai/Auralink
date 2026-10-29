package main

// The Model services page's keys (providers_page.go): each configured
// service's key as a read found it (internal/actnow.Resolve): its state,
// its mask and, for a family key, the app that saved it. Keys are read on
// goroutines; only states and masks are kept, never a key.
//
// A service that names a family key, keeps no key of its own, and has
// agreed to use the family's one gets that key copied into its own item
// (migration, spec §8): the family reference goes, and Auralink stops
// using the family's key unless another service of its own does.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/diag"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/uitext"
)

// keyInfo is a service's key as the page last read it.
type keyInfo struct {
	state  actnow.State
	mask   string // Own, Shared: the key's mask
	owner  string // Unconfirmed, Shared, Gone: the family app that saved the key
	err    error  // Failed
	family bool   // the state is about the family's key (diagnostics)
}

// errText is a failed read's error, as the dialog shows it ("" for none).
func (k keyInfo) errText() string {
	if k.err == nil {
		return ""
	}
	return k.err.Error()
}

// keyTarget is a configured service whose key is read.
type keyTarget struct {
	account, label string
	kind           providers.Kind
	p              settings.Provider
}

// keyTargets are the services among accounts (all configured services
// when accounts is nil). An account not set up is read too, as a service
// with no setup: its own item may still hold a key.
func (a *app) keyTargets(accounts []string) []keyTarget {
	var out []keyTarget
	if accounts != nil {
		for _, acc := range accounts {
			kind, id, ok := strings.Cut(acc, ".")
			if !ok {
				continue
			}
			k := providers.Kind(kind)
			p, _ := providers.Setup(a.prefs, k, id)
			p.ID = id
			out = append(out, keyTarget{account: acc, label: "Auralink " + uitext.ProviderName(k, p), kind: k, p: p})
		}
		return out
	}
	for _, kind := range []providers.Kind{providers.ASR, providers.LLM} {
		for _, p := range providerList(a.prefs, kind) {
			out = append(out, keyTarget{account: providers.Account(kind, p.ID), label: "Auralink " + uitext.ProviderName(kind, p), kind: kind, p: p})
		}
	}
	return out
}

// loadKeyMasks reads the keys of accounts in the background and keeps
// their states and masks. Before the family is read, a service that names
// a family key is Unknown rather than missing.
func (a *app) loadKeyMasks(accounts []string) {
	targets := a.keyTargets(accounts)
	if len(targets) == 0 {
		return
	}
	ks, ring := a.keySource(), a.logs // family_keys_read.go
	a.async(func() {
		ctx := context.Background()
		infos := make([]keyInfo, len(targets))
		migrated := make([]bool, len(targets))
		for i, tg := range targets {
			k := ks.resolve(ctx, tg.account)
			infos[i] = keyInfo{state: k.State, owner: k.Owner, err: k.Err, family: k.Family}
			if k.Found() {
				infos[i].mask = maskKey(k.Secret)
			}
			if k.State == actnow.KeyShared {
				err := copyOwnKey(ks.own, tg.account, tg.label, k.Secret)
				sev, line := keyCopyLine(tg.account, err)
				appendLine(ring, sev, diagSource, line)
				if err != nil {
					continue
				}
				migrated[i] = true
				infos[i] = keyInfo{state: actnow.KeyOwn, mask: infos[i].mask}
			}
		}
		a.post(func() {
			reads := make([]keyRead, 0, len(targets))
			for i, tg := range targets {
				a.keyInfos[tg.account] = infos[i]
				a.keyMasks[tg.account] = infos[i].mask
				reads = append(reads, keyRead{account: tg.account, keyInfo: infos[i]})
			}
			a.logKeyReads(reads) // diagnostics_keys.go
			a.keysKnown = a.keysReady()
			a.checkServices() // shell_notices.go
			for i, tg := range targets {
				if migrated[i] {
					a.dropFamilyRef(tg)
				}
			}
		})
	})
}

// copyOwnKey writes a family key into Auralink's own item and reads it
// back; an error means the own item was not written as it should be.
func copyOwnKey(own keyring, account, label, secret string) error {
	if err := own.Set(account, label, secret); err != nil {
		return err
	}
	back, err := own.Get(account)
	if err != nil {
		return err
	}
	if back != secret {
		return errors.New("the key does not read back as written")
	}
	return nil
}

// dropFamilyRef is the end of a migration on the main thread: the service
// keeps its own key, so its credentialRef goes, and Auralink leaves the
// family's key unless another service of its own uses it.
func (a *app) dropFamilyRef(tg keyTarget) {
	ref := tg.p.CredentialRef
	a.updateSettings(func(s *settings.Settings) {
		p, ok := providers.Setup(*s, tg.kind, tg.p.ID)
		if !ok || p.CredentialRef != ref {
			return
		}
		p.CredentialRef = ""
		providers.Put(s, tg.kind, p)
	})
	vendor := actnow.RefVendor(tg.p)
	cs := a.famCreds.Load()
	if vendor == "" || cs == nil || a.vendorInUse(vendor, tg.account) {
		return
	}
	ring := a.logs
	a.async(func() {
		if err := cs.StopUsing(context.Background(), vendor); err != nil {
			appendLine(ring, core.SeverityWarning, diagSource, fmt.Sprintf("key %s: leaving the ActNow family's key failed (%s)", tg.account, diag.ErrorClass(err)))
		}
	})
}

// familySettled runs once the family is read (or is not there): the
// services that name a family key are read again, now that their state
// can be told apart from "not read yet".
func (a *app) familySettled() {
	a.famSettled = true
	var accounts []string
	for _, kind := range []providers.Kind{providers.ASR, providers.LLM} {
		for _, p := range providerList(a.prefs, kind) {
			if actnow.RefVendor(p) != "" {
				accounts = append(accounts, providers.Account(kind, p.ID))
			}
		}
	}
	if len(accounts) == 0 {
		a.keysKnown = a.keysReady()
		a.checkServices()
		return
	}
	a.loadKeyMasks(accounts)
}

// keysReady reports whether every configured service's key is known: none
// is Unknown, which is the case only until the family is read.
func (a *app) keysReady() bool {
	if a.famSettled {
		return true
	}
	for _, tg := range a.keyTargets(nil) {
		if a.keyInfos[tg.account].state == actnow.KeyUnknown {
			return false
		}
	}
	return true
}

// keyStateText is a service's key line in short, as a card's state: why
// it has no key.
func (a *app) keyStateText(info keyInfo) string {
	switch info.state {
	case actnow.KeyUnconfirmed:
		return i18n.T("Confirm the key")
	case actnow.KeyFailed:
		return i18n.T("Keychain error")
	case actnow.KeyGone:
		return i18n.T("Key missing")
	}
	return i18n.T("No key saved")
}

// keyCardText is a card's key line for a service with no key: what to do
// about it, or why it has none.
func (a *app) keyCardText(info keyInfo) string {
	switch info.state {
	case actnow.KeyUnconfirmed:
		from := i18n.T("another ActNow app")
		if info.owner != "" {
			from = a.appNames([]string{info.owner})[0]
		}
		return i18n.T("Saved in %s · click to use it", from)
	case actnow.KeyFailed:
		return i18n.T("Could not read the key · check Keychain access")
	case actnow.KeyGone:
		return i18n.T("The shared key is gone · enter it again")
	}
	return i18n.T("No key saved")
}

// hasKey reports whether a key is saved for account (as last read).
func (a *app) hasKey(account string) bool { return a.keyMasks[account] != "" }
