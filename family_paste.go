package main

// Pasting with the other ActNow apps that paste (spec §11.1, "pasting"):
// two voice input apps pasting at once would overwrite each other's
// clipboard. Before a paste, Auralink waits while another app's runtime
// file says it is pasting (at most 500 ms), and says so in its own
// (runtime/<bundle id>.json) for the whole paste: the helper's
// textio.paste writes the clipboard, posts ⌘V and puts the clipboard back
// before it answers. The helper pastes; Go decides when.

import (
	"context"
	"log"
	"sync/atomic"

	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/session"
	famruntime "github.com/hehehai/actnowkit/runtime"
)

// pasteGuard is the helper's text I/O with the family's paste flag
// around Paste; outside the family (link nil), the helper's alone.
type pasteGuard struct {
	deliver.TextIO
	link *atomic.Pointer[familyLink]
}

func (g pasteGuard) Paste(ctx context.Context, p proto.PasteParams) (proto.PasteResult, error) {
	fl := g.link.Load()
	if fl == nil {
		return g.TextIO.Paste(ctx, p)
	}
	bid := fl.self.BundleID
	if !famruntime.WaitPasting(ctx, fl.dir, bid) {
		log.Printf("family: another app is still pasting after %v: pasting anyway", famruntime.PasteWait)
	}
	if err := famruntime.Write(fl.dir, famruntime.File{BundleID: bid, Pasting: true}); err != nil {
		log.Printf("family: runtime: %v", err)
	}
	defer func() {
		if err := famruntime.Write(fl.dir, famruntime.File{BundleID: bid}); err != nil {
			log.Printf("family: runtime: %v", err)
		}
	}()
	return g.TextIO.Paste(ctx, p)
}

// textIO is the text I/O inserts go through: the helper's, guarded.
func (a *app) textIO() deliver.TextIO {
	return pasteGuard{TextIO: session.HelperText{H: a.host.sup}, link: &a.famLink}
}

// dropRuntime removes Auralink's runtime file on quit (spec §11.1).
func (fl *familyLink) dropRuntime() {
	if err := famruntime.Remove(fl.dir, fl.self.BundleID); err != nil {
		log.Printf("family: runtime: %v", err)
	}
}
