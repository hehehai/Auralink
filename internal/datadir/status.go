package datadir

// What the start-up log says about the data folder (diagnostics): which
// of the folder's own files are there, read before Resolve finishes them.

import (
	"os"
	"path/filepath"

	"github.com/hehehai/Auralink/internal/store/sqlitestore"
)

// Files are the folder's own files that change what a start does.
type Files struct {
	Location bool // location.json: the data folder is another one
	Move     bool // move.json: the folder is moved at this start
	Reset    bool // reset.json: the folder is erased at this start
}

// FilesIn reports which of those files exist in the default folder def.
func FilesIn(def string) Files {
	return Files{
		Location: exists(filepath.Join(def, LocationFile)),
		Move:     exists(filepath.Join(def, MoveFile)),
		Reset:    exists(filepath.Join(def, ResetFile)),
	}
}

// DatabaseExists reports whether dir holds a database already (before it
// is opened, which creates one).
func DatabaseExists(dir string) bool {
	return exists(filepath.Join(dir, sqlitestore.FileName))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
