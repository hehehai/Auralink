package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/logs"
)

// TestLogHistoryReadBack: the logs page starts with the file's lines of
// earlier runs, then a line that says so; the file itself is not written.
func TestLogHistoryReadBack(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 29, 9, 14, 2, 0, time.Local)
	var b strings.Builder
	for i, m := range []string{"start: Auralink 0.1.0", "key asr.aliyun in the Keychain: found", "quit requested"} {
		b.WriteString(logs.Format(core.LogEntry{Time: base.Add(time.Duration(i) * time.Second), Level: core.SeverityInfo, Source: "app", Message: m}) + "\n")
	}
	path := filepath.Join(dir, logs.FileName)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _, _ := newTestApp(t)
	a.preloadLogHistory(dir)
	all := a.logs.Tail(100, "") // the test app's own lines come first
	if len(all) < 4 {
		t.Fatalf("lines: %+v", all)
	}
	lines := all[len(all)-4:]
	if lines[0].Message != "start: Auralink 0.1.0" || lines[2].Message != "quit requested" {
		t.Fatalf("lines: %+v", lines)
	}
	if !strings.Contains(lines[3].Message, "earlier runs") {
		t.Errorf("no marker: %q", lines[3].Message)
	}
	if got, _ := os.ReadFile(path); string(got) != b.String() {
		t.Error("the log file was changed")
	}
}

// TestLogHistoryNoFile: a first start has no file, and adds nothing.
func TestLogHistoryNoFile(t *testing.T) {
	a, _, _ := newTestApp(t)
	before := len(a.logs.Tail(100, ""))
	a.preloadLogHistory(t.TempDir())
	if n := len(a.logs.Tail(100, "")); n != before {
		t.Errorf("%d lines added for no file", n-before)
	}
}
