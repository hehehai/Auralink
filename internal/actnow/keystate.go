package actnow

// Where a service's key is, as a read finds it (docs/diagnostics.md §3).
// Auralink's own Keychain item (asr.<id>, llm.<id>) is the authority: it
// is read first, and a service that names a family key (credentialRef)
// reads the family's only when its own item is empty, and only when this
// app agreed to use it (credentials.Store.Secret never reads before that).
// A family key that is not there, not agreed to, or that cannot be read
// is said so: never "no key".

import (
	"context"
	"errors"

	"github.com/hehehai/actnowkit/credentials"
)

// SharedKeys is the family's keys as Auralink reads them: credentials.Store
// (nil outside the family, or before the family is read).
type SharedKeys interface {
	Offer(vendor string) (credentials.Entry, bool)
	Using(vendor string) bool
	Secret(ctx context.Context, vendor string) (string, error)
}

// State is what a read found.
type State int

const (
	KeyNone        State = iota // no key of its own, and none shared to it
	KeyOwn                      // Auralink's own item holds the key
	KeyShared                   // the family's key, which this app uses (its own item is empty)
	KeyUnconfirmed              // the family's key, which this app has not agreed to use yet
	KeyGone                     // the service names a family key that is no longer there
	KeyFailed                   // a Keychain read failed (Err)
	KeyUnknown                  // not read yet: the family is not read yet
)

// Key is a service's key as a read found it. Secret is for the caller
// only: it is never logged or kept beyond the read.
type Key struct {
	State  State
	Secret string
	Owner  string // KeyUnconfirmed, KeyShared: the bundle id that saved the family's key
	Err    error  // KeyFailed
	Family bool   // the state is about the family's key, not Auralink's own item
}

// Found reports whether a usable key was read.
func (k Key) Found() bool { return k.State == KeyOwn || k.State == KeyShared }

// Resolve reads account's key. own reads Auralink's item of account (""
// when there is none); vendor is the family vendor the service names
// ("" for none); shared is nil before the family is read.
func Resolve(ctx context.Context, own func(account string) (string, error), account, vendor string, shared SharedKeys) Key {
	v, err := own(account)
	switch {
	case err != nil:
		return Key{State: KeyFailed, Err: err}
	case v != "":
		return Key{State: KeyOwn, Secret: v}
	case vendor == "":
		return Key{State: KeyNone}
	case shared == nil:
		return Key{State: KeyUnknown, Family: true}
	}
	e, ok := shared.Offer(vendor)
	if !ok {
		return Key{State: KeyGone, Family: true}
	}
	if !shared.Using(vendor) {
		return Key{State: KeyUnconfirmed, Owner: e.AddedBy, Family: true}
	}
	s, err := shared.Secret(ctx, vendor)
	switch {
	case errors.Is(err, credentials.ErrMissing):
		return Key{State: KeyGone, Family: true}
	case errors.Is(err, credentials.ErrNotConsented):
		return Key{State: KeyUnconfirmed, Owner: e.AddedBy, Family: true}
	case err != nil:
		return Key{State: KeyFailed, Err: err, Family: true}
	case s == "":
		return Key{State: KeyGone, Family: true}
	}
	return Key{State: KeyShared, Secret: s, Owner: e.AddedBy, Family: true}
}
