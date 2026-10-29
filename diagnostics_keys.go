package main

// The Keychain's lines (docs/diagnostics.md): each configured service's key
// as a read found it (found, missing, or an error with its class), logged
// when its state changes (so once per launch, then on a change), and what
// saving or removing a key did. Account names and service names only:
// never a key, nor its mask.

import (
	"fmt"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/diag"
)

// keyRead is one configured service's key as a read found it.
type keyRead struct {
	account string
	family  bool   // the service uses the ActNow family's key
	mask    string // "" for none
	err     error
}

// keyState names a read for the log: found, missing, or an error with its class.
func keyState(r keyRead) string {
	switch {
	case r.err != nil:
		return "error (" + diag.ErrorClass(r.err) + ")"
	case r.mask != "":
		return "found"
	}
	return "missing"
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
		if prev, seen := a.keyStates[r.account]; seen && prev == st {
			continue
		}
		a.keyStates[r.account] = st
		sev := core.SeverityInfo
		if r.err != nil {
			sev = core.SeverityWarning
		}
		a.diagLog(sev, diagSource, fmt.Sprintf("key %s %s: %s", r.account, keyWhere(r.family), st))
	}
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
