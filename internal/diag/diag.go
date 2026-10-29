// Package diag holds the pure helpers of the diagnostic log lines (the
// lines a user copies to a bug report): how an error is named without
// its text, and how a path is shown without the user's name. No I/O, no
// MyGo; the lines themselves are written by package main.
//
// Imports: nothing internal.
package diag

import (
	"fmt"
	"regexp"
	"strings"
)

// codeRE is an OSStatus or errno-like code, e.g. -25308.
var codeRE = regexp.MustCompile(`-\d{4,6}\b`)

// migrationRE is the number of a migration that failed.
var migrationRE = regexp.MustCompile(`migration (\d+)`)

// known are the phrases an error may carry (the Keychain's, SQLite's,
// Go's), and what a log line says for each. Only these reach the log: an
// error's own text may hold a path, a value or a key.
var known = []struct{ needle, label string }{
	{"user interaction is not allowed", "user interaction not allowed"},
	{"could not be found", "not found"},
	{"already exists", "already exists"},
	{"not correct", "wrong passphrase"},
	{"authorization was denied", "authorization denied"},
	{"locked", "locked"},
	{"read back", "does not read back"},
	{"no keychain", "no keychain"},
	{"newer than this app", "schema newer than this app"},
	{"backup before migrating", "backup failed"},
	{"unable to open", "unable to open"},
	{"disk i/o error", "disk I/O error"},
	{"malformed", "malformed"},
	{"not a database", "not a database"},
	{"read-only", "read-only"},
	{"no such file", "no such file"},
	{"permission denied", "permission denied"},
	{"deadline exceeded", "timed out"},
	{"timed out", "timed out"},
	{"canceled", "cancelled"},
}

// ErrorClass names an error for a log line without its text: the codes,
// migration number and known phrases it carries, else the Go type. ""
// for nil.
func ErrorClass(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	var parts []string
	parts = append(parts, codeRE.FindAllString(msg, -1)...)
	if m := migrationRE.FindStringSubmatch(msg); m != nil {
		parts = append(parts, "migration "+m[1])
	}
	for _, k := range known {
		if strings.Contains(low, k.needle) && !contains(parts, k.label) {
			parts = append(parts, k.label)
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%T", err)
	}
	return strings.Join(parts, ", ")
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Tilde shows path with the home folder as "~" ("~/Library/…"); a path
// outside home, or an empty home, is shown as it is.
func Tilde(path, home string) string {
	if path == "" || home == "" {
		return path
	}
	home = strings.TrimSuffix(home, "/")
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+"/"); ok {
		return "~/" + rest
	}
	return path
}
