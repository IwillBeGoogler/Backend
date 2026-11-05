// Package netguard keeps the server from being used to reach internal
// services (SSRF) when it connects out for a user: webhook deliveries and
// live database introspection. Hosts are resolved once, every address is
// checked, and the connection is made to the checked address itself, so a
// DNS answer that changes between the check and the dial (rebinding) gets
// no second chance.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// ErrBlocked is a target that is private, loopback, link-local or
// otherwise not a routable internet address.
var ErrBlocked = errors.New("private, loopback and link-local addresses are not allowed")

// Lookup resolves a host name to addresses.
type Lookup func(ctx context.Context, host string) ([]netip.Addr, error)

// DefaultLookup uses the system resolver.
func DefaultLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, len(ips))
	for i, ip := range ips {
		out[i] = ip.Unmap()
	}
	return out, nil
}

var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 reaches IPv4 private ranges
	netip.MustParsePrefix("2002::/16"),    // 6to4
}

// Private reports whether ip is anything but a routable internet address:
// loopback, private, link-local, CGNAT, multicast, unspecified, reserved.
func Private(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// PrivateHost reports whether host is, or by its name must be, internal: a
// private IP literal or a localhost / .local / .internal name.
func PrivateHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return true
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		return Private(ip)
	}
	return false
}

// Resolve returns the addresses host may be dialed at. A literal IP is
// checked as is; a name is resolved and every answer must be public. With
// allowPrivate nothing is refused.
func Resolve(ctx context.Context, host string, allowPrivate bool, lookup Lookup) ([]netip.Addr, error) {
	host = strings.Trim(host, "[]")
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if !allowPrivate && Private(ip) {
			return nil, ErrBlocked
		}
		return []netip.Addr{ip}, nil
	}
	if !allowPrivate && PrivateHost(host) {
		return nil, ErrBlocked
	}
	if lookup == nil {
		lookup = DefaultLookup
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("the host name does not resolve")
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("the host name does not resolve")
	}
	if !allowPrivate {
		for _, ip := range ips {
			if Private(ip) {
				return nil, fmt.Errorf("the host resolves to a private address: %w", ErrBlocked)
			}
		}
	}
	return ips, nil
}

// CheckURL validates a user-supplied http(s) URL: scheme, no credentials,
// and a host that passes Resolve.
func CheckURL(ctx context.Context, raw string, allowPrivate bool, lookup Lookup) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("only http and https URLs are allowed")
	}
	if u.User != nil {
		return nil, errors.New("URLs with credentials are not allowed")
	}
	if _, err := Resolve(ctx, u.Hostname(), allowPrivate, lookup); err != nil {
		return nil, err
	}
	return u, nil
}

// Dialer connects to host:port after the check, to the address that was
// checked (pinned), trying the answers in order.
type Dialer struct {
	AllowPrivate bool
	Lookup       Lookup
	Timeout      time.Duration
}

// DialContext has the signature of http.Transport.DialContext.
func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := Resolve(ctx, host, d.AllowPrivate, d.Lookup)
	if err != nil {
		return nil, err
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	nd := net.Dialer{Timeout: timeout}
	var last error
	for _, ip := range ips {
		c, err := nd.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}
