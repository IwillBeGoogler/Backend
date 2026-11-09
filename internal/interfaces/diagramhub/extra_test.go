package diagramhub

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/topic"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/yjs"
)

var bg = context.Background()

// ystub is a store whose YLoad / YAppendMany can be scripted.
type ystub struct {
	*diagramtest.Memory
	mu     sync.Mutex
	loads  int
	load   func(n int) (diagram.YState, error)
	append func() (int64, error)
}

func (s *ystub) YLoad(ctx context.Context, f string) (diagram.YState, error) {
	s.mu.Lock()
	s.loads++
	n := s.loads
	s.mu.Unlock()
	if s.load != nil {
		return s.load(n)
	}
	return s.Memory.YLoad(ctx, f)
}

func (s *ystub) YAppendMany(ctx context.Context, f string, d [][]byte) (int64, error) {
	if s.append != nil {
		return s.append()
	}
	return s.Memory.YAppendMany(ctx, f, d)
}

// flakySlots wraps MemorySlots with switchable errors.
type flakySlots struct {
	*MemorySlots
	members, acquire bool
}

func (s *flakySlots) Members(ctx context.Context, f string) ([]string, error) {
	if s.members {
		return nil, errors.New("redis down")
	}
	return s.MemorySlots.Members(ctx, f)
}

func (s *flakySlots) Acquire(ctx context.Context, f, c string, max int) (bool, error) {
	if s.acquire {
		return false, errors.New("redis down")
	}
	return s.MemorySlots.Acquire(ctx, f, c, max)
}

type errBus struct{}

func (errBus) Publish(context.Context, application.Message) error { return errors.New("broker down") }

type panicAuth struct{}

func (panicAuth) FileAccess(context.Context, string, string) (application.ChannelAccess, error) {
	panic("boom")
}

func TestBusEdges(t *testing.T) {
	h := New(Options{})
	defer h.Close()
	h.publish(bg, application.Message{Topic: "x"}) // no broker: dropped
	h.Attach(errBus{})
	h.publish(bg, application.Message{Topic: "x"}) // broker error: logged

	full := &Hub{out: make(chan application.Message, 1)}
	full.emit(application.Message{Topic: "a"})
	full.emit(application.Message{Topic: "b"}) // queue full: dropped
	if len(full.out) != 1 {
		t.Fatal("queue")
	}
}

func TestHubHelpers(t *testing.T) {
	h := New(Options{Y: store(t), CollabEnabled: true})
	defer h.Close()
	if !h.CollabEnabled() {
		t.Error("collab")
	}
	if len(newID()) != 32 {
		t.Error("newID")
	}
	if userRef(0) != nil || *userRef(5) != "5" {
		t.Error("userRef")
	}
	if mustJSON(make(chan int)) != nil {
		t.Error("mustJSON")
	}
	if _, ok := h.access(bg, alice, "f"); ok { // no Authority
		t.Error("access without authority")
	}
}

func TestOnMessageEdges(t *testing.T) {
	var gone []string
	var mu sync.Mutex
	_, b := fixture(t, store(t, "f"), Options{OnFileGone: func(f string) { mu.Lock(); gone = append(gone, f); mu.Unlock() }})
	b.hub.OnMessage(application.Message{Topic: "not/a/topic"})
	b.hub.OnMessage(application.Message{Topic: topic.YjsDownTopic("f")}) // a kind the hub ignores
	b.hub.OnMessage(application.Message{Topic: topic.FileEventsTopic("f"), Payload: []byte(`{"type":"other"}`)})
	b.hub.OnMessage(application.Message{Topic: topic.FileEventsTopic("f"), Payload: []byte(`{"type":"ctl","code":1012}`)})
	mu.Lock()
	defer mu.Unlock()
	if len(gone) != 1 || gone[0] != "f" {
		t.Errorf("gone = %v", gone)
	}
}

func TestOnMessageAfterClose(t *testing.T) {
	h := New(Options{})
	h.Close()
	h.Close() // idempotent
	for range 400 {
		h.OnMessage(application.Message{Topic: topic.YjsUpTopic("f", alice)})
	}
}

func TestHandlePanicRecovered(t *testing.T) {
	h, _ := fixture(t, store(t, "f"), Options{Authority: panicAuth{}})
	h.handle(job{t: topic.Topic{Kind: topic.Presence, FileID: "f", ClientID: alice}, m: application.Message{Payload: []byte("x")}})
}

func TestSweep(t *testing.T) {
	mem := store(t, "f")
	h, _ := fixture(t, mem, Options{IdleAfter: time.Millisecond})
	if _, err := h.room(bg, "f"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	h.mu.Lock()
	h.seen["c"] = old
	h.roster["k"] = rosterMark{at: old}
	h.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	h.sweep()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.rooms) != 0 || len(h.seen) != 0 || len(h.roster) != 0 {
		t.Errorf("not swept: %d %d %d", len(h.rooms), len(h.seen), len(h.roster))
	}
}

func TestMemorySlots(t *testing.T) {
	s := NewMemorySlots()
	for range 2 {
		if ok, _ := s.Acquire(bg, "f", "a", 1); !ok {
			t.Fatal("acquire")
		}
	}
	if ok, _ := s.Acquire(bg, "f", "b", 1); ok {
		t.Fatal("over cap")
	}
	s.Release(bg, "f", "a")
	if m, _ := s.Members(bg, "f"); len(m) != 0 {
		t.Fatal(m)
	}
}

func TestRefreshSlot(t *testing.T) {
	fs := &flakySlots{MemorySlots: NewMemorySlots()}
	h, _ := fixture(t, store(t, "f"), Options{Slots: fs})
	h.refreshSlot(bg, "f", alice) // not a member: nothing
	fs.Acquire(bg, "f", alice, 2)
	h.refreshSlot(bg, "f", alice)
	fs.members = true
	h.refreshSlot(bg, "f", alice) // store error: ignored
}

func TestRTCEdges(t *testing.T) {
	fs := &flakySlots{MemorySlots: NewMemorySlots()}
	h, b := fixture(t, store(t, "f"), Options{Slots: fs})
	ft := func(c string) topic.Topic { return topic.Topic{FileID: "f", ClientID: c} }

	if p := h.who(bg, "f", "u9.zzzzzzzz"); p.Name != "Someone" || p.UserID == nil || *p.UserID != "9" {
		t.Errorf("who = %+v", p)
	}
	h.handleRTC(bg, ft(alice), []byte("{"))                           // bad json
	h.handleRTC(bg, ft("u9.zzzzzzzz"), []byte(`{"type":"rtc.join"}`)) // no access
	h.handleRTC(bg, ft(alice), []byte(`{"type":"rtc.join"}`))
	h.handleRTC(bg, ft(alice), []byte(`{"type":"rtc.join"}`))                             // already a member
	h.handleRTC(bg, ft(bob), []byte(`{"type":"rtc.state","muted":true,"speaking":true}`)) // bob is not in the call
	h.handleRTC(bg, ft(alice), []byte(`{"type":"rtc.leave"}`))
	h.handleRTC(bg, ft(alice), []byte(`{"type":"rtc.leave"}`)) // not a member

	fs.members = true
	if p := h.peers(bg, "f", ""); len(p) != 0 {
		t.Errorf("peers = %v", p)
	}
	fs.members = false
	fs.acquire = true
	h.joinRTC(bg, "f", carol)
	if len(b.jsonOn(t, topic.RTCInboxTopic("f", carol), "rtc.full")) != 1 {
		t.Error("no rtc.full on slot error")
	}
}

func TestYRoomLoadFailures(t *testing.T) {
	mem := store(t, "f")
	s := &ystub{Memory: mem, load: func(int) (diagram.YState, error) { return diagram.YState{}, errors.New("db down") }}
	h, b := fixture(t, nil, Options{Y: s})
	h.serveJoin(bg, "f", alice)
	h.write(bg, "f", alice, []byte{1, 2, 3}) // unparsable: returns before the room
	h.write(bg, "f", alice, updates(t, "map values")[0])
	h.handleAwareness(bg, topic.Topic{FileID: "f", ClientID: alice}, aw(1, 1, `{}`))
	if n := len(b.jsonOn(t, topic.CtlTopic(alice), "unavailable")); n != 2 {
		t.Errorf("unavailable messages = %d", n)
	}
	if _, err := h.Compact(bg, "f", 1, updates(t, "map values")[0]); err == nil {
		t.Error("compact with failing store")
	}
}

func TestYRoomClosedHub(t *testing.T) {
	h := New(Options{Y: store(t, "f"), CollabEnabled: true})
	h.Close()
	if _, err := h.room(bg, "f"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestYRoomStoredState(t *testing.T) {
	u := updates(t, "two clients")
	s := &ystub{Memory: store(t, "f")}
	s.load = func(int) (diagram.YState, error) {
		return diagram.YState{Snapshot: u[0], SnapshotSeq: 1, Updates: []diagram.YUpdate{{Seq: 2, Data: u[1]}, {Seq: 3, Data: []byte{1, 2, 3}}}}, nil
	}
	h, b := fixture(t, nil, Options{Y: s})
	h.serveJoin(bg, "f", alice) // snapshot + updates, one of which does not parse (logged)
	if n := len(b.on(topic.YjsSyncTopic("f", alice))); n != 5 {
		t.Errorf("sync messages = %d", n)
	}
}

func TestServeJoinSecondLoadFails(t *testing.T) {
	s := &ystub{Memory: store(t, "f")}
	s.load = func(n int) (diagram.YState, error) {
		if n >= 2 {
			return diagram.YState{}, errors.New("db down")
		}
		return diagram.YState{}, nil
	}
	h, b := fixture(t, nil, Options{Y: s})
	h.serveJoin(bg, "f", alice)
	if len(b.jsonOn(t, topic.CtlTopic(alice), "unavailable")) != 1 {
		t.Error("not told")
	}
}

func TestJoinCarriesAwareness(t *testing.T) {
	h, b := fixture(t, store(t, "f"), Options{})
	ft := topic.Topic{FileID: "f", ClientID: alice}
	h.handleAwareness(bg, ft, aw(77, 1, `{"cursor":1}`))
	h.serveJoin(bg, "f", bob)
	found := false
	for _, m := range b.on(topic.YjsSyncTopic("f", bob)) {
		if d, err := yjs.Decode(m.Payload); err == nil && d.Type == yjs.MsgAwareness {
			found = true
		}
	}
	if !found {
		t.Error("join without awareness")
	}
}

func TestHandleUpAndReqEdges(t *testing.T) {
	h, b := fixture(t, store(t, "f"), Options{MaxUpdateBytes: 64})
	ft := func(c string) topic.Topic { return topic.Topic{FileID: "f", ClientID: c} }
	big := make([]byte, 100)
	h.handleUp(bg, ft(alice), big)                          // too big
	h.handleUp(bg, ft(alice), []byte{99, 99})               // not yjs
	h.handleUp(bg, ft(alice), yjs.Step1([]byte{0}))         // sync step 1 on the up topic joins
	h.handleUp(bg, ft(alice), yjs.Compact(1, []byte{1, 2})) // snapshot does not parse: logged
	if len(b.on(topic.YjsSyncTopic("f", alice))) == 0 {
		t.Error("step 1 on up not answered")
	}
	h.handleReq(bg, ft(alice), big)                          // too big
	h.handleReq(bg, ft("u9.zzzzzzzz"), yjs.Step1([]byte{0})) // no access
	h.handleReq(bg, ft(alice), []byte{99, 99})               // not yjs
}

func TestFlushNotFoundDropsRoomSilently(t *testing.T) {
	s := &ystub{Memory: store(t, "f"), append: func() (int64, error) { return 0, domain.ErrNotFound }}
	h, b := fixture(t, nil, Options{Y: s})
	h.write(bg, "f", alice, updates(t, "map values")[0])
	eventually(t, "room dropped", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.rooms) == 0
	})
	if len(b.jsonOn(t, topic.CtlTopic(alice), "unavailable")) != 0 {
		t.Error("editor told about a deleted file")
	}
}

func TestCompactErrors(t *testing.T) {
	u := updates(t, "two clients")
	mk := func(st diagram.YState) *Hub {
		s := &ystub{Memory: store(t, "f"), load: func(int) (diagram.YState, error) { return st, nil }}
		h, _ := fixture(t, nil, Options{Y: s})
		return h
	}
	// the stored snapshot does not parse
	if _, err := mk(diagram.YState{Snapshot: []byte{1, 2, 3}, Updates: []diagram.YUpdate{{Seq: 1, Data: u[0]}}}).Compact(bg, "f", 1, u[0]); err == nil {
		t.Error("bad stored snapshot")
	}
	// a stored update does not parse
	if _, err := mk(diagram.YState{Updates: []diagram.YUpdate{{Seq: 1, Data: []byte{1, 2, 3}}}}).Compact(bg, "f", 1, u[0]); err == nil {
		t.Error("bad stored update")
	}
	// updates past seq are not part of the check
	if _, err := mk(diagram.YState{Updates: []diagram.YUpdate{{Seq: 1, Data: u[0]}, {Seq: 2, Data: u[1]}}}).Compact(bg, "f", 1, u[0]); err != nil {
		t.Errorf("break path: %v", err)
	}
	// a snapshot that does not parse
	if _, err := mk(diagram.YState{}).Compact(bg, "f", 1, []byte{1, 2, 3}); err == nil {
		t.Error("bad snapshot")
	}
}

func TestAwarenessEdges(t *testing.T) {
	guest := "g.gggggggg"
	auth := access{roles: map[string]diagram.Role{alice: diagram.RoleEditor, guest: diagram.RoleEditor}, users: map[string]int{alice: 1}}
	h, b := fixture(t, store(t, "f"), Options{Authority: auth})
	ft := func(c string) topic.Topic { return topic.Topic{FileID: "f", ClientID: c} }
	reset := func(c string) { h.mu.Lock(); delete(h.seen, c); h.mu.Unlock() }

	h.handleAwareness(bg, ft(alice), make([]byte, 70<<10)) // too big
	reset(alice)
	h.handleAwareness(bg, ft(alice), []byte{99, 99}) // not yjs
	reset(alice)
	h.handleAwareness(bg, ft(alice), yjs.Step1([]byte{0})) // not awareness
	reset(alice)
	h.handleAwareness(bg, ft("u9.zzzzzzzz"), aw(1, 1, `{}`))            // no access
	h.handleAwareness(bg, ft(alice), yjs.Awareness([]byte{0xff, 0xff})) // payload does not parse
	reset(alice)
	h.handleAwareness(bg, ft(alice), aw(5, 5, `{"a":1}`))
	reset(alice)
	h.handleAwareness(bg, ft(alice), aw(5, 3, `{"a":2}`)) // stale clock: skipped
	h.handleAwareness(bg, ft(guest), aw(6, 1, `{}`))      // guest: no user id
	if n := len(awarenessOut(t, b, "f")); n != 2 {
		t.Errorf("awareness out = %d", n)
	}
	if userID(0) != "" || !strings.HasPrefix(userID(3), "3") {
		t.Error("userID")
	}
	// a client dropped before it showed a cursor, and a cursor already gone
	h.dropAwareness(bg, "f", "u9.zzzzzzzz")
	h.dropAwareness(bg, "nofile", alice)
	h.handleAwareness(bg, ft(alice), aw(5, 9, `null`))
	h.dropAwareness(bg, "f", alice)
}

func TestMaintainSweepsOnHeartbeat(t *testing.T) {
	h := New(Options{Y: store(t, "f"), CollabEnabled: true, Heartbeat: 5 * time.Millisecond, IdleAfter: time.Millisecond})
	defer h.Close()
	h.mu.Lock()
	h.seen["c"] = time.Now().Add(-time.Hour)
	h.mu.Unlock()
	eventually(t, "heartbeat sweep", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.seen) == 0
	})
}
