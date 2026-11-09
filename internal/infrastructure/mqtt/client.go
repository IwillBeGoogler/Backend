// Package mqtt is the API's own connection to the MQTT broker (EMQX): a
// superuser client that publishes the server-side channels and consumes what
// browsers publish through shared subscriptions, so the API instances split
// the work. It reconnects by itself and resubscribes on every connection.
package mqtt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

// Origin is the user property that carries the id of the client that caused
// a message.
const Origin = "origin"

var (
	connected = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "iwillbegoogler_mqtt_client_connected",
		Help: "1 while the API's own broker connection is up.",
	})
	received = promauto.NewCounter(prometheus.CounterOpts{
		Name: "iwillbegoogler_mqtt_messages_received_total",
		Help: "Messages the API received from the broker.",
	})
	published = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iwillbegoogler_mqtt_messages_published_total",
		Help: "Messages the API published, by outcome.",
	}, []string{"result"})
)

// Config is how to reach the broker.
type Config struct {
	URL      string // tcp://host:1883, mqtt://..., or ws://host:8083/mqtt
	Username string
	Password string
	// ClientID defaults to "srv." plus a random suffix.
	ClientID string
}

// Subscription is a topic filter (a "$share/group/..." filter for shared
// work) and its maximum QoS.
type Subscription struct {
	Filter string
	QoS    byte
}

// Client is a connection to the broker.
type Client struct {
	cm *autopaho.ConnectionManager
}

var _ application.Bus = (*Client)(nil)

// Connect starts connecting and returns at once; it keeps retrying until
// ctx ends. on is called for every message received on subs, one at a time:
// it must hand work off rather than block.
func Connect(ctx context.Context, c Config, subs []Subscription, on func(application.Message)) (*Client, error) {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("mqtt: MQTT_URL %q is not a URL", c.URL)
	}
	if c.ClientID == "" {
		var b [6]byte
		_, _ = rand.Read(b[:])
		c.ClientID = "srv." + hex.EncodeToString(b[:])
	}
	opts := make([]paho.SubscribeOptions, len(subs))
	for i, s := range subs {
		opts[i] = paho.SubscribeOptions{Topic: s.Filter, QoS: s.QoS}
	}
	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		KeepAlive:                     30,
		CleanStartOnInitialConnection: true,
		SessionExpiryInterval:         0,
		ConnectUsername:               c.Username,
		ConnectPassword:               []byte(c.Password),
		ConnectTimeout:                10 * time.Second,
		ReconnectBackoff:              func(n int) time.Duration { return min(time.Duration(n+1)*time.Second, 10*time.Second) },
		OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
			connected.Set(1)
			if len(opts) == 0 {
				return
			}
			sub, err := cm.Subscribe(ctx, &paho.Subscribe{Subscriptions: opts})
			if err != nil {
				zap.L().Error("mqtt: subscribe", zap.Error(err))
				return
			}
			for i, rc := range sub.Reasons {
				if rc >= 0x80 && i < len(opts) {
					zap.L().Error("mqtt: subscription refused", zap.String("filter", opts[i].Topic), zap.Uint8("reason", rc))
				}
			}
			zap.L().Info("mqtt: connected", zap.String("url", u.Redacted()))
		},
		OnConnectError: func(err error) { connected.Set(0); zap.L().Warn("mqtt: connect", zap.Error(err)) },
		ClientConfig: paho.ClientConfig{
			ClientID: c.ClientID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(pr paho.PublishReceived) (bool, error) {
				received.Inc()
				if on != nil {
					on(fromPacket(pr.Packet))
				}
				return true, nil
			}},
			OnClientError: func(err error) { connected.Set(0); zap.L().Warn("mqtt: client", zap.Error(err)) },
			OnServerDisconnect: func(d *paho.Disconnect) {
				connected.Set(0)
				zap.L().Warn("mqtt: broker disconnected us", zap.Uint8("reason", d.ReasonCode))
			},
		},
	}
	cm, err := autopaho.NewConnection(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Client{cm: cm}, nil
}

func fromPacket(p *paho.Publish) application.Message {
	m := application.Message{Topic: p.Topic, Payload: p.Payload, QoS: p.QoS, Retain: p.Retain}
	if p.Properties != nil {
		m.Origin = p.Properties.User.Get(Origin)
		if p.Properties.MessageExpiry != nil {
			m.Expiry = time.Duration(*p.Properties.MessageExpiry) * time.Second
		}
	}
	return m
}

func toPacket(m application.Message) *paho.Publish {
	p := &paho.Publish{Topic: m.Topic, Payload: m.Payload, QoS: m.QoS, Retain: m.Retain}
	if m.Expiry > 0 || m.Origin != "" {
		p.Properties = &paho.PublishProperties{}
		if m.Expiry > 0 {
			sec := uint32(m.Expiry / time.Second)
			p.Properties.MessageExpiry = &sec
		}
		if m.Origin != "" {
			p.Properties.User = paho.UserProperties{{Key: Origin, Value: m.Origin}}
		}
	}
	return p
}

// ErrDown is returned by Publish while the broker is unreachable.
var ErrDown = errors.New("mqtt: broker unreachable")

// Publish sends a message; it fails fast while the connection is down.
func (c *Client) Publish(ctx context.Context, m application.Message) error {
	resp, err := c.cm.Publish(ctx, toPacket(m))
	switch {
	case errors.Is(err, autopaho.ConnectionDownError):
		published.WithLabelValues("down").Inc()
		return ErrDown
	case err != nil:
		published.WithLabelValues("error").Inc()
		return err
	case resp != nil && resp.ReasonCode >= 0x80 && resp.ReasonCode != 0x10: // 0x10: no matching subscribers
		published.WithLabelValues("refused").Inc()
		return fmt.Errorf("mqtt: publish %s refused (reason %#x)", m.Topic, resp.ReasonCode)
	}
	published.WithLabelValues("ok").Inc()
	return nil
}

// Connected waits for the first connection (for tests and start-up checks).
func (c *Client) Connected(ctx context.Context) error { return c.cm.AwaitConnection(ctx) }

// Close disconnects.
func (c *Client) Close(ctx context.Context) error { return c.cm.Disconnect(ctx) }
