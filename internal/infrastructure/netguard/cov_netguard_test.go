package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestCovDefaultLookup(t *testing.T) {
	ips, err := DefaultLookup(context.Background(), "127.0.0.1")
	if err != nil || len(ips) != 1 || ips[0] != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("%v %v", ips, err)
	}
	if _, err := DefaultLookup(context.Background(), "nonexistent.invalid"); err == nil {
		t.Fatal("want error")
	}
}

func TestCovResolveLookupFailures(t *testing.T) {
	ctx := context.Background()
	if _, err := Resolve(ctx, "example.com", false, func(context.Context, string) ([]netip.Addr, error) {
		return nil, errors.New("boom")
	}); err == nil {
		t.Fatal("lookup err")
	}
	if _, err := Resolve(ctx, "example.com", false, func(context.Context, string) ([]netip.Addr, error) {
		return nil, nil
	}); err == nil {
		t.Fatal("empty answer")
	}
	// nil lookup falls back to DefaultLookup
	if _, err := Resolve(ctx, "nonexistent.invalid", false, nil); err == nil {
		t.Fatal("default lookup")
	}
}

func TestCovDialContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
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
	ctx := context.Background()
	if _, err := (Dialer{}).DialContext(ctx, "tcp", "noport"); err == nil {
		t.Fatal("split err")
	}
	if _, err := (Dialer{}).DialContext(ctx, "tcp", ln.Addr().String()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("want blocked, got %v", err)
	}
	c, err := (Dialer{AllowPrivate: true}).DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	// all addresses fail: closed port, explicit timeout
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	d := Dialer{AllowPrivate: true, Timeout: time.Second}
	if _, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port)); err == nil {
		t.Fatal("want dial error")
	}
}
