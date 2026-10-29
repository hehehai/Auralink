package main

// The Keychain's lines (docs/diagnostics.md): each configured service's key
// as a read found it (found, missing, not confirmed, or an error with its
// class), logged when its state changes (so once per launch, then on a
// change), what saving or removing a key did, and what the read back after
// a save found. Account names and service names only: never a key, nor its
// mask.

import (
	"fmt"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/diag"
)

// keyRead is one configured service's key as a read found it.
type keyRead struct {
	account string
	keyInfo
}

// keyState names a read for the log (docs/diagnostics.md §3): found, missing,
// not confirmed, or an error with its class. "" for a read that is not
// known yet (the family is not read).
func keyState(r keyRead) string {
	switch r.state {
	case actnow.KeyOwn, actnow.KeyShared:
		return "found"
	case actnow.KeyNone:
		return "missing"
	case actnow.KeyUnconfirmed:
		return "not confirmed (saved by " + r.owner + "; not used here yet)"
	case actnow.KeyGone:
		return "missing (the family's key is no longer in the Keychain)"
	case actnow.KeyFailed:
		return "error (" + diag.ErrorClass(r.err) + ")"
	}
	return ""
}

// keyWhere says where a key is kept.
func keyWhere(family bool) string {
	if family {
		return "in the ActNow family's key"
	}
	return "in the Keychain"
}

// logKeyReads logs the keys whose state changed since the last read; the
// first read of each service is always logged (once per launch).
func (a *app) logKeyReads(reads []keyRead) {
	if a.keyStates == nil {
		a.keyStates = map[string]string{}
	}
	for _, r := range reads {
		st := keyState(r)
		if st == "" {
			continue
		}
		if prev, seen := a.keyStates[r.account]; seen && prev == st {
			continue
		}
		a.keyStates[r.account] = st
		sev := core.SeverityInfo
		if r.state == actnow.KeyFailed || r.state == actnow.KeyUnconfirmed || r.state == actnow.KeyGone {
			sev = core.SeverityWarning
		}
		a.diagLog(sev, diagSource, fmt.Sprintf("key %s %s: %s", r.account, keyWhere(r.family), st))
	}
}

// keyReadBackLine says what Auralink's own item held right after a write
// (found or missing, never the key).
func keyReadBackLine(account, value string, err error) (core.Severity, string) {
	if err != nil {
		return core.SeverityWarning, fmt.Sprintf("key %s: read back from the Keychain failed (%s)", account, diag.ErrorClass(err))
	}
	if value == "" {
		return core.SeverityWarning, fmt.Sprintf("key %s: read back from the Keychain: missing", account)
	}
	return core.SeverityInfo, fmt.Sprintf("key %s: read back from the Keychain: found", account)
}

// keyCopyLine says a family key was copied into Auralink's own item, or
// that the copy failed.
func keyCopyLine(account string, err error) (core.Severity, string) {
	if err != nil {
		return core.SeverityWarning, fmt.Sprintf("key %s: copying the ActNow family's key into the Keychain failed (%s); it stays shared", account, diag.ErrorClass(err))
	}
	return core.SeverityInfo, fmt.Sprintf("key %s: copied from the ActNow family's key into the Keychain; no longer shared", account)
}

// keyOp is the operation a dialog's key does: "save", "remove" or "" for
// none (the key is left as it is).
func keyOp(key string, remove bool) string {
	switch {
	case key != "":
		return "save"
	case remove:
		return "remove"
	}
	return ""
}

// keyOpLine says what saving or removing a service's key did (any goroutine).
func keyOpLine(account, service, op string, family bool, err error) (core.Severity, string) {
	name := account
	if service != "" {
		name += " (" + service + ")"
	}
	past, doing := "saved", "saving"
	if op == "remove" {
		past, doing = "removed", "removing"
	}
	if err != nil {
		return core.SeverityWarning, fmt.Sprintf("key %s: %s %s failed (%s)", name, doing, keyWhere(family), diag.ErrorClass(err))
	}
	return core.SeverityInfo, fmt.Sprintf("key %s: %s %s", name, past, keyWhere(family))
}
