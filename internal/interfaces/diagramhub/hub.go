// Package diagramhub is the realtime hub of the diagram workspace: who has
// which file open (presence), tree / file / comment events, audio-room
// signaling and the Yjs relay. It speaks MQTT through the small application.Bus
// interface, so it is testable without a broker.
//
// The browsers and the API meet on an EMQX broker (docs/MQTT.md has the
// topics and payloads). The hub is the API's side: it publishes the server-
// only channels (events, roster, Yjs updates, cursors, signaling) and works
// through what browsers publish, which the API instances split between them
// with shared subscriptions. An instance keeps nothing a browser depends on
// except short-lived caches; the broker's retained roster, Redis audio room
// slots and the database are the shared state.
package diagramhub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"hash/fnv"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/topic"
)

// Control codes carried by {"type":"ctl","code":...} messages (the codes the
// WebSocket close frames used): the client treats 4404 and 1011 as final and
// 1012 as "sync again".
const (
	CtlRestart = 1012 // the document was replaced: sync again
	CtlUnavail = 1011
	CtlDeleted = 4404
)

// Authority tells the hub what a client holds in a file (application.MQTTAccess).
type Authority interface {
	FileAccess(ctx context.Context, clientID, fileID string) (application.ChannelAccess, error)
}

// Options configures a Hub.
type Options struct {
	// Authority resolves a client's role, name and project in a file.
	Authority Authority
	// Slots counts audio room participants across instances (nil: in memory).
	Slots Slots
	// Y stores Yjs updates; nil turns live co-editing off.
	Y diagram.YRepository
	// CollabEnabled serves the Yjs relay (needs Y).
	CollabEnabled bool
	// CompactAfter is how many stored updates make the server ask a client
	// for a compacted snapshot.
	CompactAfter int
	// MaxUpdateBytes caps one Yjs message.
	MaxUpdateBytes int
	// MaxPeers is the audio room size.
	MaxPeers int
	// ICEServers is handed to browsers (GET /rtc/config).
	ICEServers any
	// Heartbeat is how often idle rooms and caches are swept.
	Heartbeat time.Duration
	// FlushEvery is how often accumulated Yjs updates are written to the
	// database in one batch (default 150 ms).
	FlushEvery time.Duration
	// IdleAfter is how long a Yjs room nobody writes to is kept in memory.
	IdleAfter time.Duration
	// OnFileGone is told when a file was deleted or its document replaced.
	OnFileGone func(fileID string)
}

// Hub routes realtime traffic.
type Hub struct {
	o Options

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	jobs   []chan job
	out    chan application.Message

	mu      sync.Mutex
	bus     application.Bus
	rooms   map[string]*yroom
	roster  map[string]rosterMark // file|client -> last published
	seen    map[string]time.Time  // client -> last awareness (throttle)
	closed  bool
	started bool
}

type job struct {
	t topic.Topic
	m application.Message
}

const workers = 16

// New starts a hub; Close stops it.
func New(o Options) *Hub {
	if o.Heartbeat <= 0 {
		o.Heartbeat = 30 * time.Second
	}
	if o.MaxPeers <= 0 {
		o.MaxPeers = 6
	}
	if o.CompactAfter <= 0 {
		o.CompactAfter = 50
	}
	if o.MaxUpdateBytes <= 0 {
		o.MaxUpdateBytes = 10 << 20
	}
	if o.FlushEvery <= 0 {
		o.FlushEvery = 150 * time.Millisecond
	}
	if o.IdleAfter <= 0 {
		o.IdleAfter = 5 * time.Minute
	}
	if o.Slots == nil {
		o.Slots = NewMemorySlots()
	}
	if o.Y == nil {
		o.CollabEnabled = false
	}
	h := &Hub{o: o, out: make(chan application.Message, 1024), rooms: map[string]*yroom{},
		roster: map[string]rosterMark{}, seen: map[string]time.Time{}}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	for range workers {
		ch := make(chan job, 256)
		h.jobs = append(h.jobs, ch)
		h.wg.Add(1)
		go h.work(ch)
	}
	h.wg.Add(2)
	go h.publisher()
	go h.maintain()
	return h
}

// CollabEnabled reports whether the Yjs relay is on.
func (h *Hub) CollabEnabled() bool { return h.o.CollabEnabled }

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func userRef(id int) *string {
	if id == 0 {
		return nil
	}
	s := strconv.Itoa(id)
	return &s
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		zap.L().Error("diagramhub: marshal", zap.Error(err))
	}
	return b
}

// OnMessage takes a message the broker delivered. It queues the work on the
// worker that owns the message's file (so one file's messages are handled in
// order) and blocks only when that worker is far behind.
func (h *Hub) OnMessage(m application.Message) {
	t, ok := topic.Parse(m.Topic)
	if !ok {
		return
	}
	switch t.Kind {
	case topic.FileEvents:
		h.fileEvent(t.FileID, m)
		return
	case topic.YjsUp, topic.YjsReq, topic.AwarenessUp, topic.Presence, topic.RTCUp:
	default:
		return
	}
	f := fnv.New32a()
	_, _ = f.Write([]byte(t.FileID))
	select {
	case h.jobs[f.Sum32()%workers] <- job{t, m}:
	case <-h.ctx.Done():
	}
}

func (h *Hub) work(ch chan job) {
	defer h.wg.Done()
	for {
		select {
		case <-h.ctx.Done():
			return
		case j := <-ch:
			h.handle(j)
		}
	}
}

func (h *Hub) handle(j job) {
	defer func() {
		if r := recover(); r != nil {
			zap.L().Error("diagramhub: handler panic", zap.String("topic", j.m.Topic), zap.Any("panic", r))
		}
	}()
	ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
	defer cancel()
	switch j.t.Kind {
	case topic.YjsUp:
		h.handleUp(ctx, j.t, j.m.Payload)
	case topic.YjsReq:
		h.handleReq(ctx, j.t, j.m.Payload)
	case topic.AwarenessUp:
		h.handleAwareness(ctx, j.t, j.m.Payload)
	case topic.Presence:
		h.handlePresence(ctx, j.t, j.m.Payload)
	case topic.RTCUp:
		h.handleRTC(ctx, j.t, j.m.Payload)
	}
}

// access is what a client holds in a file; ok is false (and the client may
// be told) when it holds nothing.
func (h *Hub) access(ctx context.Context, clientID, fileID string) (application.ChannelAccess, bool) {
	if h.o.Authority == nil {
		return application.ChannelAccess{}, false
	}
	a, err := h.o.Authority.FileAccess(ctx, clientID, fileID)
	if err != nil {
		return a, false
	}
	return a, true
}

// Events

// BroadcastFile sends msg to the channel of a file and to the project's tree
// channel. origin is the client that made the change: it is carried as the
// message's "origin" user property and clients drop their own.
func (h *Hub) BroadcastFile(projectID, fileID string, msg any, origin string) {
	b := mustJSON(msg)
	h.emit(application.Message{Topic: topic.FileEventsTopic(fileID), Payload: b, QoS: 1, Expiry: eventExpiry, Origin: origin})
	h.emit(application.Message{Topic: topic.ProjectEventsTopic(projectID), Payload: b, QoS: 1, Expiry: eventExpiry, Origin: origin})
}

// BroadcastTree sends msg to the project's tree channel only.
func (h *Hub) BroadcastTree(projectID string, msg any, origin string) {
	h.emit(application.Message{Topic: topic.ProjectEventsTopic(projectID), Payload: mustJSON(msg), QoS: 1, Expiry: eventExpiry, Origin: origin})
}

func ctlMsg(code int, reason string) map[string]any {
	return map[string]any{"type": "ctl", "code": code, "reason": reason}
}

// CloseFile tells the clients of a deleted file to stop: code 4404.
func (h *Hub) CloseFile(projectID, fileID string) {
	h.fileGone(fileID)
	h.emit(application.Message{Topic: topic.FileEventsTopic(fileID), Payload: mustJSON(ctlMsg(CtlDeleted, "deleted")), QoS: 1, Expiry: eventExpiry})
}

// CloseProject tells the clients of a deleted project and of its files to
// stop.
func (h *Hub) CloseProject(projectID string, fileIDs []string) {
	for _, fid := range fileIDs {
		h.CloseFile(projectID, fid)
	}
	h.emit(application.Message{Topic: topic.ProjectEventsTopic(projectID), Payload: mustJSON(ctlMsg(CtlDeleted, "deleted")), QoS: 1, Expiry: eventExpiry})
}

// ResetFile ends the Yjs sessions of a file whose document was replaced (a
// restored version): code 1012, clients sync from scratch.
func (h *Hub) ResetFile(projectID, fileID string) {
	h.fileGone(fileID)
	h.emit(application.Message{Topic: topic.FileEventsTopic(fileID), Payload: mustJSON(ctlMsg(CtlRestart, "restored")), QoS: 1, Expiry: eventExpiry})
}

// Denied tells a client that tried to write that its role does not allow it.
func (h *Hub) Denied(clientID, fileID string) {
	h.emit(application.Message{Topic: topic.CtlTopic(clientID), QoS: 1, Expiry: eventExpiry,
		Payload: mustJSON(map[string]any{"type": "denied", "fileId": fileID, "reason": "read-only"})})
}

// fileEvent is a file event any instance published: a ctl message means the
// file was deleted or replaced, whoever raised it.
func (h *Hub) fileEvent(fileID string, m application.Message) {
	var e struct {
		Type string `json:"type"`
		Code int    `json:"code"`
	}
	if json.Unmarshal(m.Payload, &e) == nil && e.Type == "ctl" && (e.Code == CtlRestart || e.Code == CtlDeleted) {
		h.fileGone(fileID)
	}
}

// fileGone forgets what this instance holds about a file.
func (h *Hub) fileGone(fileID string) {
	h.mu.Lock()
	delete(h.rooms, fileID) // the next message gets a fresh room
	yjsRooms.Set(float64(len(h.rooms)))
	h.mu.Unlock()
	if h.o.OnFileGone != nil {
		h.o.OnFileGone(fileID)
	}
}

// Close stops the hub; pending Yjs writes are flushed first.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	rooms := make([]*yroom, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()
	h.cancel()
	h.wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, r := range rooms {
		h.flush(ctx, r)
	}
}

// maintain flushes Yjs writes, and sweeps idle rooms and caches.
func (h *Hub) maintain() {
	defer h.wg.Done()
	flush := time.NewTicker(h.o.FlushEvery)
	sweep := time.NewTicker(h.o.Heartbeat)
	defer flush.Stop()
	defer sweep.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-flush.C:
			h.flushAll()
		case <-sweep.C:
			h.sweep()
		}
	}
}

func (h *Hub) sweep() {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, r := range h.rooms {
		if r.idle(now, h.o.IdleAfter) {
			delete(h.rooms, id)
		}
	}
	yjsRooms.Set(float64(len(h.rooms)))
	for k, t := range h.seen {
		if now.Sub(t) > time.Minute {
			delete(h.seen, k)
		}
	}
	for k, m := range h.roster {
		if now.Sub(m.at) > 3*rosterExpiry {
			delete(h.roster, k)
		}
	}
}
