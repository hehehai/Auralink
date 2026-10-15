package main

// The helper as the UI sees it: main makes its supervisor
// (newHelperHost, connectHelper), the window's start starts it, and the
// status bar, the Permissions page and Diagnostics read the snapshot of
// its status kept here from helper.StatusChanged. Requests go from a
// goroutine (helperCall); notifications that belong to no request
// (hotkey.event) arrive in helperNotified.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/helper/proto"
)

// helperHost holds the supervisor: newHelperHost finds the executable,
// connectHelper makes the supervisor (it needs the app's bus and log).
// Tests set sup to a fake before connectHelper.
type helperHost struct {
	bin, logDir string
	env         []string
	sup         helper.Supervisor
}

func newHelperHost(resources, logDir string) *helperHost {
	return &helperHost{bin: helper.Find(resources), logDir: logDir}
}

// helperLink is the helper's view state.
type helperLink struct {
	host    *helperHost
	hstatus helper.Status // as StatusChanged last said
	// hotkeys is the event tap's state as hotkey.state last said; front
	// the app in front as app.activated last said (sessions, M3).
	hotkeys proto.HotkeyStatus
	front   proto.App
}

// connectHelper makes the supervisor and follows its status.
func (a *app) connectHelper() {
	h := a.host
	core.On(a.bus, func(ev helper.StatusChanged) { a.post(func() { a.helperChanged(ev.Status) }) })
	if h.sup == nil {
		version := ""
		if a.hasMenu || a.win != nil {
			version = mygo.App.Version()
		}
		h.sup = helper.New(helper.Config{
			Bin: h.bin, AppVersion: version, LogDir: h.logDir, Bus: a.bus, Env: h.env,
			Log:      a.logs.Append,
			OnNotify: func(m proto.Message) { a.post(func() { a.helperNotified(m) }) },
		})
	}
	a.hstatus = h.sup.Status()
}

// startHelper starts the helper; it runs when the window opens.
func (a *app) startHelper() {
	if err := a.host.sup.Start(context.Background()); err != nil {
		log.Printf("helper: %v", err)
	}
}

// stopHelper stops it on quit.
func (a *app) stopHelper() {
	if a.host != nil && a.host.sup != nil {
		a.host.sup.Stop()
	}
}

// restartHelper restarts it (the status bar's popover, the palette).
func (a *app) restartHelper() {
	sup := a.host.sup
	a.async(func() { sup.Restart() })
}

func (a *app) helperStatus() helper.Status { return a.hstatus }

// helperChanged keeps the snapshot; a helper that just became ready is
// asked what the Permissions page shows.
func (a *app) helperChanged(st helper.Status) {
	was := a.hstatus.State
	a.hstatus = st
	if st.State == helper.Ready && was != helper.Ready {
		a.recheckPermissions() // permissions.go
		a.probeAutomation()
		if a.try.on {
			a.tryConfigure() // devtry.go
		}
	}
}

// helperNotified handles a notification of no request. Until sessions
// (M3) start from hotkey.event, it logs the shortcuts the helper reports
// and keeps the tap's state and the app in front.
func (a *app) helperNotified(m proto.Message) {
	switch m.Method {
	case proto.MethodHotkeyEvent:
		var e proto.HotkeyEventParams
		m.DecodeParams(&e)
		msg := "hotkey " + e.Binding + " " + e.Phase
		if e.Front != nil {
			msg += " in " + e.Front.BundleID
		}
		level := core.SeverityDebug
		if a.try.on {
			level = core.SeverityInfo
			a.tryHotkey(e)
		}
		a.logs.Append(core.LogEntry{Time: time.Now(), Level: level, Source: helper.LogSource, Message: msg})
	case proto.MethodHotkeyState:
		var st proto.HotkeyStatus
		m.DecodeParams(&st)
		if st.Accessibility != a.hotkeys.Accessibility {
			a.recheckPermissions() // Accessibility was granted or taken away
		}
		a.hotkeys = st
		if a.try.on {
			a.tryLog(fmt.Sprintf("hotkey.state: installed %v, accessibility %v %s", st.Installed, st.Accessibility, st.Error))
		}
	case proto.MethodAppActivated:
		m.DecodeParams(&a.front)
		if a.try.on {
			a.tryLog("app.activated " + a.front.BundleID)
		}
	}
}

// helperCall sends a request on a goroutine of its own and hands the
// result to then on the main thread (decoded into a new T), with the
// error. Without a helper that implements method, then gets the error at
// once.
func helperCall[T any](a *app, method string, params any, timeout time.Duration, then func(T, error)) {
	sup := a.host.sup
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var out T
		raw, err := sup.Call(ctx, "", method, params, nil)
		if err == nil {
			err = json.Unmarshal(raw, &out)
		}
		a.post(func() { then(out, err) })
	})
}
