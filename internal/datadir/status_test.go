package datadir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilesIn(t *testing.T) {
	dir := t.TempDir()
	if f := FilesIn(dir); f != (Files{}) || DatabaseExists(dir) {
		t.Fatalf("empty folder: %+v", f)
	}
	for _, name := range []string{ResetFile, "auralink.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if f := FilesIn(dir); f != (Files{Reset: true}) {
		t.Errorf("reset only: %+v", f)
	}
	if !DatabaseExists(dir) {
		t.Errorf("database not seen")
	}
}
