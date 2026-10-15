package main

// The pages of the sidebar (plan §3.1). A page not built yet is its
// header and an empty state that says what it will hold; the milestones
// replace them one by one (App enhancement and Dictionary M5, Home M6).
// Logs (shell_logs.go), Model services (providers_page.go), Transcribe,
// Translate and Rewrite (transcribe_page.go) and History
// (history_page.go) work; Permissions is a section of the settings dialog
// (permissions.go).

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
	switch path {
	case "/history":
		a.historyShown() // history_page.go
	}
}

// page builds one page of the sidebar.
func (a *app) page(c *ui.Context, p navPage) {
	switch p.path {
	case "/logs":
		a.logsPage(c)
		return
	case "/providers":
		a.providersPage(c)
		return
	case "/transcribe":
		a.transcribePage(c) // transcribe_page.go
		return
	case "/translate":
		a.translatePage(c) // transcribe_page.go
		return
	case "/rewrite":
		a.rewritePage(c) // transcribe_page.go
		return
	case "/history":
		a.historyPage(c) // history_page.go
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
			a.navigate("/settings/permissions")
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
