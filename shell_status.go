package main

// The status bar along the bottom of the window (AudioSmith's, with the
// helper in the engine's place): what the features will use (providers,
// app enhancement; M2 and M5 fill it in), the helper's state, which opens
// its popover, then on the right the notification centre's bell, the logs
// panel toggle and the version.

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/i18n"
)

func (a *app) statusBar(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c).Height(px(t, statusBarHeight)).Shrink(0).Padding(0, px(t, sp2)).Gap(px(t, sp1)).AlignItems(ui.Center).Background(k.Sidebar).TextColor(k.Text2).Children(func() {
		// The providers (M2 lists "ASR Doubao · LLM DeepSeek" here).
		if statusButton(c, "status-providers", i18n.T("Set up a speech and a language model service"), func() {
			statusDot(c, t.Warning)
			ui.Text(c, i18n.T("No provider set up")).FontSize(px(t, fsCaption)).SingleLine()
		}).Clicked() {
			a.navigate("/providers")
		}

		// The helper.
		st := a.helperStatus()
		color, label := t.Warning, i18n.T("Helper starting…")
		switch st.State {
		case helper.Ready:
			color, label = t.Success, i18n.T("Helper ready")
		case helper.Failed, helper.Stopped:
			color, label = t.Danger, helperFailText(st)
		}
		hb := statusButton(c, "status-helper", i18n.T("The helper records, watches shortcuts and reads browser addresses"), func() {
			statusDot(c, color)
			ui.Text(c, label).FontSize(px(t, fsCaption)).SingleLine().Ellipsis("…").MaxWidth(px(t, 240)).MinWidth(0)
		})
		if hb.Clicked() {
			a.helperOpen = !a.helperOpen
		}
		ui.Popover(c, hb, &a.helperOpen, func() { a.helperPanel(c) })
		ui.Spacer(c)

		// Notifications.
		unread := 0
		for _, n := range a.notices {
			if !n.Read {
				unread++
			}
		}
		bell := statusButton(c, "status-bell", i18n.T("Notifications"), func() {
			ui.Icon(c, bellIcon.Get(unread > 0)).Size(px(t, 14), px(t, 14))
			if unread > 0 {
				ui.Text(c, itoa(unread)).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.AccentInk)
			}
		})
		if bell.Clicked() {
			a.noticesOpen = !a.noticesOpen
		}
		ui.Popover(c, bell, &a.noticesOpen, func() { a.noticeCentre(c) })

		if statusButton(c, "status-logs", i18n.T("Show or hide the logs panel (⇧⌘L)"), func() {
			ui.Icon(c, logsIcon.Get(a.logsPanel)).Size(px(t, 14), px(t, 14))
		}).Clicked() {
			a.logsPanel = !a.logsPanel
		}
		if a.version != "" {
			ui.Text(c, "v"+a.version).FontSize(px(t, fsMicro)).TextColor(k.Text3).Margin(0, px(t, sp1))
		}
	})
}

// statusButton is a flat button of the status bar.
func statusButton(c *ui.Context, key, label string, content func()) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Padding(px(t, 2), px(t, 6)).Radius(px(t, 6)).Label(label).Tooltip(label).MinWidth(0).Shrink(1)
	if b.Hovered() {
		b.Background(tok(t).Hover)
	}
	return b.Children(func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).MinWidth(0).Children(content)
	})
}

// helperPanel is the helper's popover: its state, version and what to do.
func (a *app) helperPanel(c *ui.Context) {
	t := c.Theme()
	st := a.helperStatus()
	ui.Column(c).Width(px(t, 320)).Gap(px(t, sp2)).Padding(px(t, sp1)).Children(func() {
		sectionHeading(c, i18n.T("Helper"))
		info := map[helper.State]string{helper.Starting: i18n.T("Starting"), helper.Ready: i18n.T("Ready"),
			helper.Failed: i18n.T("Stopped"), helper.Stopped: i18n.T("Stopped")}[st.State]
		if st.Hello != nil {
			info += " · " + i18n.T("version %s", st.Hello.Version)
		}
		if st.PID != 0 {
			info += " · PID " + itoa(st.PID)
		}
		caption(c, info)
		if st.State == helper.Failed {
			ui.Text(c, helperFailText(st)).FontSize(px(t, fsCaption)).TextColor(t.Danger).MaxLines(3).Ellipsis("…").Selectable()
			if !st.RestartAt.IsZero() {
				caption(c, i18n.T("Restarting in %d s", max(1, int(time.Until(st.RestartAt).Seconds()+0.5))))
			}
		}
		caption(c, i18n.T("The helper is a small part of Auralink that records the microphone, watches the shortcuts and reads browser addresses."))
		ui.Divider(c)
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			if ui.Button(c.Key("helper-restart"), i18n.T("Restart the helper")).Clicked() {
				a.restartHelper()
			}
			ui.Spacer(c)
			if ghostButton(c.Key("helper-logs"), i18n.T("Logs")).Clicked() {
				a.helperOpen = false
				a.logsSource = helper.LogSource
				a.navigate("/logs")
			}
		})
	})
}

// helperFailText says why the helper does not run, for the status bar.
func helperFailText(st helper.Status) string {
	switch st.Reason {
	case helper.FailMissing:
		return i18n.T("Helper missing")
	case helper.FailProtocol:
		return i18n.T("Helper out of date")
	}
	if st.State == helper.Stopped {
		return i18n.T("Helper stopped")
	}
	return i18n.T("Helper stopped unexpectedly")
}

// noticePosted adds a notification to the centre's snapshot and shows it
// as a toast.
func (a *app) noticePosted(n core.Notification) {
	for i := range a.notices {
		if a.notices[i].ID == n.ID {
			a.notices = append(a.notices[:i], a.notices[i+1:]...)
			break
		}
	}
	a.notices = append([]core.Notification{n}, a.notices...)
	msg := n.Title
	if n.Body != "" {
		msg += ": " + n.Body
	}
	a.toast(msg)
}

// notify posts a notification (on any goroutine).
func (a *app) notify(sev core.Severity, title, body, link string) {
	a.notes.Post(core.Notification{Severity: sev, Title: title, Body: body, Link: link})
}

// noticeCentre lists the notifications, newest first.
func (a *app) noticeCentre(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(px(t, 360)).Gap(px(t, sp2)).Padding(px(t, sp1)).Children(func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			sectionHeading(c, i18n.T("Notifications")).Grow(1)
			if ghostButton(c.Key("notices-read"), i18n.T("Mark all read")).Disabled(len(a.notices) == 0).Clicked() {
				for i := range a.notices {
					a.notices[i].Read = true
				}
				notes := a.notes
				a.async(func() { notes.MarkRead(context.Background(), "") })
			}
			if ghostButton(c.Key("notices-clear"), i18n.T("Clear")).Disabled(len(a.notices) == 0).Clicked() {
				a.notices = nil
				notes := a.notes
				a.async(func() { notes.Clear(context.Background()) })
			}
		})
		if len(a.notices) == 0 {
			ui.Text(c, i18n.T("Nothing new.")).TextColor(t.TextMuted).Padding(px(t, 8), 0)
			return
		}
		ui.Scroll(c).MaxHeight(px(t, 360)).Children(func() {
			ui.Column(c).Gap(px(t, 6)).Children(func() {
				for _, n := range a.notices {
					a.noticeRow(c, n)
				}
			})
		})
	})
}

func (a *app) noticeRow(c *ui.Context, n core.Notification) {
	t := c.Theme()
	color := t.TextMuted
	switch n.Severity {
	case core.SeverityError:
		color = t.Danger
	case core.SeverityWarning:
		color = t.Warning
	case core.SeveritySuccess:
		color = t.Success
	}
	k := tok(t)
	row := ui.Row(c.Key("notice-" + n.ID)).Gap(px(t, sp2)).Padding(px(t, sp2)).Radius(px(t, radiusControl)).AlignItems(ui.Start)
	if !n.Read {
		row.Background(k.Subtle)
	}
	row.Children(func() {
		ui.Box(c).Size(px(t, 6), px(t, 6)).Radius(px(t, 3)).Background(color).Margin(px(t, 6), 0, 0, 0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
			ui.Text(c, n.Title).FontSize(px(t, fsBody)).FontWeight(600).MaxLines(2).Ellipsis("…")
			if n.Body != "" {
				ui.Text(c, n.Body).FontSize(px(t, fsCaption)).TextColor(k.Text2).MaxLines(4).Ellipsis("…").Selectable()
			}
			ui.Text(c, timeAgo(n.Time)).FontSize(px(t, fsMicro)).TextColor(k.Text3)
		})
		if n.Link != "" {
			if ghostButton(c.Key("open"), i18n.T("Open")).Clicked() {
				a.noticesOpen = false
				a.openURL(n.Link)
			}
		}
	})
}

// timeAgo says how long ago t was, briefly.
func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return i18n.T("just now")
	case d < time.Hour:
		return i18n.N(int(d.Minutes()), "%d minute ago", "%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return i18n.N(int(d.Hours()), "%d hour ago", "%d hours ago", int(d.Hours()))
	}
	return i18n.DateTime(t)
}
