package application

import (
	"context"
	"time"
)

// Message is one MQTT message as the realtime hub sees it.
type Message struct {
	Topic   string
	Payload []byte
	QoS     byte
	Retain  bool
	// Expiry is how long the broker may keep the message for a subscriber
	// that is offline, or keep it retained (0: no expiry).
	Expiry time.Duration
	// Origin is the client id that caused the message (the "origin" user
	// property): clients drop what they caused themselves.
	Origin string
}

// Bus publishes to the realtime broker. Publish returns once the broker
// has taken the message (QoS 1) or it was written to the socket (QoS 0).
type Bus interface {
	Publish(ctx context.Context, m Message) error
}
