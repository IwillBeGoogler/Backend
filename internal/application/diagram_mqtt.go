package application

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/topic"
)

// Who may connect to the MQTT broker and what they may publish or subscribe
// to. The broker (EMQX) asks over HTTP, on every connect and, per client and
// topic, on every publish and subscribe (it caches the answers briefly); the
// rules are the REST routes' own: fileAccessVia and projectAccessVia.
//
//   - The API itself connects with the shared MQTT_USERNAME / MQTT_PASSWORD
//     and is a superuser.
//   - A signed-in user connects as username "ticket" with a single-use
//     stream ticket as password, and client id "u<userId>.<nonce>".
//   - A share-link holder connects as username "share" with the link's token
//     as password and client id "g.<nonce>". The broker never tells the
//     access check the password again, so the token is kept (by client id,
//     for MQTTGuestTTL) in MQTTGuests.
//
// A client id is its owner's name tag: topics that carry a client id are only
// open to the client of that id, which is what lets the server trust the
// identity behind a cursor or a presence.

// Usernames of browser connections.
const (
	MQTTTicketUser = "ticket"
	MQTTShareUser  = "share"
)

// MQTTGuestTTL is how long a share-link connection's token is remembered.
const MQTTGuestTTL = 12 * time.Hour

// MQTTGuests keeps the share token of guest connections between the
// broker's authentication and access questions, which may reach different
// API instances (Redis when there are several).
type MQTTGuests interface {
	Put(ctx context.Context, clientID, token string, ttl time.Duration) error
	// Get returns domain.ErrNotFound for an unknown client id.
	Get(ctx context.Context, clientID string) (string, error)
}

// MQTTClients remembers which broker client ids rest on a share link or a
// member's access to a project, so they can be cut when it is revoked. Keys
// are shareClientsKey and userClientsKey.
type MQTTClients interface {
	Add(ctx context.Context, key, clientID string, ttl time.Duration) error
	// Take returns the client ids under key and forgets them.
	Take(ctx context.Context, key string) ([]string, error)
}

// BrokerKicker disconnects a client from the broker (EMQX REST API).
type BrokerKicker interface {
	Enabled() bool
	Kick(ctx context.Context, clientID string) error
}

func shareClientsKey(projectID, shareID string) string { return "p:" + projectID + ":share:" + shareID }
func userClientsKey(projectID string, user int) string {
	return "p:" + projectID + ":user:" + strconv.Itoa(user)
}

func accountClientsKey(user int) string { return "user:" + strconv.Itoa(user) }

// MQTTAccess answers the broker.
type MQTTAccess struct {
	D       *Diagrams
	Tickets ResetTokens
	Guests  MQTTGuests
	// Clients tracks client ids per share link / member (nil: no cut-off).
	Clients MQTTClients
	// Bus publishes the "denied" ctl message; Kicker disconnects the clients
	// (nil or not enabled: only the message is sent).
	Bus    Bus
	Kicker BrokerKicker
	// ServerUser and ServerPassword are the API's own broker login.
	ServerUser, ServerPassword string
	// CollabEnabled opens the Yjs topics.
	CollabEnabled bool
	// TTL is how long a client's role in a file or project is remembered
	// (default 30 s): a revoked share link or removed member loses access
	// within that time plus the broker's own cache.
	TTL time.Duration
	// OnDenied hears of a client whose Yjs write was refused for its role.
	OnDenied func(clientID, fileID string)

	now   func() time.Time
	mu    sync.Mutex
	cache map[aclKey]aclEntry
	// gen counts the cuts of each client, so a lookup that began before a cut
	// does not store its (stale) answer after it.
	gen map[string]uint64
}

type aclKey struct{ client, scope, id string }

type aclEntry struct {
	a   ChannelAccess
	err error
	at  time.Time
}

const maxACLCache = 50_000

func (m *MQTTAccess) ttl() time.Duration {
	if m.TTL > 0 {
		return m.TTL
	}
	return 30 * time.Second
}

// MQTTAuth is the answer to a connection attempt.
type MQTTAuth struct {
	Allow     bool
	Superuser bool
}

// Authenticate decides a CONNECT. A bad ticket or token is a plain deny.
func (m *MQTTAccess) Authenticate(ctx context.Context, clientID, username, password string) MQTTAuth {
	if m.ServerUser != "" && eq(username, m.ServerUser) && eq(password, m.ServerPassword) {
		return MQTTAuth{Allow: true, Superuser: true}
	}
	user, ok := topic.ParseClientID(clientID)
	if !ok || password == "" {
		return MQTTAuth{}
	}
	switch username {
	case MQTTTicketUser:
		if user == 0 || m.Tickets == nil {
			return MQTTAuth{}
		}
		id, err := m.Tickets.Consume(ctx, password)
		if err != nil || id != user {
			return MQTTAuth{}
		}
		if m.Clients != nil && m.Clients.Add(ctx, accountClientsKey(user), clientID, MQTTGuestTTL) != nil {
			return MQTTAuth{}
		}
		return MQTTAuth{Allow: true}
	case MQTTShareUser:
		if user != 0 || m.Guests == nil {
			return MQTTAuth{}
		}
		project, share, err := m.D.ShareLinkOf(ctx, password)
		if err != nil {
			return MQTTAuth{}
		}
		// Client ids are public (rosters carry them) and answers are remembered
		// by client id: an id stays with the link that first used it.
		held, err := m.Guests.Get(ctx, clientID)
		if err == nil && !eq(held, password) || err != nil && !errors.Is(err, domain.ErrNotFound) {
			return MQTTAuth{}
		}
		if m.Clients != nil {
			if m.Clients.Add(ctx, shareClientsKey(project, share), clientID, MQTTGuestTTL) != nil {
				return MQTTAuth{}
			}
		}
		return MQTTAuth{Allow: m.Guests.Put(ctx, clientID, password, MQTTGuestTTL) == nil}
	}
	return MQTTAuth{}
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// Authorize decides a PUBLISH or SUBSCRIBE (action "publish" or
// "subscribe"; topic is the filter for a subscription).
func (m *MQTTAccess) Authorize(ctx context.Context, clientID, username, action, topicName string) bool {
	user, ok := topic.ParseClientID(clientID)
	if !ok || user > 0 && username != MQTTTicketUser || user == 0 && username != MQTTShareUser {
		return false
	}
	var t topic.Topic
	switch action {
	case "publish":
		t, ok = topic.Parse(topicName)
	case "subscribe":
		t, ok = topic.ParseFilter(topicName)
	default:
		return false
	}
	if !ok {
		return false
	}
	own := t.ClientID == clientID
	viewer := func(scope, id string) bool { return m.role(ctx, clientID, user, scope, id, diagram.RoleViewer) }
	if action == "subscribe" {
		switch t.Kind {
		case topic.ProjectEvents, topic.ProjectRoster:
			return viewer("project", t.ProjectID)
		case topic.FileEvents, topic.FileRoster, topic.YjsDown, topic.AwarenessOut:
			return viewer("file", t.FileID)
		case topic.YjsSync, topic.RTCInbox:
			return own && viewer("file", t.FileID)
		case topic.Ctl:
			return own
		}
		return false
	}
	switch t.Kind {
	case topic.YjsReq:
		return own && m.CollabEnabled && viewer("file", t.FileID)
	case topic.YjsUp:
		if !own || !m.CollabEnabled {
			return false
		}
		if m.role(ctx, clientID, user, "file", t.FileID, diagram.RoleEditor) {
			return true
		}
		if m.OnDenied != nil && viewer("file", t.FileID) {
			m.OnDenied(clientID, t.FileID)
		}
		return false
	case topic.AwarenessUp:
		return own && m.CollabEnabled && viewer("file", t.FileID)
	case topic.Presence, topic.RTCUp:
		return own && viewer("file", t.FileID)
	}
	return false
}

// role reports whether the client holds at least min on a file or project.
func (m *MQTTAccess) role(ctx context.Context, clientID string, user int, scope, id string, min diagram.Role) bool {
	a, err := m.channel(ctx, clientID, user, scope, id)
	return err == nil && a.Role.Allows(min)
}

// FileAccess is what the client holds in a file (for the realtime hub: its
// role, name and project). Not found when it holds nothing.
func (m *MQTTAccess) FileAccess(ctx context.Context, clientID, fileID string) (ChannelAccess, error) {
	user, ok := topic.ParseClientID(clientID)
	if !ok {
		return ChannelAccess{}, domain.ErrUnauthorized
	}
	return m.channel(ctx, clientID, user, "file", fileID)
}

func (m *MQTTAccess) channel(ctx context.Context, clientID string, user int, scope, id string) (ChannelAccess, error) {
	k := aclKey{clientID, scope, id}
	now := m.clock()
	m.mu.Lock()
	e, hit := m.cache[k]
	m.mu.Unlock()
	if hit && now.Sub(e.at) < m.ttl() {
		return e.a, e.err
	}
	m.mu.Lock()
	gen := m.gen[clientID]
	m.mu.Unlock()
	token := ""
	if user == 0 {
		var err error
		if token, err = m.Guests.Get(ctx, clientID); err != nil {
			return ChannelAccess{}, err
		}
	}
	a, err := m.D.OpenChannel(ctx, user, token, scope, id)
	var refusal *diagram.Error
	if err != nil && !errors.As(err, &refusal) && !errors.Is(err, domain.ErrUnauthorized) {
		return ChannelAccess{}, err // an outage is not remembered
	}
	if err == nil && user > 0 && a.ProjectID != "" && m.Clients != nil {
		_ = m.Clients.Add(ctx, userClientsKey(a.ProjectID, user), clientID, MQTTGuestTTL)
	}
	m.mu.Lock()
	if m.cache == nil || len(m.cache) >= maxACLCache {
		m.cache = map[aclKey]aclEntry{}
	}
	if m.gen[clientID] == gen {
		m.cache[k] = aclEntry{a, err, now}
	}
	m.mu.Unlock()
	return a, err
}

func (m *MQTTAccess) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// Forget drops what is remembered about a file (it was deleted, or its
// sharing changed) so the next question is asked afresh.
func (m *MQTTAccess) Forget(fileID string) {
	m.mu.Lock()
	for k := range m.cache {
		if k.id == fileID {
			delete(m.cache, k)
		}
	}
	m.mu.Unlock()
}

// ShareRevoked cuts the guests that connected with a revoked share link.
func (m *MQTTAccess) ShareRevoked(ctx context.Context, projectID, shareID string) {
	m.cut(ctx, shareClientsKey(projectID, shareID))
}

// MemberLost cuts a member's sessions in a project (removed, or role lowered);
// the client reconnects and gets the role it now holds.
func (m *MQTTAccess) MemberLost(ctx context.Context, projectID string, userID int) {
	m.cut(ctx, userClientsKey(projectID, userID))
}

// AccountBlocked cuts every session of a banned or suspended account; it
// cannot reconnect, since a blocked account gets no new ticket.
func (m *MQTTAccess) AccountBlocked(ctx context.Context, userID int) {
	m.cut(ctx, accountClientsKey(userID))
}

// cut drops the ACL cache entries of the clients under key, tells them they
// are denied and disconnects them.
func (m *MQTTAccess) cut(ctx context.Context, key string) {
	if m.Clients == nil {
		return
	}
	ids, err := m.Clients.Take(ctx, key)
	if err != nil {
		zap.L().Warn("mqtt: list clients to cut", zap.String("key", key), zap.Error(err))
		return
	}
	if len(ids) == 0 {
		return
	}
	m.DropClients(ids)
	if m.Bus != nil {
		if p, err := json.Marshal(map[string][]string{"clients": ids}); err == nil {
			if err := m.Bus.Publish(ctx, Message{Topic: topic.ACLDrop, Payload: p, QoS: 1, Expiry: time.Minute}); err != nil {
				zap.L().Warn("mqtt: announce cut", zap.Error(err))
			}
		}
	}
	payload, _ := json.Marshal(map[string]string{"type": "ctl", "code": "denied"})
	for _, id := range ids {
		if m.Bus != nil {
			if err := m.Bus.Publish(ctx, Message{Topic: topic.CtlTopic(id), Payload: payload, QoS: 1, Expiry: time.Minute}); err != nil {
				zap.L().Warn("mqtt: send denied", zap.String("client", id), zap.Error(err))
			}
		}
		if m.Kicker != nil && m.Kicker.Enabled() {
			if err := m.Kicker.Kick(ctx, id); err != nil {
				zap.L().Warn("mqtt: kick client", zap.String("client", id), zap.Error(err))
			}
		}
	}
}

// DropClients forgets the cached answers of the clients and makes lookups
// already under way not store theirs.
func (m *MQTTAccess) DropClients(ids []string) {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.cache {
		if set[k.client] {
			delete(m.cache, k)
		}
	}
	if m.gen == nil || len(m.gen) >= maxACLCache {
		m.gen = map[string]uint64{}
	}
	for _, id := range ids {
		m.gen[id]++
	}
}

// OnACLDrop handles the server-only topic.ACLDrop message another API
// instance sent when it cut clients.
func (m *MQTTAccess) OnACLDrop(payload []byte) {
	var v struct {
		Clients []string `json:"clients"`
	}
	if json.Unmarshal(payload, &v) == nil {
		m.DropClients(v.Clients)
	}
}

// MemoryClients is MQTTClients for a single API instance (and tests).
type MemoryClients struct {
	mu sync.Mutex
	m  map[string][]string
}

func (c *MemoryClients) Add(_ context.Context, key, clientID string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string][]string{}
	}
	for _, e := range c.m[key] {
		if e == clientID {
			return nil
		}
	}
	c.m[key] = append(c.m[key], clientID)
	return nil
}

func (c *MemoryClients) Take(_ context.Context, key string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := c.m[key]
	delete(c.m, key)
	return ids, nil
}

// MemoryGuests is MQTTGuests for a single API instance (and tests).
type MemoryGuests struct {
	mu sync.Mutex
	m  map[string]memGuest
}

type memGuest struct {
	token string
	until time.Time
}

func (g *MemoryGuests) Put(_ context.Context, clientID, token string, ttl time.Duration) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.m == nil {
		g.m = map[string]memGuest{}
	}
	g.m[clientID] = memGuest{token, time.Now().Add(ttl)}
	return nil
}

func (g *MemoryGuests) Get(_ context.Context, clientID string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.m[clientID]
	if !ok || time.Now().After(e.until) {
		delete(g.m, clientID)
		return "", domain.ErrNotFound
	}
	return e.token, nil
}
