package mqtt

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/packets"
)

// fakeBroker is a tiny MQTT v5 broker: enough to accept a client, answer
// SUBSCRIBE and PUBLISH with configurable reason codes, push messages and
// drop connections.
type fakeBroker struct {
	t  *testing.T
	ln net.Listener

	mu           sync.Mutex
	subReasons   []byte // nil: all granted
	pubReason    byte
	noPubAck     bool
	dropOnSub    bool
	connects     []*packets.Connect
	subs         []string
	pubs         []*packets.Publish
	conns        []net.Conn
	connectCount int
}

func newFakeBroker(t *testing.T) *fakeBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBroker{t: t, ln: ln}
	t.Cleanup(b.close)
	go b.accept()
	return b
}

func (b *fakeBroker) url() string { return "tcp://" + b.ln.Addr().String() }

func (b *fakeBroker) close() {
	b.ln.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.conns {
		c.Close()
	}
}

func (b *fakeBroker) accept() {
	for {
		c, err := b.ln.Accept()
		if err != nil {
			return
		}
		b.mu.Lock()
		b.conns = append(b.conns, c)
		b.mu.Unlock()
		go b.serve(c)
	}
}

func send(c net.Conn, cp *packets.ControlPacket) { _, _ = cp.WriteTo(c) }

func (b *fakeBroker) serve(c net.Conn) {
	defer c.Close()
	for {
		cp, err := packets.ReadPacket(c)
		if err != nil {
			return
		}
		switch p := cp.Content.(type) {
		case *packets.Connect:
			b.mu.Lock()
			b.connects = append(b.connects, p)
			b.connectCount++
			b.mu.Unlock()
			ack := packets.NewControlPacket(packets.CONNACK)
			send(c, ack)
		case *packets.Subscribe:
			b.mu.Lock()
			for _, s := range p.Subscriptions {
				b.subs = append(b.subs, s.Topic)
			}
			drop, reasons := b.dropOnSub, b.subReasons
			b.mu.Unlock()
			if drop {
				return
			}
			ack := packets.NewControlPacket(packets.SUBACK)
			sa := ack.Content.(*packets.Suback)
			sa.PacketID = p.PacketID
			sa.Reasons = reasons
			if sa.Reasons == nil {
				for _, s := range p.Subscriptions {
					sa.Reasons = append(sa.Reasons, s.QoS)
				}
			}
			send(c, ack)
		case *packets.Publish:
			b.mu.Lock()
			b.pubs = append(b.pubs, p)
			reason, skip := b.pubReason, b.noPubAck
			b.mu.Unlock()
			if p.QoS == 1 && !skip {
				ack := packets.NewControlPacket(packets.PUBACK)
				pa := ack.Content.(*packets.Puback)
				pa.PacketID, pa.ReasonCode = p.PacketID, reason
				send(c, ack)
			}
		case *packets.Pingreq:
			send(c, packets.NewControlPacket(packets.PINGRESP))
		case *packets.Disconnect:
			return
		}
	}
}

// lastConn returns the newest client connection.
func (b *fakeBroker) lastConn() net.Conn {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.conns) == 0 {
		return nil
	}
	return b.conns[len(b.conns)-1]
}

// push delivers a PUBLISH to the connected client.
func (b *fakeBroker) push(topic string, payload []byte, props *packets.Properties) {
	cp := packets.NewControlPacket(packets.PUBLISH)
	p := cp.Content.(*packets.Publish)
	p.Topic, p.Payload, p.Properties = topic, payload, props
	send(b.lastConn(), cp)
}

// kick sends a server DISCONNECT with a reason code.
func (b *fakeBroker) kick(reason byte) {
	cp := packets.NewControlPacket(packets.DISCONNECT)
	cp.Content.(*packets.Disconnect).ReasonCode = reason
	send(b.lastConn(), cp)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}
