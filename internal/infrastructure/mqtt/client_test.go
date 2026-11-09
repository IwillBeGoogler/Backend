package mqtt

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/packets"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

func TestPacketRoundTrip(t *testing.T) {
	in := application.Message{Topic: "dg/f/f1/events", Payload: []byte("x"), QoS: 1, Retain: true, Expiry: 90 * time.Second, Origin: "u1.abcdefgh"}
	p := toPacket(in)
	if p.Topic != in.Topic || p.QoS != 1 || !p.Retain || p.Properties == nil || *p.Properties.MessageExpiry != 90 || p.Properties.User.Get(Origin) != "u1.abcdefgh" {
		t.Fatalf("packet = %+v %+v", p, p.Properties)
	}
	if out := fromPacket(p); out.Origin != in.Origin || out.Expiry != in.Expiry || string(out.Payload) != "x" || !out.Retain {
		t.Errorf("message = %+v", out)
	}
	if p := toPacket(application.Message{Topic: "t"}); p.Properties != nil {
		t.Error("a bare message carries properties")
	}
}

func TestConnectRejectsABadURL(t *testing.T) {
	for _, u := range []string{"", "not a url", "://x"} {
		if _, err := Connect(context.Background(), Config{URL: u}, nil, nil); err == nil {
			t.Errorf("URL %q accepted", u)
		}
	}
}

func connectTo(t *testing.T, url string, subs []Subscription, on func(application.Message)) *Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, err := Connect(ctx, Config{URL: url, Username: "srv", Password: "pw", ClientID: "test-client"}, subs, on)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	cctx, ccancel := context.WithTimeout(ctx, 8*time.Second)
	defer ccancel()
	if err := c.Connected(cctx); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClientAgainstBroker(t *testing.T) {
	b := newFakeBroker(t)
	var mu sync.Mutex
	var got []application.Message
	c := connectTo(t, b.url(), []Subscription{{Filter: "$share/api/dg/+/in", QoS: 1}}, func(m application.Message) {
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	})

	waitFor(t, "subscribe", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.subs) == 1 })
	b.mu.Lock()
	cn := b.connects[0]
	b.mu.Unlock()
	if cn.ClientID != "test-client" || cn.Username != "srv" || string(cn.Password) != "pw" {
		t.Errorf("connect = %+v", cn)
	}

	ctx := context.Background()
	if err := c.Publish(ctx, application.Message{Topic: "dg/f/out", Payload: []byte("a"), QoS: 0}); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(ctx, application.Message{Topic: "dg/f/out1", Payload: []byte("b"), QoS: 1, Origin: "u1", Expiry: time.Minute}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "publishes", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.pubs) == 2 })

	// no-subscriber reason (0x10) counts as success; a refusal is an error
	b.mu.Lock()
	b.pubReason = 0x10
	b.mu.Unlock()
	if err := c.Publish(ctx, application.Message{Topic: "t", QoS: 1}); err != nil {
		t.Errorf("0x10: %v", err)
	}
	b.mu.Lock()
	b.pubReason = 0x87
	b.mu.Unlock()
	if err := c.Publish(ctx, application.Message{Topic: "t", QoS: 1}); err == nil {
		t.Errorf("refused: %v", err)
	}

	// A QoS 1 publish nobody acknowledges ends with the caller's deadline.
	b.mu.Lock()
	b.noPubAck = true
	b.mu.Unlock()
	tctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if err := c.Publish(tctx, application.Message{Topic: "t", QoS: 1}); err == nil || errors.Is(err, ErrDown) {
		t.Errorf("timeout publish: %v", err)
	}

	// Inbound message with properties.
	exp := uint32(30)
	b.push("dg/f1/in", []byte("hi"), &packets.Properties{MessageExpiry: &exp, User: []packets.User{{Key: Origin, Value: "u9"}}})
	waitFor(t, "inbound", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })
	if m := got[0]; m.Topic != "dg/f1/in" || string(m.Payload) != "hi" || m.Origin != "u9" || m.Expiry != 30*time.Second {
		t.Errorf("inbound = %+v", m)
	}

	// The socket dies without a DISCONNECT: OnClientError, then reconnect.
	before := func() int { b.mu.Lock(); defer b.mu.Unlock(); return b.connectCount }()
	b.lastConn().Close()
	waitFor(t, "reconnect after socket drop", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.connectCount > before })
	waitFor(t, "resubscribe after socket drop", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.subs) >= 2 })

	// Server disconnect: the client reconnects and subscribes again.
	b.kick(0x8B)
	waitFor(t, "reconnect", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.connectCount >= 2 })
	waitFor(t, "resubscribe", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.subs) >= 3 })
}

func TestClientNoSubscriptionsAndNilHandler(t *testing.T) {
	b := newFakeBroker(t)
	c := connectTo(t, b.url(), nil, nil)
	b.push("x", []byte("y"), nil) // nil handler: counted, dropped
	if err := c.Publish(context.Background(), application.Message{Topic: "t"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "publish", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.pubs) == 1 })
	b.mu.Lock()
	n := len(b.subs)
	b.mu.Unlock()
	if n != 0 {
		t.Errorf("subscribed with no filters: %d", n)
	}
}

func TestClientRefusedSubscription(t *testing.T) {
	b := newFakeBroker(t)
	b.mu.Lock()
	b.subReasons = []byte{0x8F}
	b.mu.Unlock()
	connectTo(t, b.url(), []Subscription{{Filter: "bad/#", QoS: 0}}, nil)
	waitFor(t, "subscribe", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.subs) == 1 })
	time.Sleep(100 * time.Millisecond) // the refusal is logged after SUBACK
}

func TestClientSubscribeFailure(t *testing.T) {
	b := newFakeBroker(t)
	b.mu.Lock()
	b.dropOnSub = true // the broker never answers SUBSCRIBE
	b.mu.Unlock()
	// Subscribe uses the Connect context, so its deadline ends the wait.
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	if _, err := Connect(ctx, Config{URL: b.url()}, []Subscription{{Filter: "a/#"}}, nil); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond) // the error is logged when Subscribe returns
}

func TestClientDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens here
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := Connect(ctx, Config{URL: "tcp://" + addr}, nil, nil) // random client id
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1300 * time.Millisecond) // the first attempt waits one backoff step, then OnConnectError fires
	if err := c.Publish(ctx, application.Message{Topic: "t"}); !errors.Is(err, ErrDown) {
		t.Errorf("publish while down: %v", err)
	}
	cctx, ccancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer ccancel()
	if err := c.Connected(cctx); err == nil {
		t.Error("Connected succeeded without a broker")
	}
}
