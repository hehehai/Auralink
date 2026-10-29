package actnow

import (
	"context"
	"errors"
	"testing"

	"github.com/hehehai/actnowkit/credentials"
)

// fakeShared is the family's keys in memory: entries by vendor, the
// vendors this app uses, and the secrets; reads counts Secret calls.
type fakeShared struct {
	entries map[string]credentials.Entry
	using   map[string]bool
	secrets map[string]string
	errs    map[string]error
	reads   int
}

func (f *fakeShared) Offer(v string) (credentials.Entry, bool) {
	e, ok := f.entries[v]
	return e, ok
}

func (f *fakeShared) Using(v string) bool { return f.using[v] }

func (f *fakeShared) Secret(_ context.Context, v string) (string, error) {
	if !f.using[v] {
		return "", credentials.ErrNotConsented
	}
	f.reads++
	if err := f.errs[v]; err != nil {
		return "", err
	}
	s, ok := f.secrets[v]
	if !ok {
		return "", credentials.ErrMissing
	}
	return s, nil
}

// ownOf is Auralink's Keychain items; err, when set, is what every read fails with.
func ownOf(items map[string]string, err error) func(string) (string, error) {
	return func(acc string) (string, error) { return items[acc], err }
}

func TestResolve(t *testing.T) {
	const acc, vendor = "asr.aliyun", "dashscope"
	entry := credentials.Entry{AddedBy: "dev.example.audiosmith", Hint: "sk-…a1b2"}
	consented := func() *fakeShared {
		return &fakeShared{
			entries: map[string]credentials.Entry{vendor: entry},
			using:   map[string]bool{vendor: true},
			secrets: map[string]string{vendor: "sk-shared-000000a1b2"},
		}
	}
	unconsented := func() *fakeShared {
		f := consented()
		f.using = map[string]bool{}
		return f
	}
	keyErr := errors.New("security: user interaction is not allowed")
	tests := []struct {
		name   string
		own    map[string]string
		ownErr error
		vendor string
		shared SharedKeys
		want   State
		secret string
	}{
		{"only Auralink: its own item", map[string]string{acc: "sk-own"}, nil, "", nil, KeyOwn, "sk-own"},
		{"own item wins over a family key", map[string]string{acc: "sk-own"}, nil, vendor, consented(), KeyOwn, "sk-own"},
		{"no key at all", nil, nil, "", nil, KeyNone, ""},
		{"own item unreadable", nil, keyErr, vendor, consented(), KeyFailed, ""},
		{"family key agreed to", nil, nil, vendor, consented(), KeyShared, "sk-shared-000000a1b2"},
		{"family key not agreed to yet", nil, nil, vendor, unconsented(), KeyUnconfirmed, ""},
		{"family not read yet", nil, nil, vendor, nil, KeyUnknown, ""},
		{"family key gone: no metadata", nil, nil, vendor, &fakeShared{entries: map[string]credentials.Entry{}}, KeyGone, ""},
		{"family key gone: item deleted", nil, nil, vendor, &fakeShared{
			entries: map[string]credentials.Entry{vendor: entry},
			using:   map[string]bool{vendor: true},
		}, KeyGone, ""},
		{"family key unreadable", nil, nil, vendor, &fakeShared{
			entries: map[string]credentials.Entry{vendor: entry},
			using:   map[string]bool{vendor: true},
			errs:    map[string]error{vendor: keyErr},
		}, KeyFailed, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := Resolve(context.Background(), ownOf(tt.own, tt.ownErr), acc, tt.vendor, tt.shared)
			if k.State != tt.want || k.Secret != tt.secret {
				t.Fatalf("got %v %q, want %v %q", k.State, k.Secret, tt.want, tt.secret)
			}
			if k.Found() != (tt.want == KeyOwn || tt.want == KeyShared) {
				t.Errorf("Found() = %v for %v", k.Found(), k.State)
			}
		})
	}
}

// TestResolveReadsFamilyOnlyWhenNeeded: an own item, or a service that
// names no family key, reads nothing from the family; an unconfirmed key
// is never read.
func TestResolveReadsFamilyOnlyWhenNeeded(t *testing.T) {
	ctx := context.Background()
	f := &fakeShared{
		entries: map[string]credentials.Entry{"dashscope": {AddedBy: "x"}},
		using:   map[string]bool{},
		secrets: map[string]string{"dashscope": "sk-shared"},
	}
	Resolve(ctx, ownOf(map[string]string{"asr.aliyun": "sk-own"}, nil), "asr.aliyun", "dashscope", f)
	Resolve(ctx, ownOf(nil, nil), "asr.aliyun", "", f)
	Resolve(ctx, ownOf(nil, nil), "asr.aliyun", "dashscope", f)
	if f.reads != 0 {
		t.Fatalf("read the family's key %d times before the user agreed", f.reads)
	}
	f.using["dashscope"] = true
	k := Resolve(ctx, ownOf(nil, nil), "asr.aliyun", "dashscope", f)
	if k.State != KeyShared || f.reads != 1 {
		t.Fatalf("after agreeing: %v, reads %d", k.State, f.reads)
	}
}

// TestResolveResidue: metadata another app left behind (its bundle in the
// users of a vendor it no longer has, or the item gone) does not change
// Auralink's own item.
func TestResolveResidue(t *testing.T) {
	ctx := context.Background()
	f := &fakeShared{
		entries: map[string]credentials.Entry{"dashscope": {AddedBy: "dev.uninstalled.app", Users: []string{"dev.uninstalled.app"}}},
		using:   map[string]bool{},
	}
	if k := Resolve(ctx, ownOf(map[string]string{"asr.aliyun": "sk-own"}, nil), "asr.aliyun", "dashscope", f); k.State != KeyOwn {
		t.Fatalf("own item lost to residue: %v", k.State)
	}
	if k := Resolve(ctx, ownOf(nil, nil), "asr.aliyun", "dashscope", f); k.State != KeyUnconfirmed || k.Owner != "dev.uninstalled.app" {
		t.Fatalf("residue taken as agreed: %+v", k)
	}
}
