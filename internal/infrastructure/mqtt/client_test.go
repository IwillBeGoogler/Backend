package mqtt

import (
	"context"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/application"
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
