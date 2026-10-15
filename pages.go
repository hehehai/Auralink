package main

// The pages of the sidebar (plan §3.1). In M1 each is its header and an
// empty state that says what the page will hold; the milestones replace
// them one by one (Home and History M3, the features M3–M4, App
// enhancement and Dictionary M5). Permissions (permissions.go), Logs
// (shell_logs.go) and Model services (providers_page.go) work already.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
)

// pages builds the page the router shows.
func (a *app) pages(c *ui.Context) {
	a.router.View(c, func(r *ui.Route) {
		for _, p := range pages {
			if r.Match(p.path) {
				r.Title(pageTitle(p.path))
				a.page(c, p)
				return
			}
		}
		r.Title(i18n.T("Not found"))
		a.notFoundPage(c, r.Path())
	}).Grow(1).MinHeight(0)
}

// pageShown runs when the router goes to path: pages that read something
// when they show start it here.
func (a *app) pageShown(path string) {
	if path == "/permissions" {
		a.recheckPermissions()
		a.probeAutomation()
	}
}

// page builds one page of the sidebar.
func (a *app) page(c *ui.Context, p navPage) {
	switch p.path {
	case "/permissions":
		a.permissionsPage(c)
		return
	case "/logs":
		a.logsPage(c)
		return
	case "/providers":
		a.providersPage(c)
		return
	}
	pageHeader(c, pageTitle(p.path), nil)
	title, body := placeholderText(p.path)
	emptyState(c, p.icon, title, body, func() { a.placeholderActions(c, p.path) })
}

// placeholderText says what a page will hold.
func placeholderText(path string) (title, body string) {
	switch path {
	case "/home":
		return i18n.T("Speak anywhere, get text where your cursor is"),
			i18n.T("Home will list what is still missing (a service, a permission), let you try each feature without leaving the page, and show today's results.")
	case "/history":
		return i18n.T("Nothing here yet"),
			i18n.T("Every transcription, translation and rewrite will be listed here: search them, see the source text, the result, the rule that matched and the timings, copy or redo them.")
	case "/transcribe":
		return i18n.T("Speech to text, typed where you are"),
			i18n.T("Here you will choose the shortcuts, the speech and language services, and how the text is cleaned up, then try it out.")
	case "/translate":
		return i18n.T("Translate what you say or select"),
			i18n.T("Here you will choose the shortcut, the services and the target language, and whether the translation is typed or shown in a card.")
	case "/rewrite":
		return i18n.T("Rewrite the selection, or write from a spoken prompt"),
			i18n.T("Select text and say how to change it (\"shorter\", \"more formal\"), or say what to write with nothing selected. Here you will choose the shortcut, the services and when the answer shows in a card.")
	case "/app-enhancement":
		return i18n.T("Rules for the app or website in front"),
			i18n.T("Group apps and website addresses, give each group its own scene prompt (a chat app, your mail), and see which group matches right now.")
	case "/dictionary":
		return i18n.T("Names and terms you use"),
			i18n.T("Words you add here go to speech recognition as hints and to the language model as a glossary, so they come back spelled right.")
	}
	return "", ""
}

// placeholderActions are the empty states' ways forward.
func (a *app) placeholderActions(c *ui.Context, path string) {
	switch path {
	case "/home":
		if ui.Button(c.Key("home-providers"), i18n.T("Set up a service")).Clicked() {
			a.navigate("/providers")
		}
		if ghostButton(c.Key("home-permissions"), i18n.T("Check permissions")).Clicked() {
			a.navigate("/permissions")
		}
	case "/transcribe", "/translate", "/rewrite":
		if ghostButton(c.Key("feature-shortcuts"), i18n.T("Keyboard shortcuts")).Clicked() {
			a.sheetOpen = true
		}
	}
}

func (a *app) notFoundPage(c *ui.Context, path string) {
	pageHeader(c, i18n.T("Not found"), nil)
	emptyState(c, icons.Magnifer, i18n.T("There is no page at %s.", path), "", func() {
		if ui.Button(c.Key("go-home"), i18n.T("Go to Home")).Clicked() {
			a.navigate("/home")
		}
	})
}
