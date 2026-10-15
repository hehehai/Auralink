package main

// The Permissions page (/permissions, plan §6): what macOS lets Auralink
// do, why each permission matters, and how to grant it. The Microphone and
// Accessibility are read here (internal/permission); each browser's
// Automation permission, which app enhancement's URL rules need, is the
// helper's to read (automation.probe). The page reads them again when
// Auralink comes to the front and with Check again.

import (
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/permission"
)

// System Settings panes.
const (
	privacyMicrophone    = "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone"
	privacyAccessibility = "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
	privacyAutomation    = "x-apple.systempreferences:com.apple.preference.security?Privacy_Automation"
)

// browser is a browser whose tab URL app enhancement reads.
type browser struct{ id, name string }

// builtinBrowsers are the browsers Auralink knows (plan §2.2); the user
// adds others in settings.AppEnhancement.Browsers (M5).
var builtinBrowsers = []browser{
	{"com.apple.Safari", "Safari"},
	{"com.google.Chrome", "Google Chrome"},
	{"com.microsoft.edgemac", "Microsoft Edge"},
	{"com.brave.Browser", "Brave"},
	{"company.thebrowser.Browser", "Arc"},
}

// permissionsState is what the page last read.
type permissionsState struct {
	permSys permission.System
	perms   struct {
		mic     permission.State
		access  bool
		checked bool
	}
	automation   map[string]string // bundle id → proto.Automation*
	autoChecking bool
	autoErr      string
}

func (a *app) openPermissions() {
	if a.permSys == nil {
		a.permSys = permission.Native()
	}
}

// browsers are the built-in browsers and the user's.
func (a *app) browsers() []browser {
	out := slices.Clone(builtinBrowsers)
	for _, b := range a.prefs.AppEnhancement.Browsers {
		if !slices.ContainsFunc(out, func(x browser) bool { return x.id == b.BundleID }) {
			name := b.Name
			if name == "" {
				name = b.BundleID
			}
			out = append(out, browser{b.BundleID, name})
		}
	}
	return out
}

// recheckPermissions reads the Microphone and Accessibility (cheap
// system calls).
func (a *app) recheckPermissions() {
	a.perms.mic, a.perms.access, a.perms.checked = a.permSys.Microphone(), a.permSys.AccessibilityTrusted(), true
}

// probeAutomation asks the helper for the browsers' Automation states;
// prompt (with only, a bundle id) asks macOS to show its consent prompt.
func (a *app) probeAutomation(prompt bool, only []string) {
	if !a.hstatus.Has(proto.MethodAutomationProbe) {
		return
	}
	ids := only
	if ids == nil {
		for _, b := range a.browsers() {
			ids = append(ids, b.id)
		}
	}
	a.autoChecking = true
	helperCall(a, proto.MethodAutomationProbe, proto.AutomationProbeParams{BundleIDs: ids, Prompt: prompt}, 30*time.Second,
		func(r proto.AutomationProbeResult, err error) {
			a.autoChecking = false
			if err != nil {
				a.autoErr = err.Error()
				return
			}
			a.autoErr = ""
			if a.automation == nil {
				a.automation = map[string]string{}
			}
			for _, b := range r.Browsers {
				a.automation[b.BundleID] = b.State
			}
		})
}

// openPrivacy opens a pane of System Settings.
func (a *app) openPrivacy(c *ui.Context, url string) {
	if a.win == nil {
		c.Services().OpenURL(url)
		return
	}
	go mygo.Shell.OpenExternal(url)
}

func permissionText(p permission.State) (string, bool) {
	switch p {
	case permission.Granted:
		return i18n.T("Allowed"), true
	case permission.Denied:
		return i18n.T("Not allowed"), false
	case permission.Restricted:
		return i18n.T("Restricted by your organisation"), false
	case permission.NotDetermined:
		return i18n.T("Not asked yet"), false
	}
	return i18n.T("Unknown"), false
}

func automationText(state string) (string, bool) {
	switch state {
	case proto.AutomationGranted:
		return i18n.T("Allowed"), true
	case proto.AutomationDenied:
		return i18n.T("Not allowed"), false
	case proto.AutomationNotDetermined:
		return i18n.T("Not asked yet"), false
	case proto.AutomationNotRunning:
		return i18n.T("Open it to check"), false
	}
	return i18n.T("Unknown"), false
}

func (a *app) permissionsPage(c *ui.Context) {
	t := c.Theme()
	if !a.perms.checked {
		a.recheckPermissions()
	}
	pageHeader(c, i18n.T("Permissions"), func() {
		if secondaryIconButton(c.Key("perm-recheck"), icons.Refresh, i18n.T("Check again")).Label(i18n.T("Check again")).Clicked() {
			a.recheckPermissions()
			a.probeAutomation(false, nil)
		}
	})
	badge := func(text string, ok bool) {
		if ok {
			statusMark(c, icons.CheckCircle, t.Success, text)
		} else {
			statusMark(c, icons.DangerTriangle, t.Warning, text)
		}
	}
	ui.Scroll(c.Key("perm-scroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(0, px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).MaxWidth(px(t, 760)).Children(func() {
			caption(c, i18n.T("macOS asks you before Auralink may record or work with other apps. Each row says what needs it; you can change them any time in System Settings."))

			sectionTitle(c, i18n.T("This Mac"))
			micText, micOK := permissionText(a.perms.mic)
			settingRow(c, "perm-mic", i18n.T("Microphone"), i18n.T("Transcribe, translate and rewrite by voice record while you hold or tap their shortcut."), func() {
				badge(micText, micOK)
				if a.perms.mic == permission.NotDetermined {
					if ghostButton(c.Key("perm-mic-ask"), i18n.T("Ask")).Clicked() {
						sys := a.permSys
						a.async(func() { sys.RequestMicrophone(func(bool) { a.post(a.recheckPermissions) }) })
					}
				}
				if !micOK {
					if ghostIconButton(c.Key("perm-mic-open"), icons.SquareArrowRightUp, i18n.T("Open System Settings")).Label(i18n.T("Open System Settings")).Clicked() {
						a.openPrivacy(c, privacyMicrophone)
					}
				}
			})
			accText, accOK := i18n.T("Not allowed"), a.perms.access
			if accOK {
				accText = i18n.T("Allowed")
			}
			settingRow(c, "perm-access", i18n.T("Accessibility"), i18n.T("Lets Auralink type results where your cursor is, read the selected text, and use fn and other single keys as shortcuts. Without it results are copied for you to paste."), func() {
				badge(accText, accOK)
				if !accOK {
					if ghostButton(c.Key("perm-access-ask"), i18n.T("Ask")).Tooltip(i18n.T("Adds Auralink to the list in System Settings")).Clicked() {
						a.permSys.PromptAccessibility()
						a.recheckPermissions()
					}
					if ghostIconButton(c.Key("perm-access-open"), icons.SquareArrowRightUp, i18n.T("Open System Settings")).Label(i18n.T("Open System Settings")).Clicked() {
						a.openPrivacy(c, privacyAccessibility)
					}
				}
			})

			sectionTitleTip(c, i18n.T("Browsers"), i18n.T("App enhancement reads the address of the tab in front to match your URL rules. macOS asks once per browser; the address is never sent to a model."))
			if !a.hstatus.Has(proto.MethodAutomationProbe) {
				notice(c, "perm-no-helper", noticeWarn, i18n.T("Checking browsers needs the helper, which is not running."), "", func() {
					if ghostButton(c.Key("perm-helper-restart"), i18n.T("Restart the helper")).Clicked() {
						a.restartHelper()
					}
				}, nil)
			}
			if a.autoErr != "" {
				ui.Text(c, a.autoErr).FontSize(px(t, fsCaption)).TextColor(t.Danger).Selectable()
			}
			for _, b := range a.browsers() {
				state := a.automation[b.id]
				settingRow(c, "perm-browser-"+b.id, b.name, "", func() {
					if state == "" {
						caption(c, "—")
						return
					}
					text, ok := automationText(state)
					badge(text, ok)
					if state == proto.AutomationNotDetermined {
						if ghostButton(c.Key("perm-browser-ask"), i18n.T("Ask")).Disabled(a.autoChecking).Clicked() {
							a.probeAutomation(true, []string{b.id})
						}
					}
					if state == proto.AutomationDenied {
						if ghostIconButton(c.Key("perm-browser-open"), icons.SquareArrowRightUp, i18n.T("Open System Settings")).Label(i18n.T("Open System Settings")).Clicked() {
							a.openPrivacy(c, privacyAutomation)
						}
					}
				})
			}
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				infoTip(c.Key("perm-note"), i18n.T("After you change a permission, macOS may ask to quit and reopen Auralink. Updating an ad hoc signed build can make macOS forget Accessibility: turn it off and on again."))
				caption(c, i18n.T("Changed a permission? Check again."))
			})
		})
	})
}
