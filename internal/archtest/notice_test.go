package archtest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noticeElsewhere are the modules whose licence text is not a file of
// LICENSES/go named after their version: the Apache-2.0 ones share
// LICENSES/Apache-2.0.txt; MyGo is vendored, and its licence is a copy of
// third_party/mygo/LICENSE (TestBundledNotices).
var noticeElsewhere = map[string]string{
	"github.com/ebitengine/purego": "LICENSES/Apache-2.0.txt",
	"github.com/egoist/mygo":       "LICENSES/go/github.com_egoist_mygo.LICENSE.txt",
}

// requires returns the modules go.mod requires, path → version.
func requires(t *testing.T, gomod string) map[string]string {
	t.Helper()
	mods := map[string]string{}
	block := false
	for line := range strings.Lines(gomod) {
		line, _, _ = strings.Cut(strings.TrimSpace(line), "//")
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "require" && f[1] == "(":
			block = true
		case block && len(f) == 1 && f[0] == ")":
			block = false
		case block && len(f) == 2:
			mods[f[0]] = f[1]
		case !block && len(f) == 3 && f[0] == "require":
			mods[f[1]] = f[2]
		}
	}
	if len(mods) == 0 {
		t.Fatal("go.mod requires no module: the parser is wrong")
	}
	return mods
}

// TestNotice: every module go.mod requires is named in NOTICE with its
// version, and its licence text is in the repository (AGENTS.md
// "Licence"), so that a new or updated dependency cannot ship without its
// notice.
func TestNotice(t *testing.T) {
	root, _ := moduleRoot(t)
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	notice := read("NOTICE")
	for path, version := range requires(t, read("go.mod")) {
		licence, ok := noticeElsewhere[path]
		if ok {
			if !strings.Contains(notice, path) {
				t.Errorf("NOTICE does not name %s: add it to \"Third-party material\"", path)
			}
		} else {
			if !strings.Contains(notice, path+" "+version) {
				t.Errorf("NOTICE does not name %s %s: add or update its line under \"Go modules compiled into Auralink\"", path, version)
			}
			licence = "LICENSES/go/" + strings.ReplaceAll(path, "/", "_") + ".LICENSE.txt"
		}
		if _, err := os.Stat(filepath.Join(root, licence)); err != nil {
			t.Errorf("%s: no licence text at %s: copy the module's LICENSE file there, unchanged", path, licence)
		}
	}
}

// TestBundledNotices: Auralink.app carries its licence, NOTICE and the
// licence texts of LICENSES/ in Contents/Resources (mygo.json
// "resources"), as the third-party licences ask of a binary and as
// Settings → About opens them; MyGo's licence in LICENSES/ is its own,
// unchanged.
func TestBundledNotices(t *testing.T) {
	root, _ := moduleRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "mygo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Resources []string `json:"resources"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"LICENSE", "NOTICE", "LICENSES"} {
		found := false
		for _, r := range cfg.Resources {
			found = found || filepath.Clean(r) == want
		}
		if !found {
			t.Errorf("mygo.json's resources do not ship %s with the app", want)
		}
	}
	vendored, err := os.ReadFile(filepath.Join(root, "third_party/mygo/LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(filepath.Join(root, noticeElsewhere["github.com/egoist/mygo"]))
	if err != nil || !bytes.Equal(copied, vendored) {
		t.Errorf("%s is not third_party/mygo/LICENSE: copy it again (%v)", noticeElsewhere["github.com/egoist/mygo"], err)
	}
}
