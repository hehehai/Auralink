package main

// The service dialog's parts about the ActNow family's keys
// (family_keys.go; spec §8.3): the family's key offered in place of the
// key field, the checkbox that shares a key, and what the dialog says of
// a key other apps use.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/uitext"
)

// sharedKeyOffer is "Use the DeepSeek key from AudioSmith (sk-…a1b2)"
// with Use this key and Enter another key, in place of the key field: the
// key is read only when the user clicks.
func (a *app) sharedKeyOffer(c *ui.Context, name string) {
	t := c.Theme()
	f := &a.provDlg.fam
	e := f.offer
	from := strings.Join(a.appNames([]string{e.AddedBy}), ", ")
	text := i18n.T("Use the %s key from %s (%s)", name, from, e.Hint)
	d := &a.provDlg
	fieldInfo(c, uitext.KeyLabel(d.kind, d.id), "", func() {
		banner(c.Key("prov-shared-offer"), icons.KeyMinimalisticSquare, tok(t).Text2, text, func() {
			if ui.Button(c.Key("prov-shared-use"), i18n.T("Use this key")).Shrink(0).Clicked() {
				a.useSharedKey()
			}
			if ghostButton(c.Key("prov-shared-other"), i18n.T("Enter another key")).Shrink(0).Clicked() {
				f.other = true
			}
		}).Shrink(0)
	})
}

// sharedKeyNotes go under the key field: a key other apps have that
// differs from this one (with Use this key), the checkbox that shares the
// key with them, what replacing or removing a key they use does.
func (a *app) sharedKeyNotes(c *ui.Context, name, mask string) {
	d := &a.provDlg
	f := &d.fam
	if f.vendor == "" || a.famCreds.Load() == nil {
		return
	}
	t := c.Theme()
	typed := strings.TrimSpace(d.key) != ""
	if e := f.offer; e != nil && f.differs && f.ref == "" && !typed {
		from := strings.Join(a.appNames([]string{e.AddedBy}), ", ")
		ui.Row(c.Key("prov-shared-differs")).Wrap().GapX(px(t, sp2)).GapY(px(t, 2)).AlignItems(ui.Center).Children(func() {
			caption(c, i18n.T("%s has a different %s key (%s).", from, name, e.Hint))
			if ui.Link(c.Key("prov-shared-differs-use"), i18n.T("Use this key"), "").FontSize(px(t, fsCaption)).Clicked() {
				a.useSharedKey()
			}
		})
	}
	if f.askRemove {
		users := strings.Join(f.users, ", ")
		notice(c, "prov-shared-remove", noticeWarn, i18n.T("%s also uses this key.", users), "", func() {
			if ui.Button(c.Key("prov-shared-stop"), i18n.T("Stop using it here")).Shrink(0).Clicked() {
				a.saveProviderDialog(false)
			}
			if ui.Button(c.Key("prov-shared-delete"), i18n.T("Delete for all apps")).Shrink(0).Clicked() {
				a.saveProviderDialog(true)
			}
		}, func() { f.askRemove, d.removeKey = false, false })
		return
	}
	// The checkbox only when another ActNow app is installed (user
	// 10-17); alone, the key is shared or not as the checkbox's default
	// says (spec §8.3), unseen.
	if a.famReadOnly || d.removeKey || (!typed && mask == "") || !a.familyOthers() {
		return
	}
	ui.Row(c.Key("prov-share")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
		ui.Checkbox(c.Key("prov-share-box"), &f.share, i18n.T("Let other ActNow apps on this Mac use this key")).Shrink(1)
		infoTip(c, i18n.T("Shared keys are stored in your login keychain. Like each app's own keys, other programs you run can read them with the security tool.")).Shrink(0)
	})
	if typed && f.share && f.ref != "" && len(f.users) > 0 {
		caption(c, i18n.T("Also updates it in %s.", strings.Join(f.users, ", ")))
	}
}

// askRemoveShared asks, before a save that removes a family key other
// apps use, whether to stop using it here or delete it for all apps; it
// reports whether the save waits for the answer.
func (a *app) askRemoveShared(remove bool) bool {
	d := &a.provDlg
	f := &d.fam
	saved, _ := providers.Setup(a.prefs, d.kind, d.id)
	if !remove || f.askRemove || len(f.users) == 0 || saved.CredentialRef == "" {
		return false
	}
	f.askRemove = true
	return true
}

// sharedKeyError is the dialog's error when the family's key cannot be
// read.
func sharedKeyError() string {
	return i18n.T("Could not read the shared key: enter the key instead.")
}
