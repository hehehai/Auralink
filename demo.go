//go:build demo

package main

// The demo build: the README's screenshots and recordings
// (scripts/readme-media/). Built with -tags demo into an app of its own
// (bundle ID dev.hehehai.auralink.demo, scripts/readme-media/build.sh),
// it never touches the user's Auralink:
//
//   - its data, logs and cache are in $AURALINK_DEMO_DIR (it will not
//     start without one, nor as dev.hehehai.auralink);
//   - its keys come from the file $AURALINK_DEMO_KEYS (STEPFUN_API_KEY,
//     DEEPSEEK_API_KEY), never the Keychain, and Model services shows
//     none of them;
//   - the first start seeds sample data ($AURALINK_DEMO_LANG: en or zh):
//     settings, app enhancement groups, a dictionary and a history;
//   - the services' HTTP goes through a recorder: in a "take" (the
//     script's command), each request is saved with its response and its
//     timing, and played back the next time the take runs, so that a
//     retake costs no call; calls.log counts the real ones;
//   - the recording script drives it with commands, files it drops in
//     $AURALINK_DEMO_DIR/ctl (demoCommand): the shortcuts' events, pages,
//     the answer card's buttons. The helper records a file
//     (AURALINK_HELPER_CAPTURE_FILE), so no microphone is asked for.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/permission"
	"github.com/hehehai/Auralink/internal/store"
)

func init() {
	demoStart = demoSetup
	demoLibrary = demoSeed
	demoReady = demoControl
	demoPerms = func(r *proto.PermissionsResult) {
		// The helper records a file: the microphone is not needed.
		if os.Getenv("AURALINK_HELPER_CAPTURE_FILE") != "" {
			r.Microphone = proto.PermGranted
		}
	}
	demoMask = func(string) string { return "••••…••••" }
	// In English, the system apps' English names (this Mac runs in
	// Chinese).
	demoFront = func(a proto.App) proto.App {
		if os.Getenv("AURALINK_DEMO_LANG") != "zh" {
			if n, ok := map[string]string{"com.apple.TextEdit": "TextEdit", "com.apple.Notes": "Notes", "com.apple.mail": "Mail",
				"com.apple.finder": "Finder", "com.apple.Safari": "Safari"}[a.BundleID]; ok {
				a.Name = n
			}
		}
		return a
	}
	demoNoKeys = true
	// The Mac's own microphone only (not a phone's, named after its
	// owner), under its English name in English.
	demoDevices = func(ds []proto.CaptureDevice) []proto.CaptureDevice {
		var out []proto.CaptureDevice
		for _, d := range ds {
			if d.Transport == "builtin" {
				if os.Getenv("AURALINK_DEMO_LANG") != "zh" {
					d.Name = "MacBook Air Microphone"
				}
				out = append(out, d)
			}
		}
		return out
	}
}

// demoBundleID is the demo app's bundle ID; the user's is realBundleID.
const (
	demoBundleID = "dev.hehehai.auralink.demo"
	realBundleID = "dev.hehehai.auralink"
)

var demoDir string

func demoFatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "auralink demo: "+format+"\n", args...)
	os.Exit(2)
}

// demoSetup points the app at the demo's folders and keys.
func demoSetup() {
	demoDir = os.Getenv("AURALINK_DEMO_DIR")
	if demoDir == "" || !filepath.IsAbs(demoDir) {
		demoFatal("AURALINK_DEMO_DIR must name the demo's folder (absolute)")
	}
	if id := bundleID(); id == realBundleID {
		demoFatal("a demo build must not be %s", realBundleID)
	}
	for name, sub := range map[mygo.PathName]string{mygo.PathUserData: "data", mygo.PathLogs: "logs", mygo.PathCache: "cache"} {
		p := filepath.Join(demoDir, sub)
		if err := os.MkdirAll(p, 0o755); err != nil {
			demoFatal("%v", err)
		}
		mygo.App.SetPath(name, p)
	}
	keys := demoKeyring{m: map[string]string{}}
	if f := os.Getenv("AURALINK_DEMO_KEYS"); f != "" {
		env := readEnvFile(f)
		if v := env["STEPFUN_API_KEY"]; v != "" {
			keys.m["asr.stepfun"], keys.m["llm.stepfun"] = v, v
		}
		if v := env["DEEPSEEK_API_KEY"]; v != "" {
			keys.m["llm.deepseek"] = v
		}
	}
	newKeyring = func() keyring { return &keys }
	demoRec = &replay{dir: filepath.Join(demoDir, "replay"), calls: filepath.Join(demoDir, "calls.log"), base: http.DefaultTransport}
	demoHTTP = &http.Client{Transport: demoRec}
}

// bundleID reads the running bundle's identifier ("" outside a bundle).
func bundleID() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(exe)), "Info.plist"))
	if err != nil {
		return ""
	}
	m := regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>([^<]*)</string>`).FindSubmatch(b)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// readEnvFile reads NAME=value lines (keys.env).
func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

// demoKeyring holds the demo's keys in memory.
type demoKeyring struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *demoKeyring) Get(acc string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m[acc], nil
}

func (k *demoKeyring) Set(acc, _, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if v == "" {
		delete(k.m, acc)
	} else {
		k.m[acc] = v
	}
	return nil
}

// ---- commands ----

// demoPermSys reports the microphone granted while the helper records a
// file.
type demoPermSys struct{ permission.System }

func (d demoPermSys) Microphone() permission.State { return permission.Granted }

// demoControl watches $AURALINK_DEMO_DIR/ctl for commands: files named
// <n>.cmd, run in name order, each answered with <n>.ok (or .err).
func demoControl(a *app) {
	if os.Getenv("AURALINK_HELPER_CAPTURE_FILE") != "" {
		a.permSys = demoPermSys{permission.Native()}
	}
	dir := filepath.Join(demoDir, "ctl")
	os.MkdirAll(dir, 0o755)
	go func() {
		for {
			time.Sleep(50 * time.Millisecond)
			names, _ := filepath.Glob(filepath.Join(dir, "*.cmd"))
			sort.Strings(names)
			for _, name := range names {
				b, err := os.ReadFile(name)
				os.Remove(name)
				if err != nil {
					continue
				}
				line := strings.TrimSpace(string(b))
				done := make(chan string, 1)
				a.post(func() { done <- demoCommand(a, line) })
				res := <-done
				ext := ".ok"
				if strings.HasPrefix(res, "error") {
					ext = ".err"
				}
				os.WriteFile(strings.TrimSuffix(name, ".cmd")+ext, []byte(res+"\n"), 0o644)
			}
		}
	}()
}

// demoCommand runs one command on the main thread and says what it did.
func demoCommand(a *app, line string) string {
	f := strings.Fields(line)
	if len(f) == 0 {
		return "error: empty"
	}
	arg := func(i int) string {
		if i < len(f) {
			return f[i]
		}
		return ""
	}
	switch f[0] {
	case "fire": // fire transcribe|translate|rewrite: the feature's first shortcut, tapped
		// Only into the apps the script works in (AURALINK_DEMO_TARGETS):
		// another app that came to the front gets nothing.
		if t := os.Getenv("AURALINK_DEMO_TARGETS"); t != "" && !slices.Contains(strings.Split(t, ","), a.front.BundleID) && !a.answer.open {
			return "error: " + a.front.BundleID + " is in front"
		}
		a.hotkeyFired(proto.HotkeyEventParams{Binding: hotkey.ID(arg(1), 0), Phase: proto.PhaseTrigger})
	case "esc":
		a.hotkeyFired(proto.HotkeyEventParams{Binding: proto.HotkeyEscape, Phase: proto.PhaseTrigger})
	case "take": // take <name>: the services' requests are recorded or played back under it
		demoRec.setTake(arg(1))
	case "nav": // nav /history, /settings/audio
		a.navigate(arg(1))
	case "close-settings":
		a.settingsOpen = false
	case "lang-menu":
		a.toggleLangMenu()
	case "lang": // lang ja: chosen in the card's menu
		a.chooseLanguage(arg(1))
	case "insert":
		a.insertAnswer()
	case "close-card":
		a.closeAnswer()
	case "hist": // hist <tab> [row]: the History tab, and the entry shown
		tab, _ := strconv.Atoi(arg(1))
		a.hist.tab = tab
		a.loadHistory()
		if row, err := strconv.Atoi(arg(2)); err == nil {
			go func() {
				time.Sleep(400 * time.Millisecond)
				a.post(func() {
					if row < len(a.hist.list) {
						a.hist.sel = a.hist.list[row].ID
					}
				})
			}()
		}
	case "ae": // ae <n>: the app enhancement group shown
		n, _ := strconv.Atoi(arg(1))
		if n < len(a.aeRules.Groups) {
			a.aePage.sel = a.aeRules.Groups[n].ID
		}
	case "window": // window show|hide
		if a.win != nil {
			if arg(1) == "hide" {
				a.win.Hide()
			} else {
				a.win.Show()
			}
		}
	case "prune": // the takes' older entries: one of each kept
		go func() {
			ctx := context.Background()
			hs := a.lib.store.History()
			list, _ := hs.List(ctx, store.HistoryQuery{})
			seen := map[string]bool{}
			for _, e := range list { // newest first
				k := strings.Join([]string{string(e.Kind), e.SourceText, e.RawText, e.PromptText, e.TargetLang}, "\x00")
				if seen[k] {
					hs.Delete(ctx, e.ID)
				}
				seen[k] = true
			}
			a.post(a.historyChanged)
		}()
	case "state":
		return fmt.Sprintf("state=%s card=%v open=%v lang=%v front=%s", a.sess.State, a.sess.Card, a.answer.open, a.answer.langOpen, a.front.BundleID)
	case "quit":
		go func() { time.Sleep(100 * time.Millisecond); mygo.App.Quit() }()
	default:
		return "error: unknown command " + f[0]
	}
	return "ok " + line
}
