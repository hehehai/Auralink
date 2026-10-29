package diag

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorClass(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errors.New("security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain."), "not found"},
		{errors.New("security: User interaction is not allowed. (-25308)"), "-25308, user interaction not allowed"},
		{fmt.Errorf("sqlitestore: migration 3: %w", errors.New("disk I/O error")), "migration 3, disk I/O error"},
		{errors.New("open /Users/alice/secret/key-sk-abc.json: permission denied"), "permission denied"},
		{errors.New("something sk-ABCDEF123456 odd"), "*errors.errorString"},
	}
	for _, c := range cases {
		got := ErrorClass(c.err)
		if c.err != nil && c.want == "*errors.errorString" {
			if got != "*errors.errorString" {
				t.Errorf("ErrorClass(%v) = %q", c.err, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("ErrorClass(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestErrorClassHidesText(t *testing.T) {
	const secret = "sk-TESTSECRET9876543210"
	err := fmt.Errorf("security: add failed for %s: authorization was denied", secret)
	if got := ErrorClass(err); got != "authorization denied" {
		t.Errorf("got %q", got)
	}
}

func TestTilde(t *testing.T) {
	const home = "/Users/alice"
	cases := map[string]string{
		"/Users/alice/Library/Application Support/Auralink": "~/Library/Application Support/Auralink",
		"/Users/alice":    "~",
		"/Users/alicia/x": "/Users/alicia/x",
		"/Volumes/Data/x": "/Volumes/Data/x",
		"":                "",
	}
	for in, want := range cases {
		if got := Tilde(in, home); got != want {
			t.Errorf("Tilde(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Tilde("/a/b", ""); got != "/a/b" {
		t.Errorf("empty home: %q", got)
	}
}
