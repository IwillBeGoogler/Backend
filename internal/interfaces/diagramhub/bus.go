package diagramhub

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/topic"
)

// The hub talks to the broker through application.Bus (publishing) and
// receives through OnMessage; infrastructure/mqtt connects the two.

// Subscriptions are what the API subscribes to on the broker: the topics
// browsers publish to (shared, so one instance handles each message) and the
// file events and ACL cuts, which every instance reads to learn that a document was
// replaced or deleted and drop what it holds about it.
func Subscriptions() (shared, all []string) {
	for _, f := range topic.ServerFilters {
		shared = append(shared, topic.Shared(f))
	}
	return shared, []string{"dg/f/+/events", topic.ACLDrop}
}

// Message lifetimes at the broker.
const (
	eventExpiry  = time.Minute      // an event is stale after that for a subscriber that was away
	syncExpiry   = 30 * time.Second // join answers
	rosterExpiry = 90 * time.Second // a presence the client stops refreshing disappears
)

// Attach gives the hub its broker connection. Until then publishing is
// dropped (and logged at debug level).
func (h *Hub) Attach(b application.Bus) {
	h.mu.Lock()
	h.bus = b
	h.mu.Unlock()
}

func (h *Hub) publish(ctx context.Context, m application.Message) {
	h.mu.Lock()
	b := h.bus
	h.mu.Unlock()
	if b == nil {
		zap.L().Debug("diagramhub: no broker", zap.String("topic", m.Topic))
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := b.Publish(ctx, m); err != nil && h.ctx.Err() == nil {
		zap.L().Warn("diagramhub: publish", zap.String("topic", m.Topic), zap.Error(err))
	}
}

// emit queues an event for the single publisher goroutine, so the events a
// use case raises reach the broker in the order it raised them and a slow
// broker never holds up a request.
func (h *Hub) emit(m application.Message) {
	select {
	case h.out <- m:
	default:
		zap.L().Warn("diagramhub: event queue full, event dropped", zap.String("topic", m.Topic))
	}
}

func (h *Hub) publisher() {
	defer h.wg.Done()
	for {
		select {
		case <-h.ctx.Done():
			return
		case m := <-h.out:
			h.publish(h.ctx, m)
		}
	}
}

func (h *Hub) ctl(ctx context.Context, clientID string, v any) {
	h.publish(ctx, application.Message{Topic: topic.CtlTopic(clientID), Payload: mustJSON(v), QoS: 1, Expiry: eventExpiry})
}
