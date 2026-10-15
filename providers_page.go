package main

// The Model services page (/providers, plan §3.1): the speech (ASR) and
// language (LLM) services Auralink sends audio and text to. Two tabs;
// each lists which service each feature uses, then a card per service
// (its state, model, saved key and a connection test). A card opens the
// service's dialog: its key, endpoint, model and, for speech, its
// recognition prompt. The status bar shows what Transcribe uses.
//
// Keys live in the Keychain only (keyring, internal/secrets): the page
// keeps their masks ("sk-…wxyz"), read in the background, never a key.
// What to build and test is internal/providers'.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/llm"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/secrets"
	"github.com/hehehai/Auralink/internal/settings"
)

// keyring keeps the providers' keys, by Keychain account ("asr.doubao").
// Its calls may block: off the main thread only.
type keyring interface {
	Get(account string) (string, error)
	Set(account, label, value string) error // "" deletes
}

// keychain is the macOS Keychain (internal/secrets).
type keychain struct{}

func (keychain) Get(account string) (string, error) { return secrets.Secret(account, "").Get() }
func (keychain) Set(account, label, value string) error {
	return secrets.Secret(account, label).Set(value)
}

// newKeyring is the keyring a new app uses; tests swap in a memory one.
var newKeyring = func() keyring { return keychain{} }

// providersState is the Model services page's state.
type providersState struct {
	keys keyring
	// provTab: 0 speech, 1 language models.
	provTab int
	// keyMasks are the saved keys' masks by account, "" for none; an
	// account not in it was not read yet.
	keyMasks map[string]string
	// provTests are the last connection tests, by account.
	provTests map[string]providerTest
	// featSel holds the "used by" selects' labels, featSeen what they
	// were last set from.
	featSel  [2][3]string
	featSeen [2]string
	provDlg  providerDialog
}

// providerTest is a connection test running or done.
type providerTest struct {
	running bool
	res     providers.TestResult
}

// providerDialog is the open service dialog's form.
type providerDialog struct {
	open               bool
	kind               providers.Kind
	id                 string
	exists             bool // the service was set up before
	name, key          string
	endpoint, appID    string
	model, customModel string // model: the select's label
	prompt             string
	enabled            bool
	err, errMore       string // a save or check error
	saving, removeKey  bool
	test               providerTest
}

// openProviders reads the masks of the keys of every service set up.
func (a *app) openProviders() {
	if a.keys == nil {
		a.keys = newKeyring()
	}
	a.keyMasks = map[string]string{}
	a.provTests = map[string]providerTest{}
	var accounts []string
	for _, kind := range []providers.Kind{providers.ASR, providers.LLM} {
		for _, p := range providerList(a.prefs, kind) {
			accounts = append(accounts, providers.Account(kind, p.ID))
		}
	}
	a.loadKeyMasks(accounts)
}

func providerList(s settings.Settings, kind providers.Kind) []settings.Provider {
	if kind == providers.ASR {
		return s.Providers.ASR
	}
	return s.Providers.LLM
}

// loadKeyMasks reads the keys of accounts from the keyring, in the
// background, and keeps their masks.
func (a *app) loadKeyMasks(accounts []string) {
	if len(accounts) == 0 {
		return
	}
	kr := a.keys
	a.async(func() {
		masks := map[string]string{}
		for _, acc := range accounts {
			v, err := kr.Get(acc)
			if err != nil {
				v = ""
			}
			masks[acc] = maskKey(v)
		}
		a.post(func() {
			for k, v := range masks {
				a.keyMasks[k] = v
			}
		})
	})
}

func maskKey(v string) string {
	if v == "" {
		return ""
	}
	return secrets.Mask(v)
}

// hasKey reports whether a key is saved for account (as last read).
func (a *app) hasKey(account string) bool { return a.keyMasks[account] != "" }

// providerName is a service's name in the UI language: the user's label,
// else the preset's (translated where it has a Chinese name).
func providerName(kind providers.Kind, p settings.Provider) string {
	if p.Name != "" {
		return p.Name
	}
	pr, _ := providers.PresetOf(kind, p)
	return presetName(pr)
}

func presetName(pr providers.Preset) string {
	if pr.Kind == providers.ASR {
		switch pr.ID {
		case "doubao":
			return i18n.T("Doubao (Volcengine)")
		case "aliyun":
			return i18n.T("Alibaba Cloud Model Studio")
		case "openai":
			return i18n.T("OpenAI-compatible")
		}
	}
	switch pr.ID {
	case "dashscope":
		return i18n.T("Qwen (Alibaba Cloud Model Studio)")
	case "dashscope-intl":
		return i18n.T("Qwen (Model Studio international)")
	case "volcengine":
		return i18n.T("Volcengine Ark (Doubao)")
	case "moonshot":
		return i18n.T("Moonshot (Kimi)")
	case "moonshot-intl":
		return i18n.T("Moonshot (Kimi, international)")
	case "zhipu":
		return i18n.T("Zhipu GLM")
	case "stepfun":
		return i18n.T("StepFun")
	case "mimo":
		return i18n.T("Xiaomi MiMo")
	case "siliconflow":
		return i18n.T("SiliconFlow")
	case "siliconflow-intl":
		return i18n.T("SiliconFlow (international)")
	case "custom":
		return i18n.T("Custom (OpenAI-compatible)")
	}
	if pr.Name != "" {
		return pr.Name
	}
	return pr.ID
}

// keyLabel is what a service calls its key.
func keyLabel(kind providers.Kind, id string) string {
	if kind == providers.ASR && id == "doubao" {
		return i18n.T("Access Token or API key")
	}
	return i18n.T("API key")
}

// modeLabel is the chip of a service: how it works.
func modeLabel(pr providers.Preset) string {
	switch pr.Mode {
	case providers.ModeStream:
		return i18n.T("Streaming")
	case providers.ModeFile:
		return i18n.T("Upload")
	case "anthropic":
		return "Anthropic"
	}
	return i18n.T("OpenAI API")
}

// ---- The page ----

func (a *app) providersPage(c *ui.Context) {
	t := c.Theme()
	pageHeader(c, i18n.T("Model services"), func() {
		if ghostIconButton(c.Key("prov-network"), icons.WiFiRouterMinimalistic, i18n.T("Network")).Label(i18n.T("Network settings")).Clicked() {
			a.openSettings("/network")
		}
	})
	kind := providers.ASR
	if a.provTab == 1 {
		kind = providers.LLM
	}
	ui.Scroll(c.Key("prov-scroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(0, px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).MaxWidth(px(t, 880)).Children(func() {
			caption(c, i18n.T("Auralink sends your audio and text only to the services you set up here, and has no server of its own. Keys stay in this Mac's Keychain."))
			segmentedFit(c.Key("prov-tab"), &a.provTab, i18n.T("Speech recognition"), i18n.T("Language models")).Label(i18n.T("Kind of service"))
			a.providerUse(c, kind)
			a.providerCards(c, kind)
		})
	})
	a.providerDialogView(c)
}

// providerUse is the "used by" section: which service each feature uses.
func (a *app) providerUse(c *ui.Context, kind providers.Kind) {
	t := c.Theme()
	ready := providers.Ready(a.prefs, kind, a.hasKey)
	ti := 0
	if kind == providers.LLM {
		ti = 1
	}
	auto := i18n.T("Automatic")
	if len(ready) > 0 {
		auto = i18n.T("Automatic: %s", providerName(kind, ready[0]))
	}
	opts := []string{auto}
	for _, p := range ready {
		opts = append(opts, providerName(kind, p))
	}
	feats := []struct{ id, title, info string }{
		{providers.Transcribe, i18n.T("Transcribe"), ""},
		{providers.Translate, i18n.T("Translate"), ""},
		{providers.Rewrite, i18n.T("Rewrite"), ""},
	}
	// Set the selects from the settings when they (or the choices) change.
	var wants [3]string
	for i, f := range feats {
		ff := providers.FeatureOf(a.prefs, f.id)
		wants[i] = ff.ASR
		if kind == providers.LLM {
			wants[i] = ff.LLM
		}
	}
	seen := strings.Join(wants[:], "\x00") + "\x01" + strings.Join(opts, "\x00")
	if a.featSeen[ti] != seen {
		a.featSeen[ti] = seen
		for i := range feats {
			a.featSel[ti][i] = auto
			for _, p := range ready {
				if p.ID == wants[i] {
					a.featSel[ti][i] = providerName(kind, p)
				}
			}
		}
	}
	tip := i18n.T("Automatic uses the first service that is ready, in the order of the cards below.")
	sectionTitleTip(c, i18n.T("Used by"), tip)
	if len(ready) == 0 {
		notice(c, "prov-none-"+string(kind), noticeWarn, noneReadyText(kind), "", nil, nil)
		return
	}
	for i, f := range feats {
		settingRow(c, "prov-use-"+string(kind)+"-"+f.id, f.title, f.info, func() {
			sel := ui.Select(c.Key("prov-use-select-"+string(kind)+"-"+f.id), &a.featSel[ti][i], opts).Label(i18n.T("Service for %s", f.title)).Width(px(t, 300))
			if sel.Changed() {
				id := ""
				if j := slices.Index(opts, a.featSel[ti][i]); j > 0 {
					id = ready[j-1].ID
				}
				a.updateSettings(func(s *settings.Settings) { setFeatureProvider(s, kind, f.id, id) })
			}
		})
	}
}

func noneReadyText(kind providers.Kind) string {
	if kind == providers.ASR {
		return i18n.T("No speech service is ready: set one up below to transcribe, translate and rewrite by voice.")
	}
	return i18n.T("No language model is ready: set one up below to clean up, translate and rewrite text.")
}

// setFeatureProvider sets a feature's service of kind.
func setFeatureProvider(s *settings.Settings, kind providers.Kind, feature, id string) {
	f := &s.Features.Transcribe.Feature
	switch feature {
	case providers.Translate:
		f = &s.Features.Translate.Feature
	case providers.Rewrite:
		f = &s.Features.Rewrite.Feature
	}
	if kind == providers.ASR {
		f.ASR = id
	} else {
		f.LLM = id
	}
}

// providerCards lists kind's services: those set up first, then the rest.
func (a *app) providerCards(c *ui.Context, kind providers.Kind) {
	t := c.Theme()
	var mine, rest []providers.Preset
	for _, pr := range providers.Presets(kind) {
		if _, ok := providers.Setup(a.prefs, kind, pr.ID); ok {
			mine = append(mine, pr)
		} else {
			rest = append(rest, pr)
		}
	}
	room := min(a.pageWidth(c), px(t, 880)) - px(t, 2*sp6) - 1 // the column's padding
	cols := max(1, int((room+px(t, sp3))/(px(t, 250)+px(t, sp3))))
	cardW := (room - px(t, sp3)*float32(cols-1)) / float32(cols)
	grid := func(key string, list []providers.Preset) {
		ui.Row(c.Key(key)).Wrap().Gap(px(t, sp3)).AlignItems(ui.Start).Children(func() {
			for _, pr := range list {
				a.providerCard(c, kind, pr, cardW)
			}
		})
	}
	if len(mine) > 0 {
		sectionTitle(c, i18n.T("Set up"))
		grid("prov-mine-"+string(kind), mine)
	}
	if len(rest) > 0 {
		title := i18n.T("Available")
		if len(mine) == 0 {
			title = i18n.T("Services")
		}
		sectionTitle(c, title)
		grid("prov-rest-"+string(kind), rest)
	}
}

// providerState is a card's state line: its dot's colour and text.
func (a *app) providerState(c *ui.Context, kind providers.Kind, p settings.Provider, configured bool) (ui.Color, string) {
	t := c.Theme()
	k := tok(t)
	acc := providers.Account(kind, p.ID)
	switch providers.Missing(kind, p, configured, a.hasKey(acc)) {
	case providers.MissingSetup:
		return k.G[5], i18n.T("Not set up")
	case providers.MissingOff:
		return k.G[5], i18n.T("Off")
	case providers.MissingKey:
		return k.Warning, i18n.T("No key saved")
	case providers.MissingEndpoint:
		return k.Warning, i18n.T("No endpoint")
	}
	if tr, ok := a.provTests[acc]; ok && !tr.running && tr.res.Err != nil {
		return k.Danger, i18n.T("Test failed")
	}
	return k.Success, i18n.T("Ready")
}

// providerCard is a service's card: its state dot, name and model; once
// set up, its key's mask, its last test and a Test button. A click opens
// its dialog.
func (a *app) providerCard(c *ui.Context, kind providers.Kind, pr providers.Preset, w float32) {
	t := c.Theme()
	k := tok(t)
	p, configured := providers.Setup(a.prefs, kind, pr.ID)
	name := providerName(kind, p)
	acc := providers.Account(kind, p.ID)
	dot, state := a.providerState(c, kind, p, configured)
	missing := providers.Missing(kind, p, configured, a.hasKey(acc))
	b := ui.ButtonBase(c.Key("prov-card-"+acc)).Width(w).Padding(px(t, sp3), px(t, sp4)).Radius(px(t, radiusCard)).Border(1, k.Line).
		Label(name).Tooltip(i18n.T("Set up %s", name)).AlignItems(ui.Stretch).Shrink(0)
	if b.Hovered() {
		b.Background(k.Subtle)
	}
	b.Children(func() {
		ui.Column(c).Gap(px(t, 6)).Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Height(px(t, 22)).Children(func() {
				statusDot(c, dot)
				ui.Text(c, name).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				if kind == providers.ASR {
					tagChip(c, modeLabel(pr))
				}
			})
			model := providers.Model(kind, p)
			if model == "" {
				model = i18n.T("Model not chosen")
			}
			truncText(c, model).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			if !configured {
				return // the rest shows once it is set up
			}
			ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Height(px(t, 30)).Children(func() {
				mask, read := a.keyMasks[acc]
				keyText, keyCol := mask, k.Text2
				switch {
				case mask == "" && !read:
					keyText = "…"
				case mask == "" && missing == providers.MissingKey:
					keyText, keyCol = state, k.Warning
				case mask == "":
					keyText, keyCol = i18n.T("No key"), k.Text3
				}
				ui.Icon(c, icons.KeyMinimalisticSquare.Linear).Size(px(t, 14), px(t, 14)).TextColor(keyCol).Shrink(0)
				ui.Text(c, keyText).FontSize(px(t, fsCaption)).TextColor(keyCol).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
				switch missing {
				case "":
					a.testMark(c, acc)
					tr := a.provTests[acc]
					if ghostButton(c.Key("prov-test-"+acc), i18n.T("Test")).Label(i18n.T("Test %s", name)).Disabled(tr.running).Clicked() {
						a.testProvider(kind, p)
					}
				case providers.MissingKey:
				default:
					ui.Text(c, state).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Shrink(0)
				}
			})
		})
	})
	if b.Clicked() {
		a.openProviderDialog(kind, pr.ID)
	}
}

// testMark is a ready card's test state: a spinner while it runs, its
// result once done.
func (a *app) testMark(c *ui.Context, acc string) {
	t := c.Theme()
	k := tok(t)
	tr, ok := a.provTests[acc]
	switch {
	case ok && tr.running:
		ui.Spinner(c).Size(px(t, 14), px(t, 14))
	case ok && tr.res.Err == nil:
		statusMark(c, icons.CheckCircle, k.Success, i18n.T("%d ms", tr.res.Latency.Milliseconds())).Shrink(0)
	case ok:
		msg, more := providerErrorText(tr.res.Err)
		statusMark(c, icons.CloseCircle, k.Danger, i18n.T("Failed")).Shrink(0).Tooltip(msg + "\n" + more)
	}
}

// testProvider runs a connection test of a saved setup.
func (a *app) testProvider(kind providers.Kind, p settings.Provider) {
	acc := providers.Account(kind, p.ID)
	a.provTests[acc] = providerTest{running: true}
	kr, proxy := a.keys, a.prefs.Network.Proxy
	a.async(func() {
		key, err := kr.Get(acc)
		res := providers.TestResult{Err: err}
		if err == nil {
			res = runProviderTest(context.Background(), kind, p, providers.Env{Key: key, Proxy: proxy})
		}
		a.post(func() { a.provTests[acc] = providerTest{res: res} })
	})
}

// runProviderTest is providers.Test; tests swap it for a fake.
var runProviderTest = providers.Test

// ---- The dialog ----

func (a *app) openProviderDialog(kind providers.Kind, id string) {
	pr, _ := providers.LookupPreset(kind, id)
	p, exists := providers.Setup(a.prefs, kind, id)
	d := providerDialog{open: true, kind: kind, id: id, exists: exists, enabled: !exists || p.Enabled,
		name: p.Name, endpoint: p.Endpoint, appID: p.AppID, prompt: p.Prompt}
	model := p.Model
	if model == "" {
		model = pr.Model
	}
	d.model = model
	if model != "" && !slices.Contains(pr.Models, model) {
		d.model, d.customModel = i18n.T("Other model…"), model
	}
	if model == "" {
		d.model = i18n.T("Other model…")
	}
	a.provDlg = d
	acc := providers.Account(kind, id)
	if _, read := a.keyMasks[acc]; !read {
		a.loadKeyMasks([]string{acc})
	}
}

// dialogSetup is the setup the dialog's form describes.
func (d *providerDialog) setup() settings.Provider {
	pr, _ := providers.LookupPreset(d.kind, d.id)
	p := settings.Provider{ID: d.id, Enabled: d.enabled, Endpoint: strings.TrimSpace(d.endpoint), AppID: strings.TrimSpace(d.appID), Prompt: strings.TrimSpace(d.prompt)}
	if pr.Custom {
		p.Name = strings.TrimSpace(d.name)
	}
	model := d.model
	if model == i18n.T("Other model…") {
		model = strings.TrimSpace(d.customModel)
	}
	if model != pr.Model {
		p.Model = model
	}
	return p
}

func (a *app) providerDialogView(c *ui.Context) {
	d := &a.provDlg
	if !d.open {
		return
	}
	t := c.Theme()
	pr, _ := providers.LookupPreset(d.kind, d.id)
	acc := providers.Account(d.kind, d.id)
	name := presetName(pr)
	sub := i18n.T("Speech recognition · %s", modeLabel(pr))
	if d.kind == providers.LLM {
		sub = i18n.T("Language model · %s", modeLabel(pr))
	}
	dialogFrame(c, "prov-dialog", &d.open, name, sub, 560, func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
			what := i18n.T("Your recordings are sent to %s to be recognised.", name)
			switch {
			case d.kind == providers.ASR && pr.Mode == providers.ModeFile:
				what = i18n.T("Your recordings are sent to the service at the endpoint below to be recognised.")
			case d.kind == providers.LLM && pr.Custom:
				what = i18n.T("Recognised and selected text is sent to the service at the endpoint below.")
			case d.kind == providers.LLM:
				what = i18n.T("Recognised and selected text is sent to %s.", name)
			}
			caption(c, what).Grow(1).Basis(px(t, 240))
			if pr.KeyURL != "" {
				ui.Link(c.Key("prov-keyurl"), i18n.T("Get a key"), pr.KeyURL).FontSize(px(t, fsCaption))
			}
		})
		settingRow(c, "prov-enabled", i18n.T("Use this service"), i18n.T("Off, the features skip it but keep its setup and key."), func() {
			ui.Switch(c.Key("prov-enabled-switch"), &d.enabled).Label(i18n.T("Use this service"))
		})
		if pr.Custom {
			fieldInfo(c, i18n.T("Name"), "", func() {
				ui.TextInput(c.Key("prov-name"), &d.name).Placeholder(name).Label(i18n.T("Name"))
			})
		}
		if pr.AppID {
			fieldInfo(c, i18n.T("App ID"), i18n.T("From the app in the Volcengine speech console, with its Access Token below. A key of the newer console needs no App ID."), func() {
				ui.TextInput(c.Key("prov-appid"), &d.appID).Placeholder(i18n.T("Optional")).Label(i18n.T("App ID"))
			})
		}
		mask := a.keyMasks[acc]
		fieldInfo(c, keyLabel(d.kind, d.id), i18n.T("Saved in this Mac's Keychain, never in Auralink's settings or logs."), func() {
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ph := i18n.T("Paste the key")
				if mask != "" && !d.removeKey {
					ph = i18n.T("Saved: %s · paste a new one to replace it", mask)
				}
				ui.TextInput(c.Key("prov-key"), &d.key).Password().Placeholder(ph).Label(keyLabel(d.kind, d.id)).Grow(1).MinWidth(0)
				if mask != "" && !d.removeKey && ghostButton(c.Key("prov-key-remove"), i18n.T("Remove")).Label(i18n.T("Remove the saved key")).Clicked() {
					d.removeKey, d.key = true, ""
				}
			})
			if d.removeKey {
				caption(c, i18n.T("The saved key is removed when you save."))
			}
		})
		epInfo := i18n.T("Leave empty for the service's own. Only https:// (wss:// for streaming) addresses, or http:// to this Mac.")
		if d.kind == providers.ASR && pr.Mode == providers.ModeFile {
			epInfo = i18n.T("Any OpenAI-compatible transcription address (Groq, SiliconFlow…), or its API root ending in /v1. Only https://, or http:// to this Mac.")
		}
		fieldInfo(c, i18n.T("Endpoint"), epInfo, func() {
			ph := pr.Endpoint
			if ph == "" {
				ph = "https://…/v1"
			}
			ui.TextInput(c.Key("prov-endpoint"), &d.endpoint).Placeholder(ph).Label(i18n.T("Endpoint"))
		})
		fieldInfo(c, i18n.T("Model"), "", func() {
			opts := append(slices.Clone(pr.Models), i18n.T("Other model…"))
			if !slices.Contains(opts, d.model) {
				opts = append([]string{d.model}, opts...)
			}
			ui.Select(c.Key("prov-model"), &d.model, opts).Label(i18n.T("Model")).Width(px(t, 320))
			if d.model == i18n.T("Other model…") {
				ui.TextInput(c.Key("prov-model-custom"), &d.customModel).Placeholder(i18n.T("Model id")).Label(i18n.T("Model id"))
			}
		})
		if pr.Prompt {
			fieldInfo(c, i18n.T("Recognition prompt"), i18n.T("Words about what you say (topics, names, how to spell them) that help recognition. Your dictionary's terms are added to it."), func() {
				ui.TextArea(c.Key("prov-prompt"), &d.prompt).Placeholder(i18n.T("Optional")).Label(i18n.T("Recognition prompt")).MinHeight(px(t, 64))
			})
		}
		switch {
		case d.err != "":
			notice(c, "prov-dlg-err", noticeError, d.err, d.errMore, nil, func() { d.err, d.errMore = "", "" })
		case d.test.running:
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(px(t, 14), px(t, 14))
				caption(c, i18n.T("Testing…"))
			})
		case !d.test.res.At.IsZero() && d.test.res.Err == nil:
			notice(c, "prov-dlg-ok", noticeOK, i18n.T("It works (%d ms).", d.test.res.Latency.Milliseconds()), "", nil, func() { d.test = providerTest{} })
		case !d.test.res.At.IsZero():
			msg, more := providerErrorText(d.test.res.Err)
			notice(c, "prov-dlg-fail", noticeError, msg, more, nil, func() { d.test = providerTest{} })
		}
	}, func() {
		if ui.Button(c.Key("prov-dlg-test"), i18n.T("Test connection")).Disabled(d.test.running || d.saving).Clicked() {
			a.testDialog()
		}
		ui.Spacer(c)
		if ghostButton(c.Key("prov-dlg-cancel"), i18n.T("Cancel")).Clicked() {
			d.open = false
		}
		if primaryButton(c.Key("prov-dlg-save"), i18n.T("Save")).Disabled(d.saving).Clicked() {
			a.saveProviderDialog()
		}
	})
}

// dialogKey is the key the dialog's test uses: the one typed, else the
// saved one (read in the background), none when it is to be removed.
func (a *app) dialogKey(acc string) func() (string, error) {
	typed, remove, kr := strings.TrimSpace(a.provDlg.key), a.provDlg.removeKey, a.keys
	return func() (string, error) {
		switch {
		case typed != "":
			return typed, nil
		case remove:
			return "", nil
		}
		return kr.Get(acc)
	}
}

// testDialog tests what the dialog's form describes, saved or not.
func (a *app) testDialog() {
	d := &a.provDlg
	kind, p := d.kind, d.setup()
	p.Enabled = true
	if err := providers.CheckEndpoint(kind, p); err != nil {
		d.err, d.errMore = providerErrorText(err)
		return
	}
	d.err, d.test = "", providerTest{running: true}
	key, proxy, id := a.dialogKey(providers.Account(kind, p.ID)), a.prefs.Network.Proxy, d.id
	a.async(func() {
		k, err := key()
		res := providers.TestResult{Err: err, At: time.Now()}
		if err == nil {
			res = runProviderTest(context.Background(), kind, p, providers.Env{Key: k, Proxy: proxy})
		}
		a.post(func() {
			if a.provDlg.open && a.provDlg.id == id && a.provDlg.kind == kind {
				a.provDlg.test = providerTest{res: res}
			}
			a.provTests[providers.Account(kind, p.ID)] = providerTest{res: res}
		})
	})
}

// saveProviderDialog checks the form, saves the key (when one was typed
// or removed) and then the setup.
func (a *app) saveProviderDialog() {
	d := &a.provDlg
	kind, p := d.kind, d.setup()
	if err := providers.CheckEndpoint(kind, p); err != nil {
		d.err, d.errMore = providerErrorText(err)
		return
	}
	acc := providers.Account(kind, p.ID)
	key, remove := strings.TrimSpace(d.key), d.removeKey
	label := "Auralink " + providerName(kind, p)
	d.saving, d.err = true, ""
	kr := a.keys
	a.async(func() {
		var err error
		switch {
		case key != "":
			err = kr.Set(acc, label, key)
		case remove:
			err = kr.Set(acc, label, "")
		}
		a.post(func() {
			a.provDlg.saving = false
			if err != nil {
				a.provDlg.err, a.provDlg.errMore = i18n.T("Could not save the key in the Keychain."), err.Error()
				return
			}
			switch {
			case key != "":
				a.keyMasks[acc] = maskKey(key)
			case remove:
				a.keyMasks[acc] = ""
			}
			delete(a.provTests, acc)
			a.updateSettings(func(s *settings.Settings) { providers.Put(s, kind, p) })
			a.provDlg = providerDialog{}
			a.toast(i18n.T("%s saved", providerName(kind, p)))
		})
	})
}

// providerErrorText says what went wrong with a service, and its detail
// (the service's own message, for the ⓘ).
func providerErrorText(err error) (msg, more string) {
	if err == nil {
		return "", ""
	}
	more = err.Error()
	var ae *asr.Error
	if errors.As(err, &ae) {
		switch ae.Kind {
		case asr.KindAuth:
			if ae.Provider == "doubao" {
				return i18n.T("The service refused the key. Check it (and the App ID) and try again."), more
			}
			return i18n.T("The service refused the key. Check it and try again."), more
		case asr.KindQuota:
			return i18n.T("The service refused for now: out of quota, rate limited, or the model is not enabled for your account."), more
		case asr.KindNetwork:
			return i18n.T("Could not reach the service. Check the network, the proxy and the endpoint."), more
		case asr.KindServer:
			return i18n.T("The service had a problem on its side. Try again in a moment."), more
		case asr.KindAudio:
			return i18n.T("The service did not accept the audio."), more
		case asr.KindCancelled:
			return i18n.T("Cancelled."), more
		case asr.KindConfig:
			return i18n.T("The setup is incomplete or wrong: check the key, the endpoint and the model."), more
		}
		return i18n.T("The service answered something Auralink does not understand. Check the endpoint and the model."), more
	}
	var le *llm.Error
	if errors.As(err, &le) {
		switch le.Code {
		case llm.CodeNoKey:
			return i18n.T("No key is saved for this service."), more
		case llm.CodeAuth:
			return i18n.T("The service refused the key. Check it and try again."), more
		case llm.CodeRateLimited, llm.CodeOverloaded:
			return i18n.T("The service refused for now: out of quota, rate limited, or busy."), more
		case llm.CodeModelNotFound:
			return i18n.T("The service does not know the model %s.", le.Params["model"]), more
		case llm.CodeTimeout:
			return i18n.T("The service did not answer in time."), more
		case llm.CodeNetwork:
			return i18n.T("Could not reach the service. Check the network, the proxy and the endpoint."), more
		case llm.CodeServer:
			return i18n.T("The service had a problem on its side. Try again in a moment."), more
		case llm.CodeConfig:
			return i18n.T("The setup is incomplete or wrong: check the endpoint (https:// only) and the model."), more
		case llm.CodeBadRequest, llm.CodeContextLength, llm.CodeFiltered:
			return i18n.T("The service refused the request."), more
		case llm.CodeCancelled:
			return i18n.T("Cancelled."), more
		}
		return i18n.T("The service answered something Auralink does not understand. Check the endpoint and the model."), more
	}
	return i18n.T("Could not reach the service. Check the network, the proxy and the endpoint."), more
}

// providerSummary is the status bar's: what Transcribe uses.
func (a *app) providerSummary() (asrName, llmName string, ok bool) {
	f := a.prefs.Features.Transcribe.Feature
	pa, okA := providers.Choose(a.prefs, providers.ASR, f.ASR, a.hasKey)
	pl, okL := providers.Choose(a.prefs, providers.LLM, f.LLM, a.hasKey)
	if okA {
		asrName = providerName(providers.ASR, pa)
	}
	if okL {
		llmName = providerName(providers.LLM, pl)
	}
	return asrName, llmName, okA && okL
}
