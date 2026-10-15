package main

// The Transcribe page (/transcribe, plan §3.1): its shortcuts, services,
// clean-up layers, prompt and Try area.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
)

// transcribeState is the Transcribe page's view state.
type transcribeState struct{}

// transcribePage builds the page.
func (a *app) transcribePage(c *ui.Context) {
	pageHeader(c, i18n.T("Transcribe"), nil)
}
