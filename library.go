package main

// The library: the app's domain, opened once by main before the window.
// It is the SQLite store (history, dictionary, documents), the bus every
// service publishes on, and the settings kept in the store. Everything in
// it is goroutine-safe; the UI reads it through snapshots it keeps
// current from the bus, never on the main thread.

import (
	"context"
	"log"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store/sqlitestore"
)

type library struct {
	store    *sqlitestore.Store
	bus      *core.Bus
	settings *settings.Store
	// What opening found, for the start-up log (diagnostics.go).
	settingsErr error
}

// openLibrary opens (creating or migrating) the store in dataDir and the
// settings in it.
func openLibrary(dataDir string) (*library, error) {
	ctx := context.Background()
	st, err := sqlitestore.Open(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	bus := core.NewBus()
	l := &library{store: st, bus: bus}
	l.settings, err = settings.Open(ctx, st.Documents(), func(ev settings.Changed) { bus.Publish(ev) })
	if err != nil {
		l.settingsErr = err
		log.Printf("settings: %v", err) // the defaults, until the next change saves them
	}
	return l, nil
}

// close closes the database; it runs last, on quit.
func (l *library) close() {
	if err := l.store.Close(); err != nil {
		log.Printf("library: %v", err)
	}
}
