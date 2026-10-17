package actnow

import (
	"slices"

	"github.com/hehehai/Auralink/internal/settings"
	asettings "github.com/hehehai/actnowkit/settings"
)

// Shared are the family's shared settings Auralink has (spec §9.1): the
// interface language and the proxy. (The model folder and the Hugging
// Face endpoints are for apps with local models.)
var Shared = []string{asettings.KeyLanguage, asettings.KeyProxy}

// Own is Auralink's own value of a shared setting.
func Own(s settings.Settings, key string) string {
	switch key {
	case asettings.KeyLanguage:
		return s.General.Language
	case asettings.KeyProxy:
		return s.Network.Proxy
	}
	return ""
}

// SetOwn sets Auralink's own value of a shared setting.
func SetOwn(s *settings.Settings, key, value string) {
	switch key {
	case asettings.KeyLanguage:
		s.General.Language = value
	case asettings.KeyProxy:
		s.Network.Proxy = value
	}
}

// Follows reports whether Auralink uses the family's value of key (spec
// §9.3): unless the user chose a setting of its own, it does.
func Follows(s settings.Settings, key string) bool {
	return asettings.Follow(s.Family.Follow).Of(key)
}

// SetFollow records whether Auralink follows the family's key.
func SetFollow(s *settings.Settings, key string, on bool) {
	if s.Family.Follow == nil {
		s.Family.Follow = map[string]bool{}
	}
	s.Family.Follow[key] = on
}

// Differs reports whether key's row says, once, that the family has
// another value (spec §9.4).
func Differs(s settings.Settings, key string) bool { return slices.Contains(s.Family.Differs, key) }

// Answered drops key from the settings that differ.
func Answered(s *settings.Settings, key string) {
	s.Family.Differs = slices.DeleteFunc(slices.Clone(s.Family.Differs), func(k string) bool { return k == key })
}

// Locals are Auralink's values for the first-run migration
// (settings.Migrate, spec §9.4): "" is the default of both.
func Locals(s settings.Settings) map[string]asettings.Local {
	out := map[string]asettings.Local{}
	for _, key := range Shared {
		v := Own(s, key)
		out[key] = asettings.Local{Value: v, IsDefault: v == ""}
	}
	return out
}

// Migrated applies the migration's outcomes to Auralink's settings: the
// follow flags, the rows that say the family differs, and the mark that
// it ran.
func Migrated(s *settings.Settings, outcomes []asettings.Outcome) {
	for _, o := range outcomes {
		SetFollow(s, o.Key, o.Follow)
		if o.Conflict && !Differs(*s, o.Key) {
			s.Family.Differs = append(slices.Clone(s.Family.Differs), o.Key)
		}
	}
	s.Family.Migrated = true
}

// Adopt is what Auralink's own values become from the family's shared
// settings fam: the family's value of each key it follows and the family
// sets (spec §9.3). A key the family does not set keeps Auralink's value:
// that is the default unless the family's file went away, and then the
// apps go back to their own (spec §15.3).
func Adopt(s settings.Settings, fam asettings.Settings) map[string]string {
	out := map[string]string{}
	for _, key := range Shared {
		v, ok := fam.Get(key)
		if ok && Follows(s, key) && v != Own(s, key) {
			out[key] = v
		}
	}
	return out
}

// SharedValue is how Auralink's value of key is written to the family:
// a proxy typed as "host:port" (which Auralink reads as http://) gets
// its scheme, as the family's format has it.
func SharedValue(key, value string) string {
	if key != asettings.KeyProxy {
		return value
	}
	if _, err := asettings.Normalize(key, value); err != nil {
		if _, err := asettings.Normalize(key, "http://"+value); err == nil {
			return "http://" + value
		}
	}
	return value
}
