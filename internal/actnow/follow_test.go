package actnow

import (
	"context"
	"testing"

	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/conformance"
	asettings "github.com/hehehai/actnowkit/settings"
)

func TestFollowFlags(t *testing.T) {
	s := settings.Defaults()
	if !Follows(s, asettings.KeyProxy) || !Follows(s, asettings.KeyLanguage) {
		t.Fatal("a new install follows the family")
	}
	SetFollow(&s, asettings.KeyProxy, false)
	if Follows(s, asettings.KeyProxy) || !Follows(s, asettings.KeyLanguage) {
		t.Errorf("follow %v", s.Family.Follow)
	}
	SetOwn(&s, asettings.KeyProxy, "direct")
	SetOwn(&s, asettings.KeyLanguage, "zh-Hans")
	if s.Network.Proxy != "direct" || Own(s, asettings.KeyLanguage) != "zh-Hans" {
		t.Errorf("own %+v %+v", s.Network, s.General)
	}
	l := Locals(s)
	if len(l) != 2 || l[asettings.KeyProxy].IsDefault || l[asettings.KeyProxy].Value != "direct" {
		t.Errorf("locals %+v", l)
	}
	if l := Locals(settings.Defaults()); !l[asettings.KeyLanguage].IsDefault || !l[asettings.KeyProxy].IsDefault {
		t.Errorf("defaults %+v", l)
	}
}

// TestMigrateAndAdopt: the first start's merge (spec §9.4) through
// actnowkit, then what Auralink takes from the family.
func TestMigrateAndAdopt(t *testing.T) {
	ctx := context.Background()
	fx := conformance.New(t)
	// AudioSmith set a proxy and a language before Auralink joined.
	if _, err := asettings.Set(ctx, fx.Family, conformance.PeerSmith, asettings.KeyProxy, "socks5://127.0.0.1:7897"); err != nil {
		t.Fatal(err)
	}
	if _, err := asettings.Set(ctx, fx.Family, conformance.PeerSmith, asettings.KeyLanguage, "zh-Hans"); err != nil {
		t.Fatal(err)
	}
	// Auralink's proxy is the user's own; its language the default.
	s := settings.Defaults()
	s.Network.Proxy = "http://10.0.0.1:3128"
	out, err := asettings.Migrate(ctx, fx.Family, "dev.hehehai.auralink.t", Locals(s))
	if err != nil {
		t.Fatal(err)
	}
	Migrated(&s, out)
	if !s.Family.Migrated || Follows(s, asettings.KeyProxy) || !Differs(s, asettings.KeyProxy) {
		t.Errorf("a proxy of its own differs: %+v", s.Family)
	}
	if !Follows(s, asettings.KeyLanguage) || Differs(s, asettings.KeyLanguage) {
		t.Errorf("the default language follows: %+v", s.Family)
	}
	fam, _, _ := asettings.Read(fx.Family)
	got := Adopt(s, fam)
	if len(got) != 1 || got[asettings.KeyLanguage] != "zh-Hans" {
		t.Errorf("adopt %v", got)
	}
	Answered(&s, asettings.KeyProxy)
	if Differs(s, asettings.KeyProxy) {
		t.Error("still differs once answered")
	}
	// Nothing to take once the values are the same; a key the family does
	// not set keeps Auralink's.
	SetOwn(&s, asettings.KeyLanguage, "zh-Hans")
	if got := Adopt(s, fam); len(got) != 0 {
		t.Errorf("adopt again %v", got)
	}
	if got := Adopt(s, asettings.Settings{}); len(got) != 0 {
		t.Errorf("an empty family changes %v", got)
	}
}

func TestSharedValue(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:7890":        "http://127.0.0.1:7890",
		"socks5://127.0.0.1:80": "socks5://127.0.0.1:80",
		"":                      "",
		"direct":                "direct",
	} {
		if got := SharedValue(asettings.KeyProxy, in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if got := SharedValue(asettings.KeyLanguage, "en"); got != "en" {
		t.Error(got)
	}
}
