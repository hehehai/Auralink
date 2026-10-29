package main

// Reading a service's key (family_keys.go; spec §8). Auralink's own
// Keychain item (asr.<id>, llm.<id>) is the authority and is read first.
// Only a service with no key of its own that names a family key
// (credentialRef) reads the family's, and only once this app agreed to use
// it (internal/actnow.Resolve). Sessions and tests read through Get; the
// Model services page keeps the states (providers_keys.go).

import (
	"context"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/credentials"
)

// keySource reads the keys of the services set up, off the main thread.
type keySource struct {
	own    keyring
	shared *credentials.Store // nil outside the family, or before it is read
	refs   map[string]string  // account → vendor, of the services naming a family key
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

// resolve reads account's key (actnow.Resolve).
func (k keySource) resolve(ctx context.Context, account string) actnow.Key {
	var shared actnow.SharedKeys // a nil interface, not a nil store
	if k.shared != nil {
		shared = k.shared
	}
	return actnow.Resolve(ctx, k.own.Get, account, k.refs[account], shared)
}

// Get returns the key a service uses: "" when there is none to use (none
// saved, not agreed to yet, gone, or the family is not read yet), and an
// error when a Keychain read failed.
func (k keySource) Get(account string) (string, error) {
	r := k.resolve(context.Background(), account)
	switch {
	case r.Found():
		return r.Secret, nil
	case r.State == actnow.KeyFailed:
		return "", r.Err
	}
	return "", nil
}
