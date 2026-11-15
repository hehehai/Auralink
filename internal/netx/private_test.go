package netx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

func TestPlainAllowed(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "[::1]": true, "localhost": true, "relay.localhost": true,
		"10.0.0.1": true, "10.255.255.255": true, "9.255.255.255": false, "11.0.0.1": false,
		"172.16.0.1": true, "172.31.255.255": true, "172.15.255.255": false, "172.32.0.1": false,
		"192.168.1.2": true, "192.169.0.1": false, "192.167.255.255": false,
		"100.64.0.1": true, "100.88.1.2": true, "100.127.255.255": true, "100.63.255.255": false, "100.128.0.1": false,
		"169.254.3.4": true, "169.255.0.1": false,
		"fe80::1": true, "fe80::1%en0": true, "febf::1": true, "fec0::1": false,
		"fc00::1": true, "fd12:3456::1": true, "fe00::1": false,
		"::ffff:10.0.0.1": true, "::ffff:8.8.8.8": false,
		"8.8.8.8": false, "1.1.1.1": false, "2001:4860:4860::8888": false, "0.0.0.0": false,
		// Names pass here and are checked when dialed.
		"gpubox": true, "gpubox.tail1234.ts.net": true, "printer.local": true, "nas.lan": true, "api.openai.com": true,
		"": false,
	} {
		if got := PlainAllowed(host); got != want {
			t.Errorf("PlainAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestIsPrivateHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "localhost": true, "10.1.2.3": true, "100.100.1.1": true, "[fd00::1]": true,
		"printer.local": true, "printer.local.": true, "gpubox": false, "api.openai.com": false, "8.8.8.8": false,
	} {
		if got := IsPrivateHost(host); got != want {
			t.Errorf("IsPrivateHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestPlainOverNetwork(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://100.88.1.2:3425/v1": true, "ws://gpubox:8080/asr": true, "HTTP://10.0.0.2/v1": true,
		"http://127.0.0.1:8080/v1": false, "http://localhost/v1": false, "ws://[::1]:9/": false,
		"https://10.0.0.2/v1": false, "wss://api.example.com/": false, "": false, "not a url": false,
	} {
		if got := PlainOverNetwork(raw); got != want {
			t.Errorf("PlainOverNetwork(%q) = %v, want %v", raw, got, want)
		}
	}
}

// fakeLookup answers names from a table, for the dial-time check.
func fakeLookup(t *testing.T, table map[string][]string) {
	old := lookupAddrs
	t.Cleanup(func() { lookupAddrs = old })
	lookupAddrs = func(_ context.Context, host string) ([]netip.Addr, error) {
		as, ok := table[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		var out []netip.Addr
		for _, a := range as {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

func TestPlainDialRefusesPublicNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok "+r.Header.Get("Authorization"))
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	fakeLookup(t, map[string][]string{
		"gpubox":          {"127.0.0.1"},
		"evil.example":    {"203.0.113.7"},
		"mixed.example":   {"127.0.0.1", "8.8.8.8"},
		"tail.ts.net":     {"100.88.1.2"},
		"public6.example": {"2001:db8::1", "::1"},
	})
	client := NewHTTPClient(ProxyDirect, 0)
	get := func(host string) (string, error) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/v1", nil)
		req.Header.Set("Authorization", "Bearer k")
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}
	// A name resolving to loopback is reached, plainly.
	if body, err := get("gpubox"); err != nil || body != "ok Bearer k" {
		t.Fatalf("gpubox: %q, %v", body, err)
	}
	if body, err := get("127.0.0.1"); err != nil || body != "ok Bearer k" {
		t.Fatalf("127.0.0.1: %q, %v", body, err)
	}
	// A name with any public address is refused before connecting.
	for _, host := range []string{"evil.example", "mixed.example", "public6.example", "203.0.113.7"} {
		_, err := get(host)
		if !errors.Is(err, ErrPlainPublic) {
			t.Errorf("%s: err = %v, want ErrPlainPublic", host, err)
		}
	}
}

func TestPlainGoesDirect(t *testing.T) {
	// A proxy that answers everything itself: what reaches it went
	// through it.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "proxy")
	}))
	defer proxy.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "direct") }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	fakeLookup(t, map[string][]string{"gpubox": {"127.0.0.1"}})
	client := NewHTTPClient(proxy.URL, 0)
	resp, err := client.Get("http://" + net.JoinHostPort("gpubox", port) + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "direct" {
		t.Errorf("plain http to a private name went %q, want direct", b)
	}
	// https to private addresses skips the proxy too; to a public name
	// it does not.
	pf := ProxyFunc(proxy.URL)
	for raw, want := range map[string]bool{
		"https://10.0.0.2/v1": false, "https://100.88.1.2/v1": false, "https://[fd00::2]/": false,
		"https://printer.local/": false, "https://api.openai.com/v1": true,
	} {
		u, _ := url.Parse(raw)
		got, _ := pf(&http.Request{URL: u})
		if (got != nil) != want {
			t.Errorf("%s: proxy %v, want proxied=%v", raw, got, want)
		}
	}
}

func TestCheckRedirect(t *testing.T) {
	req := func(s string) *http.Request { u, _ := url.Parse(s); return &http.Request{URL: u} }
	for _, c := range []struct {
		from, to string
		ok       bool
	}{
		{"https://api.example.com/a", "https://api.example.com/b", true},
		{"https://api.example.com/a", "https://cdn.example.com/b", true},
		{"https://api.example.com/a", "http://api.example.com/b", false}, // downgrade
		{"http://10.0.0.2:8000/a", "http://10.0.0.2:8000/b", true},
		{"http://10.0.0.2:8000/a", "http://10.0.0.3:8000/b", false}, // another private host
		{"http://10.0.0.2:8000/a", "https://api.example.com/b", true},
	} {
		err := CheckRedirect(req(c.to), []*http.Request{req(c.from)})
		if (err == nil) != c.ok {
			t.Errorf("%s → %s: %v, want ok=%v", c.from, c.to, err, c.ok)
		}
	}
}

func TestIsNoRoute(t *testing.T) {
	op := &net.OpError{Op: "dial", Net: "tcp", Err: &netErrno{syscall.EHOSTUNREACH}}
	for err, want := range map[error]bool{
		op: true,
		fmt.Errorf("x: %w", &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}): true,
		errors.New("dial tcp 10.0.0.2:80: connect: no route to host"):           true,
		errors.New("connection refused"):                                        false,
		nil:                                                                     false,
	} {
		if got := IsNoRoute(err); got != want {
			t.Errorf("IsNoRoute(%v) = %v, want %v", err, got, want)
		}
	}
}

// netErrno wraps an errno like os.SyscallError does.
type netErrno struct{ errno syscall.Errno }

func (e *netErrno) Error() string { return "connect: " + e.errno.Error() }
func (e *netErrno) Unwrap() error { return e.errno }
