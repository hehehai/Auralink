package main

// The log file's lines from earlier runs, read back into the logs page at
// the start (the page was empty after a restart while the file still held
// the lines). The file is only read: how it is written does not change.

import (
	"path/filepath"
	"time"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/logs"
)

// The newest lines read back: at most logHistoryLines, from the last
// logHistoryBytes of the file, whichever is fewer.
const (
	logHistoryLines = 2000
	logHistoryBytes = 1 << 20
)

// preloadLogHistory puts the file's last lines in front of this run's
// lines, then a line that says where they come from. Called once, before
// the early lines move to the file's ring (connectShell). A file that is
// not there yet (a first start) adds nothing.
func (a *app) preloadLogHistory(dir string) {
	hist, err := logs.ReadTail(filepath.Join(dir, logs.FileName), logHistoryBytes, logHistoryLines)
	if err != nil || len(hist) == 0 {
		return
	}
	marker := core.LogEntry{Time: time.Now(), Level: core.SeverityInfo, Source: "app",
		Message: i18n.T("The lines above are from earlier runs, read back from the log file.")}
	a.logs.Preload(append(hist, marker))
}
