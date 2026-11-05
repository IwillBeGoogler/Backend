package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestPrivate(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.169.254", "100.64.0.1",
		"0.0.0.0", "224.0.0.1", "240.0.0.1", "::1", "::", "fe80::1", "fc00::1", "fd12::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
		"64:ff9b::7f00:1", "ff02::1", "198.18.0.1"} {
		if !Private(netip.MustParseAddr(s)) {
			t.Errorf("%s should be private", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111", "172.32.0.1", "100.128.0.1"} {
		if Private(netip.MustParseAddr(s)) {
			t.Errorf("%s should be public", s)
		}
	}
}

func TestPrivateHost(t *testing.T) {
	for _, h := range []string{"localhost", "LOCALHOST.", "app.localhost", "db.local", "svc.internal", "127.0.0.1", "[::1]", "10.0.0.5"} {
		if !PrivateHost(h) {
			t.Errorf("%s should be private", h)
		}
	}
	for _, h := range []string{"example.com", "8.8.8.8", "internal.example.com"} {
		if PrivateHost(h) {
			t.Errorf("%s should be public", h)
		}
	}
}

func lookupTo(ips ...string) Lookup {
	return func(context.Context, string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, s := range ips {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	if _, err := Resolve(ctx, "evil.example", false, lookupTo("8.8.8.8", "10.0.0.1")); !errors.Is(err, ErrBlocked) {
		t.Errorf("one private answer among public ones must block, got %v", err)
	}
	if _, err := Resolve(ctx, "rebind.example", false, lookupTo("169.254.169.254")); !errors.Is(err, ErrBlocked) {
		t.Errorf("metadata address = %v", err)
	}
	if ips, err := Resolve(ctx, "ok.example", false, lookupTo("8.8.8.8")); err != nil || len(ips) != 1 {
		t.Errorf("public host: %v %v", ips, err)
	}
	if _, err := Resolve(ctx, "127.0.0.1", false, nil); !errors.Is(err, ErrBlocked) {
		t.Errorf("literal loopback = %v", err)
	}
	if _, err := Resolve(ctx, "::ffff:192.168.0.1", false, nil); !errors.Is(err, ErrBlocked) {
		t.Errorf("mapped private = %v", err)
	}
	if _, err := Resolve(ctx, "127.0.0.1", true, nil); err != nil {
		t.Errorf("allowPrivate: %v", err)
	}
	if _, err := Resolve(ctx, "gone.example", false, func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("nx") }); err == nil {
		t.Error("unresolvable host must fail")
	}
}

func TestCheckURL(t *testing.T) {
	ctx := context.Background()
	pub := lookupTo("8.8.8.8")
	for _, raw := range []string{"ftp://example.com/x", "http://user:pw@example.com/", "not a url", "http:///x", "http://localhost/hook", "http://127.0.0.1:8080/", "http://[::1]/", "http://169.254.169.254/latest"} {
		if _, err := CheckURL(ctx, raw, false, pub); err == nil {
			t.Errorf("%q should be rejected", raw)
		}
	}
	if _, err := CheckURL(ctx, "https://hooks.example.com/in", false, pub); err != nil {
		t.Errorf("public URL: %v", err)
	}
	if _, err := CheckURL(ctx, "http://127.0.0.1:9000/in", true, nil); err != nil {
		t.Errorf("allowed private URL: %v", err)
	}
}

// The dial is pinned: a name that resolves to a public address at the check
// and to a private one at the dial is refused, and the connection goes to
// the address that was checked.
func TestDialerPinsTheAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener")
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rebind := Dialer{Lookup: lookupTo("127.0.0.1")}
	if _, err := rebind.DialContext(ctx, "tcp", net.JoinHostPort("rebind.example", port)); !errors.Is(err, ErrBlocked) {
		t.Fatalf("rebinding to loopback must be blocked, got %v", err)
	}
	if _, err := (Dialer{}).DialContext(ctx, "tcp", ln.Addr().String()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("literal loopback must be blocked, got %v", err)
	}
	// allowed: dials the resolved address, not the name
	c, err := Dialer{AllowPrivate: true, Lookup: lookupTo("127.0.0.1")}.DialContext(ctx, "tcp", net.JoinHostPort("anything.example", port))
	if err != nil {
		t.Fatalf("allowPrivate dial: %v", err)
	}
	c.Close()
}
