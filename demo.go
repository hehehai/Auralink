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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/Auralink/internal/appctx"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/permission"
	"github.com/hehehai/Auralink/internal/settings"
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
	demoMask = func(v string) string { return v[:min(3, len(v))] + "…••••" }
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

// ---- record and replay ----

var demoRec *replay

// replay is the services' transport: outside a take it calls; in a take
// it plays the take's n-th request back when it was recorded, else calls
// and records it.
type replay struct {
	dir, calls string
	base       http.RoundTripper
	mu         sync.Mutex
	take       string
	n          int
}

type recChunk struct {
	MS   int64  `json:"ms"`
	Data []byte `json:"data"`
}

type recording struct {
	URL      string     `json:"url"`
	Status   int        `json:"status"`
	Type     string     `json:"type"`
	HeaderMS int64      `json:"headerMs"`
	Chunks   []recChunk `json:"chunks"`
}

func (r *replay) setTake(name string) {
	r.mu.Lock()
	r.take, r.n = name, 0
	r.mu.Unlock()
}

func (r *replay) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	take, n := r.take, r.n
	r.n++
	r.mu.Unlock()
	url := req.URL.Host + req.URL.Path
	path := ""
	if take != "" {
		path = filepath.Join(r.dir, take, fmt.Sprintf("%02d.json", n))
		var rec recording
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &rec) == nil && rec.URL == url {
			if req.Body != nil {
				io.Copy(io.Discard, req.Body)
				req.Body.Close()
			}
			return r.play(req, rec)
		}
	}
	start := time.Now()
	resp, err := r.base.RoundTrip(req)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	r.logCall(fmt.Sprintf("%s\t%s\t%s\t%d\t%v", start.Format(time.RFC3339), req.URL.Host, take, status, err))
	if err != nil || path == "" || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	rec := recording{URL: url, Status: resp.StatusCode, Type: resp.Header.Get("Content-Type"), HeaderMS: time.Since(start).Milliseconds()}
	resp.Body = &recBody{rc: resp.Body, rec: rec, t0: time.Now(), path: path}
	return resp, nil
}

func (r *replay) logCall(line string) {
	f, err := os.OpenFile(r.calls, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	fmt.Fprintln(f, line)
	f.Close()
}

// play answers req with rec, at its pace.
func (r *replay) play(req *http.Request, rec recording) (*http.Response, error) {
	ctx := req.Context()
	select {
	case <-time.After(time.Duration(rec.HeaderMS) * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	pr, pw := io.Pipe()
	go func() {
		t0 := time.Now()
		for _, c := range rec.Chunks {
			if d := time.Duration(c.MS)*time.Millisecond - time.Since(t0); d > 0 {
				select {
				case <-time.After(d):
				case <-ctx.Done():
					pw.CloseWithError(ctx.Err())
					return
				}
			}
			if _, err := pw.Write(c.Data); err != nil {
				return
			}
		}
		pw.Close()
	}()
	h := http.Header{}
	h.Set("Content-Type", rec.Type)
	return &http.Response{StatusCode: rec.Status, Status: http.StatusText(rec.Status), Header: h, Body: pr,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Request: req, ContentLength: -1}, nil
}

// recBody records a response's body as it is read, and saves it at its
// end.
type recBody struct {
	rc   io.ReadCloser
	rec  recording
	t0   time.Time
	path string
	done bool
}

func (b *recBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.rec.Chunks = append(b.rec.Chunks, recChunk{MS: time.Since(b.t0).Milliseconds(), Data: append([]byte(nil), p[:n]...)})
	}
	if err == io.EOF && !b.done {
		b.done = true
		if data, e := json.Marshal(b.rec); e == nil {
			os.MkdirAll(filepath.Dir(b.path), 0o755)
			os.WriteFile(b.path, data, 0o644)
		}
	}
	return n, err
}

func (b *recBody) Close() error { return b.rc.Close() }

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
	case "state":
		return fmt.Sprintf("state=%s card=%v open=%v lang=%v front=%s", a.sess.State, a.sess.Card, a.answer.open, a.answer.langOpen, a.front.BundleID)
	case "quit":
		go func() { time.Sleep(100 * time.Millisecond); mygo.App.Quit() }()
	default:
		return "error: unknown command " + f[0]
	}
	return "ok " + line
}

// ---- sample data ----

// demoSeed fills an empty library with the sample data of
// $AURALINK_DEMO_LANG (en, zh).
func demoSeed(lib *library) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if list, err := lib.store.History().List(ctx, store.HistoryQuery{Limit: 1}); err != nil || len(list) > 0 {
		return
	}
	zh := os.Getenv("AURALINK_DEMO_LANG") == "zh"
	d := demoDataEN
	if zh {
		d = demoDataZH
	}
	err := lib.settings.Update(ctx, func(s *settings.Settings) {
		s.General.Language, s.General.MainLanguage, s.General.OtherLanguages = "en", "en", []string{"zh"}
		s.Features.Translate.Target = "en"
		if zh {
			s.General.Language, s.General.MainLanguage, s.General.OtherLanguages = "zh-Hans", "zh", []string{"en"}
			s.Features.Translate.Target = "zh"
		}
		s.Appearance.Theme = "light"
		s.Audio.Sounds = false
		s.Features.Translate.ShowCard = true
		s.Providers.ASR = []settings.Provider{{ID: "stepfun", Enabled: true}}
		s.Providers.LLM = []settings.Provider{{ID: "deepseek", Enabled: true}}
		s.Setup.Done, s.Setup.Tried = true, true
	})
	if err != nil {
		log.Printf("demo: settings: %v", err)
	}
	var rules appctx.Rules
	for i, g := range d.groups {
		rules.Groups = append(rules.Groups, appctx.Group{ID: fmt.Sprintf("demo-%d", i+1), Name: g.name, Enabled: true, Prompt: g.prompt,
			Apps: g.apps, URLs: g.urls, AutoKey: g.autoKey, Features: g.features})
	}
	if b, err := rules.Marshal(); err == nil {
		lib.store.Documents().Put(ctx, appctx.Key, b)
	}
	now := time.Now()
	for i, t := range d.terms {
		at := now.Add(-time.Duration(len(d.terms)-i) * 26 * time.Hour)
		lib.store.Dictionary().Put(ctx, store.Term{ID: core.NewID(at), Term: t.term, Aliases: t.aliases, Note: t.note, Enabled: true, CreatedAt: at, UpdatedAt: at})
	}
	for _, e := range d.history {
		at := now.Add(-e.ago)
		ent := store.Entry{ID: core.NewID(at), Kind: e.kind, CreatedAt: at, Status: store.StatusDone,
			AppBundle: e.app.BundleID, AppName: e.app.Name, URLHost: e.host,
			RawText: e.raw, SourceText: e.source, PromptText: e.prompt, Result: e.result, TargetLang: e.target,
			Delivery: "insert", ASRProvider: "stepfun", ASRModel: "step-asr", LLMProvider: "deepseek", LLMModel: "deepseek-flash",
			AudioMS: e.audioMS, SentMS: e.audioMS * 85 / 100, ASRMS: 600 + e.audioMS/12, LLMMS: 700 + int64(len(e.result))*3, DSP: "highpass+level", GainDB: 4.5}
		ent.TotalMS = ent.AudioMS + ent.ASRMS + ent.LLMMS + 180
		if e.group > 0 {
			g := rules.Groups[e.group-1]
			ent.RuleID, ent.RuleName, ent.RuleSource = g.ID, g.Name, string(appctx.SourceApp)
			if e.host != "" {
				ent.RuleSource = string(appctx.SourceURL)
			}
		}
		if e.kind == store.KindTranslate && e.source != "" || e.kind == store.KindRewrite && e.prompt == "" {
			ent.ASRProvider, ent.ASRModel, ent.AudioMS, ent.SentMS, ent.ASRMS, ent.DSP, ent.GainDB = "", "", 0, 0, 0, "", 0
			ent.TotalMS = ent.LLMMS + 150
		}
		if e.card {
			ent.Delivery = "card"
		}
		if err := lib.store.History().Put(ctx, ent); err != nil {
			log.Printf("demo: history: %v", err)
		}
	}
}

type demoGroup struct {
	name, prompt string
	apps         []appctx.App
	urls         []string
	autoKey      string
	features     appctx.Features
}

type demoTerm struct {
	term    string
	aliases []string
	note    string
}

type demoEntry struct {
	ago                                 time.Duration
	kind                                store.Kind
	app                                 proto.App
	host                                string
	group                               int // 1-based; 0 none
	raw, source, prompt, result, target string
	audioMS                             int64
	card                                bool
}

type demoData struct {
	groups  []demoGroup
	terms   []demoTerm
	history []demoEntry
}

var (
	appDiscord  = proto.App{BundleID: "com.hnc.Discord", Name: "Discord"}
	appTelegram = proto.App{BundleID: "ru.keepcoder.Telegram", Name: "Telegram"}
	appLark     = proto.App{BundleID: "com.electron.lark", Name: "Lark"}
	appWeChat   = proto.App{BundleID: "com.tencent.xinWeChat", Name: "WeChat"}
	appMail     = proto.App{BundleID: "com.apple.mail", Name: "Mail"}
	appCursor   = proto.App{BundleID: "com.todesktop.230313mzl4w4u92", Name: "Cursor"}
	appZed      = proto.App{BundleID: "dev.zed.Zed", Name: "Zed"}
	appXcode    = proto.App{BundleID: "com.apple.dt.Xcode", Name: "Xcode"}
	appTextEdit = proto.App{BundleID: "com.apple.TextEdit", Name: "TextEdit"}
	appObsidian = proto.App{BundleID: "md.obsidian", Name: "Obsidian"}
	appNotes    = proto.App{BundleID: "com.apple.Notes", Name: "Notes"}
	appChrome   = proto.App{BundleID: "com.google.Chrome", Name: "Google Chrome"}
	appSafari   = proto.App{BundleID: "com.apple.Safari", Name: "Safari"}
	appFigma    = proto.App{BundleID: "com.figma.Desktop", Name: "Figma"}
)

func asApps(as ...proto.App) []appctx.App {
	out := make([]appctx.App, len(as))
	for i, a := range as {
		out[i] = appctx.App{BundleID: a.BundleID, Name: a.Name}
	}
	return out
}

const (
	min_ = time.Minute
	hr   = time.Hour
	day  = 24 * time.Hour
)

var demoDataEN = demoData{
	groups: []demoGroup{
		{name: "Team chat", prompt: "A team chat: short and casual, no greeting or sign-off, one message per thought.",
			apps: asApps(appDiscord, appTelegram, appLark), urls: []string{"app.slack.com"}, autoKey: "Return",
			features: appctx.Features{Transcribe: true, Rewrite: true}},
		{name: "Email", prompt: "An email: complete sentences in a friendly, professional tone. Keep names, dates and times exactly as said.",
			apps: asApps(appMail), urls: []string{"mail.google.com", "outlook.office.com"},
			features: appctx.Features{Transcribe: true, Translate: true, Rewrite: true}},
		{name: "Code & docs", prompt: "Technical writing: keep code identifiers, file names, commands and flags exactly as spoken, in backticks.",
			apps: asApps(appCursor, appZed, appXcode), urls: []string{"github.com", "*.notion.so/*"},
			features: appctx.Features{Transcribe: true, Rewrite: true}},
		{name: "Notes", prompt: "Notes and documents: clear punctuation, a new paragraph for each separate point.",
			apps:     asApps(appTextEdit, appObsidian, appNotes),
			features: appctx.Features{Transcribe: true, Rewrite: true}},
	},
	terms: []demoTerm{
		{"Auralink", []string{"aura link", "aural link"}, "The app's name"},
		{"StepFun", []string{"step fun"}, ""},
		{"DeepSeek", []string{"deep seek"}, ""},
		{"Kubernetes", []string{"cooper netties", "kuber nets"}, ""},
		{"Figma", []string{"figma's", "fig ma"}, ""},
		{"OKRs", []string{"okay ours", "o k r s"}, "Objectives and key results"},
		{"Priya", []string{"pria", "prea"}, "Design lead"},
		{"Joaquín", []string{"wakeen", "joaquin"}, "Backend, Madrid office"},
	},
	history: []demoEntry{
		{ago: 12 * min_, kind: store.KindTranscribe, app: appDiscord, group: 1,
			raw:    "um pushed the fix for the login timeout uh can someone check it on staging",
			result: "Pushed the fix for the login timeout, can someone check it on staging?", audioMS: 5200},
		{ago: 38 * min_, kind: store.KindRewrite, app: appMail, group: 2,
			source: "cant make it thurs, can we move the sync? sorry", prompt: "make it polite and suggest friday morning",
			result: "Sorry, I can't make Thursday. Could we move our sync to Friday morning?", audioMS: 3100},
		{ago: 55 * min_, kind: store.KindTranscribe, app: appCursor, group: 3,
			raw:    "rename load history to open history and keep the old name as an alias until the next release",
			result: "Rename `loadHistory` to `openHistory` and keep the old name as an alias until the next release.", audioMS: 6400},
		{ago: 1*hr + 20*min_, kind: store.KindTranslate, app: appChrome, host: "github.com", group: 0,
			source: "这个版本先灰度百分之十，周五再全量发布。",
			result: "We'll roll this version out to 10% first, then to everyone on Friday.", target: "en", card: true},
		{ago: 2 * hr, kind: store.KindTranscribe, app: appObsidian, group: 4,
			raw:    "ideas for onboarding show one real try of each feature then hide the checklist once everything is ready",
			result: "Ideas for onboarding: show one real try of each feature, then hide the checklist once everything is ready.", audioMS: 7300},
		{ago: 3*hr + 10*min_, kind: store.KindTranscribe, app: appTelegram, group: 1,
			raw:    "running five minutes late start without me",
			result: "Running five minutes late, start without me.", audioMS: 2400},
		{ago: 4 * hr, kind: store.KindTranslate, app: appFigma, group: 0,
			raw: "保存草稿", result: "Save draft", target: "en", audioMS: 1500},
		{ago: 5*hr + 30*min_, kind: store.KindRewrite, app: appNotes, group: 4,
			prompt: "write a short agenda for tomorrow's design review",
			result: "Design review agenda\n\n1. Onboarding flow: open questions\n2. History page: filters and search\n3. Next steps and owners", audioMS: 3600, card: true},
		{ago: 1*day + 2*hr, kind: store.KindTranscribe, app: appMail, group: 2,
			raw:    "hi priya thanks for the review i've updated the figma file with the new spacing",
			result: "Hi Priya, thanks for the review. I've updated the Figma file with the new spacing.", audioMS: 5600},
		{ago: 1*day + 5*hr, kind: store.KindTranscribe, app: appZed, group: 3,
			raw:    "the kubernetes job fails because the config map is missing the region",
			result: "The Kubernetes job fails because the ConfigMap is missing the region.", audioMS: 4800},
	},
}

var demoDataZH = demoData{
	groups: []demoGroup{
		{name: "团队聊天", prompt: "团队聊天：简短口语化，不要称呼和落款，一个想法一条消息。",
			apps: asApps(appWeChat, appLark, appTelegram), autoKey: "Return",
			features: appctx.Features{Transcribe: true, Rewrite: true}},
		{name: "邮件", prompt: "邮件：完整的句子，礼貌、专业；人名、日期和时间按原话保留。",
			apps: asApps(appMail), urls: []string{"mail.google.com", "exmail.qq.com"},
			features: appctx.Features{Transcribe: true, Translate: true, Rewrite: true}},
		{name: "代码与文档", prompt: "技术写作：代码标识符、文件名、命令和参数按原样保留，用反引号括起来。",
			apps: asApps(appCursor, appZed, appXcode), urls: []string{"github.com", "*.feishu.cn/docx/*"},
			features: appctx.Features{Transcribe: true, Rewrite: true}},
		{name: "笔记", prompt: "笔记和文档：标点清楚，不同的要点分段。",
			apps:     asApps(appTextEdit, appObsidian, appNotes),
			features: appctx.Features{Transcribe: true, Rewrite: true}},
	},
	terms: []demoTerm{
		{"Auralink", []string{"奥拉林克", "aura link"}, "产品名"},
		{"阶跃星辰", []string{"接跃星辰", "阶跃新城"}, ""},
		{"DeepSeek", []string{"迪普西克", "deep seek"}, ""},
		{"飞书", []string{"飞叔", "非书"}, ""},
		{"灰度发布", []string{"灰度", "回度发布"}, ""},
		{"Kubernetes", []string{"酷伯内特斯", "k8s"}, ""},
		{"OKR", []string{"欧凯阿", "o k r"}, "目标与关键结果"},
		{"王芃", []string{"王鹏", "王朋"}, "设计负责人"},
	},
	history: []demoEntry{
		{ago: 12 * min_, kind: store.KindTranscribe, app: appWeChat, group: 1,
			raw:    "嗯登录超时那个问题我已经修了那个谁帮忙在测试环境看一下",
			result: "登录超时的问题我已经修了，谁帮忙在测试环境看一下？", audioMS: 4800},
		{ago: 38 * min_, kind: store.KindRewrite, app: appMail, group: 2,
			source: "周四去不了，同步会能不能挪一下，不好意思", prompt: "改得礼貌一点，建议改到周五上午",
			result: "不好意思，周四我去不了。我们的同步会能否改到周五上午？", audioMS: 3000},
		{ago: 55 * min_, kind: store.KindTranscribe, app: appCursor, group: 3,
			raw:    "把 load history 改名成 open history 旧名字先留一个别名到下个版本",
			result: "把 `loadHistory` 改名为 `openHistory`，旧名字先保留为别名，到下个版本再删。", audioMS: 6100},
		{ago: 1*hr + 20*min_, kind: store.KindTranslate, app: appChrome, host: "github.com", group: 0,
			source: "We'll roll this version out to 10% first, then to everyone on Friday.",
			result: "这个版本先灰度 10%，周五再全量发布。", target: "zh", card: true},
		{ago: 2 * hr, kind: store.KindTranscribe, app: appObsidian, group: 4,
			raw:    "引导页的想法每个功能让用户真的试一次然后全部就绪以后就把清单藏起来",
			result: "引导页的想法：每个功能让用户真实地试一次，全部就绪以后就把清单藏起来。", audioMS: 6900},
		{ago: 3*hr + 10*min_, kind: store.KindTranscribe, app: appLark, group: 1,
			raw:    "我晚五分钟你们先开始",
			result: "我晚到五分钟，你们先开始。", audioMS: 2200},
		{ago: 4 * hr, kind: store.KindTranslate, app: appFigma, group: 0,
			raw: "save draft", result: "保存草稿", target: "zh", audioMS: 1400},
		{ago: 5*hr + 30*min_, kind: store.KindRewrite, app: appNotes, group: 4,
			prompt: "写一个明天设计评审的简短议程",
			result: "设计评审议程\n\n1. 引导流程：待定问题\n2. 历史页：筛选与搜索\n3. 下一步与负责人", audioMS: 3300, card: true},
		{ago: 1*day + 2*hr, kind: store.KindTranscribe, app: appMail, group: 2,
			raw:    "王芃你好谢谢你的评审我已经按新的间距更新了 figma 文件",
			result: "王芃你好，谢谢你的评审。我已经按新的间距更新了 Figma 文件。", audioMS: 5300},
		{ago: 1*day + 5*hr, kind: store.KindTranscribe, app: appZed, group: 3,
			raw:    "kubernetes 的任务失败了因为 config map 里少了 region",
			result: "Kubernetes 的任务失败了，因为 ConfigMap 里少了 `region`。", audioMS: 4600},
	},
}
