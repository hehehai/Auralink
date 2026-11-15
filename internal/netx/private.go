package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
)

// Plain http:// and ws://. Keys and text go over TLS, except to a server
// on this Mac or on a private network (a LAN box, a Tailscale peer): there
// a user may run a model server without a certificate. The rule:
//
//   - at save time (PlainAllowed) a plain endpoint's host is loopback, a
//     private address, or a name (which may resolve to either);
//   - at dial time every plain connection goes direct (no proxy) through
//     a dialer that resolves the name itself and refuses unless every
//     address is loopback or private (ErrPlainPublic), so a name that
//     resolves to a public address never gets the key in clear.
//
// Private: 10/8, 172.16/12, 192.168/16, 100.64/10 (CGNAT, Tailscale),
// 169.254/16 and fe80::/10 (link-local), fc00::/7 (unique local).

// cgnat is 100.64.0.0/10, where Tailscale gives its peers addresses.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// IsLoopback reports whether host is this machine: localhost or a
// loopback address.
func IsLoopback(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.Unmap().IsLoopback()
}

// IsPrivateAddr reports whether a is loopback or on a private network.
func IsPrivateAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || (a.Is4() && cgnat.Contains(a))
}

// ipHost parses host as an address ("10.0.0.2", "[fe80::1%en0]").
func ipHost(host string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return a, err == nil
}

// IsPrivateHost reports whether host is known without a lookup to be this
// Mac or on a private network: loopback, a private address, or a .local
// (Bonjour) name. Such hosts never go through a proxy.
func IsPrivateHost(host string) bool {
	if IsLoopback(host) {
		return true
	}
	if a, ok := ipHost(host); ok {
		return IsPrivateAddr(a)
	}
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	return strings.HasSuffix(h, ".local")
}

// PlainAllowed reports whether a plain http:// or ws:// endpoint may name
// host: anything but a public address. A name is checked again when it
// is dialed (see above).
func PlainAllowed(host string) bool {
	if host == "" {
		return false
	}
	if a, ok := ipHost(host); ok {
		return IsPrivateAddr(a)
	}
	return true
}

// PlainOverNetwork reports whether the endpoint raw is plain (http://,
// ws://) to a host that is not this Mac: what crosses the network then is
// not encrypted, keys included.
func PlainOverNetwork(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	s := strings.ToLower(u.Scheme)
	return (s == "http" || s == "ws") && !IsLoopback(u.Hostname())
}

// ErrPlainPublic: a plain connection's host resolved to a public address.
var ErrPlainPublic = errors.New("plain http only to this Mac or a private network")

// PlainPublicError is ErrPlainPublic for one host.
type PlainPublicError struct {
	Host string
	Addr netip.Addr
}

func (e *PlainPublicError) Error() string {
	return fmt.Sprintf("%s resolves to the public address %s: %v", e.Host, e.Addr, ErrPlainPublic)
}

func (e *PlainPublicError) Is(target error) bool { return target == ErrPlainPublic }

// lookupAddrs resolves a host name; swapped in tests.
var lookupAddrs = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

type dialFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// privateDial wraps dial for plain connections: it resolves the host and
// connects only if every address is loopback or private, dialing the
// addresses it checked (no second lookup).
func privateDial(dial dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		var addrs []netip.Addr
		if a, ok := ipHost(host); ok {
			addrs = []netip.Addr{a}
		} else if addrs, err = lookupAddrs(ctx, host); err != nil {
			return nil, err
		}
		if len(addrs) == 0 {
			return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
		}
		for _, a := range addrs {
			if !IsPrivateAddr(a) {
				return nil, &PlainPublicError{Host: host, Addr: a}
			}
		}
		var last error
		for _, a := range addrs {
			c, err := dial(ctx, network, net.JoinHostPort(a.String(), port))
			if err == nil {
				return c, nil
			}
			last = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, last
	}
}

// plainSplit sends plain http (and ws, which the WebSocket library asks
// for as http) through plain, everything else through tls.
type plainSplit struct{ tls, plain *http.Transport }

func (s plainSplit) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "http" {
		return s.plain.RoundTrip(req)
	}
	return s.tls.RoundTrip(req)
}

func (s plainSplit) CloseIdleConnections() {
	s.tls.CloseIdleConnections()
	s.plain.CloseIdleConnections()
}

// GuardPlain is t for https, and for plain http a copy of t that goes
// direct through the private-address dialer. Every provider client is
// built on it (NewHTTPClient, asr.WSClient).
func GuardPlain(t *http.Transport) http.RoundTripper {
	p := t.Clone()
	p.Proxy = nil
	dial := t.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	p.DialContext = privateDial(dial)
	return plainSplit{tls: t, plain: p}
}

// CheckRedirect is the redirect policy of provider clients: no https →
// http downgrade, and a plain redirect stays on its host.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	prev := via[len(via)-1].URL
	if req.URL.Scheme == "http" && (prev.Scheme != "http" || !strings.EqualFold(prev.Host, req.URL.Host)) {
		return fmt.Errorf("refused a redirect from %s://%s to plain http://%s", prev.Scheme, prev.Host, req.URL.Host)
	}
	return nil
}

// IsNoRoute reports whether err is a connection that found no route to its
// host: what macOS reports when Local Network access is denied (or the
// server's network is down).
func IsNoRoute(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no route to host") || strings.Contains(s, "network is unreachable")
}
