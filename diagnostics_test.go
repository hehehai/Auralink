package main

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/datadir"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store/sqlitestore"
)

// logText is every line of the app's logs, one per line.
func logText(a *app) string {
	var b strings.Builder
	for _, e := range a.logs.Tail(1<<20, "") {
		fmt.Fprintf(&b, "%s %s %s\n", e.Source, e.Level, e.Message)
	}
	return b.String()
}

// failingKeyring reads keys from m, and fails the reads of fail's accounts
// the way the Keychain does.
type failingKeyring struct {
	m    map[string]string
	fail map[string]error
}

func (k failingKeyring) Get(account string) (string, error) {
	if err := k.fail[account]; err != nil {
		return "", err
	}
	return k.m[account], nil
}

func (k failingKeyring) Set(account, _, value string) error {
	k.m[account] = value
	return nil
}

func TestCloseDecision(t *testing.T) {
	cases := []struct {
		name    string
		g       settings.General
		quit    bool
		tray    bool
		hide    bool
		because string
	}{
		{"kept in the menu bar", settings.General{CloseToTray: true}, false, true, true, "hidden in the menu bar"},
		{"setting off", settings.General{CloseToTray: false}, false, true, false, "keep running when the window closes is off"},
		{"no icon", settings.General{CloseToTray: true}, false, false, false, "no menu bar icon"},
		{"quitting", settings.General{CloseToTray: true}, true, true, false, "quit was chosen"},
	}
	for _, c := range cases {
		hide, line := closeDecision(c.g, c.quit, c.tray)
		if hide != c.hide || !strings.Contains(line, c.because) {
			t.Errorf("%s: hide %v, line %q", c.name, hide, line)
		}
	}
}

// The Keychain's reads: a line when a service's state first shows and when
// it changes, never the key, and an error by its class.
func TestKeyStateLinesHoldNoKey(t *testing.T) {
	const secret = "sk-SECRETVALUE-000000abcd"
	a, _, _ := newTestApp(t)
	kr := failingKeyring{
		m:    map[string]string{"asr.aliyun": secret},
		fail: map[string]error{"llm.deepseek": errors.New("security: User interaction is not allowed. (-25308)")},
	}
	a.keys = kr
	read := func() { a.loadKeyMasks([]string{"asr.aliyun", "llm.deepseek"}) }
	read()
	text := logText(a)
	for _, want := range []string{
		"app info key asr.aliyun in the Keychain: found",
		"app warning key llm.deepseek in the Keychain: error (-25308, user interaction not allowed)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	read() // unchanged: nothing new
	if n := strings.Count(logText(a), "key asr.aliyun"); n != 1 {
		t.Errorf("unchanged state logged %d times", n)
	}
	kr.m["asr.aliyun"] = ""
	read() // changed: one more line
	if n := strings.Count(logText(a), "key asr.aliyun"); n != 2 || !strings.Contains(logText(a), "key asr.aliyun in the Keychain: missing") {
		t.Errorf("changed state: %d lines\n%s", n, logText(a))
	}
	if strings.Contains(logText(a), "SECRETVALUE") {
		t.Errorf("a key reached the logs:\n%s", logText(a))
	}
}

func TestKeyOpLines(t *testing.T) {
	const secret = "sk-SECRETVALUE-000000abcd"
	sev, line := keyOpLine("llm.deepseek", "DeepSeek", "save", false, nil)
	if sev != core.SeverityInfo || line != "key llm.deepseek (DeepSeek): saved in the Keychain" {
		t.Errorf("saved: %s %q", sev, line)
	}
	sev, line = keyOpLine("asr.aliyun", "Aliyun", "remove", true, errors.New("add "+secret+" failed"))
	if sev != core.SeverityWarning || line != "key asr.aliyun (Aliyun): removing in the ActNow family's key failed (*errors.errorString)" {
		t.Errorf("failed: %s %q", sev, line)
	}
	if strings.Contains(line, secret) {
		t.Errorf("key in %q", line)
	}
}

func TestStartupLines(t *testing.T) {
	cases := map[string]struct {
		o    sqlitestore.OpenInfo
		want string
	}{
		"opened":   {sqlitestore.OpenInfo{Found: 5, Now: 5}, "database: opened, schema version 5"},
		"new":      {sqlitestore.OpenInfo{Found: 0, Now: 5}, "database: new, schema version 5"},
		"migrated": {sqlitestore.OpenInfo{Found: 3, Now: 5}, "database: migrated from schema version 3 to 5 (a copy of the old file is kept)"},
	}
	for name, c := range cases {
		if got := databaseLine(c.o); got != c.want {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := settingsLine(true, nil); got != "settings: saved settings read" {
		t.Errorf("stored: %q", got)
	}
	if got := settingsLine(false, nil); got != "settings: no saved settings, defaults used" {
		t.Errorf("new: %q", got)
	}
	if got := settingsLine(false, errors.New("settings: decode failed")); got != "settings: could not be read (*errors.errorString), defaults used" {
		t.Errorf("error: %q", got)
	}
}

func TestStartupSummary(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.version = "0.1.0"
	a.logStartup(startFacts{dataDir: "/data/auralink", home: "/data/auralink", dbExisted: true, files: datadir.Files{Reset: true}})
	text := logText(a)
	for _, want := range []string{
		"app info start: Auralink 0.1.0 · data folder /data/auralink (default) · database found",
		"app info data folder files: location.json absent, move.json absent, reset.json present",
		"app warning reset.json is present: the data folder is erased at this start",
		"app info database: new, schema version 5",
		"app info settings: no saved settings, defaults used",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "move.json is present") {
		t.Errorf("move reported:\n%s", text)
	}
}

// The logs kept before the files open (the start's Keychain reads) move
// to the files' ring when the app connects its logs.
func TestEarlyLinesKeptWhenFilesOpen(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.diagLog(core.SeverityInfo, diagSource, "early line")
	old := log.Writer()
	t.Cleanup(func() { log.SetOutput(old) })
	a.connectShell(t.TempDir())
	if !strings.Contains(logText(a), "app info early line") {
		t.Errorf("early line lost:\n%s", logText(a))
	}
}

// The copied diagnostics carry the recent log lines, and not old ones.
func TestDiagnosticsCarryRecentLines(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.diagLog(core.SeverityInfo, diagSource, "fresh line")
	appendLine(a.logs, core.SeverityInfo, diagSource, "old line")
	// Backdate the second line: the ring keeps the time it was given.
	a.logs.Append(core.LogEntry{Time: time.Now().Add(-2 * diagLogWithin), Level: core.SeverityInfo, Source: diagSource, Message: "stale line"})
	text := a.diagnosticsText()
	if !strings.Contains(text, "Recent log lines:") || !strings.Contains(text, "fresh line") || strings.Contains(text, "stale line") {
		t.Errorf("diagnostics:\n%s", text)
	}
}
