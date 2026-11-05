package application

// The broker's authentication and access decisions.

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/topic"
)

// tickets is ResetTokens with tickets made by hand.
type tickets struct {
	mu sync.Mutex
	m  map[string]int
}

func (t *tickets) Issue(_ context.Context, user int, _ time.Duration) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]int{}
	}
	tok := "ticket-" + strconv.Itoa(len(t.m))
	t.m[tok] = user
	return tok, nil
}

func (t *tickets) Consume(_ context.Context, tok string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	u, ok := t.m[tok]
	delete(t.m, tok)
	if !ok {
		return 0, domain.ErrNotFound
	}
	return u, nil
}

type mqttEnv struct {
	commentEnv
	acc *MQTTAccess
	tk  *tickets
	now time.Time
	den []string
}

func newMQTT(t *testing.T) *mqttEnv {
	t.Helper()
	e := &mqttEnv{commentEnv: newComments(t), tk: &tickets{}, now: time.Now()}
	e.acc = &MQTTAccess{D: e.Diagrams, Tickets: e.tk, Guests: &MemoryGuests{}, ServerUser: "api", ServerPassword: "server-secret",
		CollabEnabled: true, TTL: 30 * time.Second, now: func() time.Time { return e.now },
		OnDenied: func(c, f string) { e.den = append(e.den, c+" "+f) }}
	return e
}

func uid(n int) string { return "u" + strconv.Itoa(n) + ".abcdefgh" }

func (e *mqttEnv) login(t *testing.T, user int) string {
	t.Helper()
	tok, _ := e.tk.Issue(dctx, user, time.Minute)
	if r := e.acc.Authenticate(dctx, uid(user), MQTTTicketUser, tok); !r.Allow || r.Superuser {
		t.Fatalf("login of %d: %+v", user, r)
	}
	return uid(user)
}

func TestMQTTAuthenticate(t *testing.T) {
	e := newMQTT(t)
	a := e.acc

	if r := a.Authenticate(dctx, "srv.1234", "api", "server-secret"); !r.Allow || !r.Superuser {
		t.Errorf("server login: %+v", r)
	}
	for _, bad := range [][2]string{{"api", "wrong"}, {"API", "server-secret"}, {"", ""}} {
		if r := a.Authenticate(dctx, "srv.1234", bad[0], bad[1]); r.Allow {
			t.Errorf("server login %v accepted", bad)
		}
	}

	tok, _ := e.tk.Issue(dctx, bob, time.Minute)
	if r := a.Authenticate(dctx, uid(bob), MQTTTicketUser, tok); !r.Allow || r.Superuser {
		t.Errorf("ticket login: %+v", r)
	}
	if r := a.Authenticate(dctx, uid(bob), MQTTTicketUser, tok); r.Allow {
		t.Error("a ticket was used twice")
	}
	tok, _ = e.tk.Issue(dctx, bob, time.Minute)
	if r := a.Authenticate(dctx, uid(alice), MQTTTicketUser, tok); r.Allow {
		t.Error("bob's ticket opened a connection named for alice")
	}
	tok, _ = e.tk.Issue(dctx, bob, time.Minute)
	for _, id := range []string{"g.abcdefgh", "bob", "u" + strconv.Itoa(bob), "", "u0.abcdefgh"} {
		if r := a.Authenticate(dctx, id, MQTTTicketUser, tok); r.Allow {
			t.Errorf("client id %q accepted", id)
		}
	}
	if r := a.Authenticate(dctx, uid(bob), MQTTTicketUser, ""); r.Allow {
		t.Error("empty ticket accepted")
	}
	if r := a.Authenticate(dctx, uid(bob), "nobody", "x"); r.Allow {
		t.Error("unknown username accepted")
	}

	_, share, err := e.CreateShare(dctx, alice, e.pid, NewShare{Role: diagram.RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	if r := a.Authenticate(dctx, "g.abcdefgh", MQTTShareUser, share); !r.Allow || r.Superuser {
		t.Errorf("share login: %+v", r)
	}
	if r := a.Authenticate(dctx, "g.abcdefgh", MQTTShareUser, "not-a-token"); r.Allow {
		t.Error("bad share token accepted")
	}
	if r := a.Authenticate(dctx, uid(bob), MQTTShareUser, share); r.Allow {
		t.Error("a user client id with a share login accepted")
	}
}

func TestMQTTAuthorize(t *testing.T) {
	e := newMQTT(t)
	a := e.acc
	bobC, vicC, alC := e.login(t, bob), e.login(t, vic), e.login(t, alice)
	f, p := e.fid, e.pid
	can := func(c, user, action, tp string) bool { return a.Authorize(dctx, c, user, action, tp) }
	const T = MQTTTicketUser

	// editors read everything of the project and write Yjs as themselves
	for _, tp := range []string{"dg/p/" + p + "/events", "dg/p/" + p + "/roster/+/+", "dg/f/" + f + "/events", "dg/f/" + f + "/roster/+",
		"dg/f/" + f + "/yjs/down", "dg/f/" + f + "/awareness/out", "dg/f/" + f + "/yjs/sync/" + bobC, "dg/f/" + f + "/rtc/" + bobC, "dg/c/" + bobC + "/ctl"} {
		if !can(bobC, T, "subscribe", tp) {
			t.Errorf("bob cannot subscribe to %s", tp)
		}
	}
	for _, tp := range []string{"dg/f/" + f + "/yjs/up/" + bobC, "dg/f/" + f + "/yjs/req/" + bobC, "dg/f/" + f + "/awareness/up/" + bobC,
		"dg/f/" + f + "/presence/" + bobC, "dg/f/" + f + "/rtc/up/" + bobC} {
		if !can(bobC, T, "publish", tp) {
			t.Errorf("bob cannot publish to %s", tp)
		}
	}

	// what is the server's alone, or somebody else's, is closed
	for _, tp := range []string{"dg/f/" + f + "/yjs/down", "dg/f/" + f + "/events", "dg/p/" + p + "/events", "dg/f/" + f + "/awareness/out",
		"dg/f/" + f + "/roster/" + bobC, "dg/f/" + f + "/yjs/sync/" + bobC, "dg/f/" + f + "/yjs/up/" + alC, "dg/f/" + f + "/presence/" + alC,
		"dg/f/" + f + "/awareness/up/" + alC, "dg/f/" + f + "/rtc/up/" + alC, "dg/c/" + bobC + "/ctl", "dg/f/" + f + "/yjs/up/+", "dg/#", "#", "$SYS/#",
		"$share/dg/dg/f/+/yjs/up/+", "dg/f/+/events"} {
		if can(bobC, T, "publish", tp) {
			t.Errorf("bob may publish to %s", tp)
		}
	}
	for _, tp := range []string{"dg/f/" + f + "/yjs/sync/" + alC, "dg/c/" + alC + "/ctl", "dg/f/" + f + "/rtc/" + alC, "dg/f/" + f + "/yjs/up/" + bobC,
		"dg/f/" + f + "/presence/" + bobC, "dg/#", "#", "$SYS/#", "$share/dg/dg/f/+/yjs/up/+", "dg/f/+/events", "dg/f/+/roster/+", "dg/+/" + f + "/events",
		"dg/f/other/events", "dg/p/other/events", "dg/p/other/roster/+/+"} {
		if can(bobC, T, "subscribe", tp) {
			t.Errorf("bob may subscribe to %s", tp)
		}
	}
	if can(bobC, T, "delete", "dg/f/"+f+"/events") || can(bobC, "share", "subscribe", "dg/f/"+f+"/events") || can(bobC, "nobody", "subscribe", "dg/f/"+f+"/events") {
		t.Error("odd action or username accepted")
	}

	// a viewer reads and shows a cursor, never writes: refused, and told why
	if !can(vicC, T, "subscribe", "dg/f/"+f+"/yjs/down") || !can(vicC, T, "publish", "dg/f/"+f+"/yjs/req/"+vicC) ||
		!can(vicC, T, "publish", "dg/f/"+f+"/awareness/up/"+vicC) || !can(vicC, T, "publish", "dg/f/"+f+"/rtc/up/"+vicC) {
		t.Error("a viewer cannot read")
	}
	if can(vicC, T, "publish", "dg/f/"+f+"/yjs/up/"+vicC) {
		t.Error("a viewer may write Yjs updates")
	}
	if len(e.den) != 1 || e.den[0] != vicC+" "+f {
		t.Errorf("denied notices = %v", e.den)
	}

	// a stranger holds nothing
	e.users.Add(99, "eve", "eve@example.com", "")
	eveC := e.login(t, 99)
	if can(eveC, T, "subscribe", "dg/f/"+f+"/events") || can(eveC, T, "publish", "dg/f/"+f+"/presence/"+eveC) || can(eveC, T, "subscribe", "dg/p/"+p+"/events") {
		t.Error("a stranger got in")
	}
	if !can(eveC, T, "subscribe", "dg/c/"+eveC+"/ctl") {
		t.Error("everybody hears their own control topic")
	}

	// an unknown file is not found: no access (a deleted file locks its readers out)
	if can(bobC, T, "subscribe", "dg/f/nope/events") {
		t.Error("a missing file is open")
	}

	// collaboration off closes the Yjs topics
	a.CollabEnabled = false
	if can(bobC, T, "publish", "dg/f/"+f+"/yjs/up/"+bobC) || can(bobC, T, "publish", "dg/f/"+f+"/awareness/up/"+bobC) || can(bobC, T, "publish", "dg/f/"+f+"/yjs/req/"+bobC) {
		t.Error("Yjs open with collaboration off")
	}
	a.CollabEnabled = true
}

func TestMQTTShareGuests(t *testing.T) {
	e := newMQTT(t)
	a := e.acc
	f, p := e.fid, e.pid
	_, viewTok, _ := e.CreateShare(dctx, alice, p, NewShare{Role: diagram.RoleViewer, FileID: &f})
	link, editTok, _ := e.CreateShare(dctx, alice, p, NewShare{Role: diagram.RoleEditor})
	g1, g2 := "g.viewerviewer", "g.editoreditor"
	if !a.Authenticate(dctx, g1, MQTTShareUser, viewTok).Allow || !a.Authenticate(dctx, g2, MQTTShareUser, editTok).Allow {
		t.Fatal("share logins refused")
	}
	can := func(c, action, tp string) bool { return a.Authorize(dctx, c, MQTTShareUser, action, tp) }

	// a file link opens its own file, as a viewer; a project link the project
	if !can(g1, "subscribe", "dg/f/"+f+"/events") || !can(g1, "publish", "dg/f/"+f+"/awareness/up/"+g1) {
		t.Error("file link cannot read its file")
	}
	if can(g1, "publish", "dg/f/"+f+"/yjs/up/"+g1) {
		t.Error("viewer link writes")
	}
	if can(g1, "subscribe", "dg/p/"+p+"/events") {
		t.Error("a file link opened the project channel")
	}
	if !can(g2, "subscribe", "dg/p/"+p+"/events") || !can(g2, "publish", "dg/f/"+f+"/yjs/up/"+g2) {
		t.Error("editor project link cannot work")
	}
	if can(g2, "publish", "dg/f/"+f+"/yjs/up/"+g1) {
		t.Error("guest wrote as another guest")
	}
	if can("g.unknownunknown", "subscribe", "dg/f/"+f+"/events") {
		t.Error("a guest that never logged in got in")
	}
	// a user username on a guest id, and the reverse
	if a.Authorize(dctx, g2, MQTTTicketUser, "subscribe", "dg/f/"+f+"/events") {
		t.Error("guest id with the ticket username")
	}

	// revoking the link locks the guest out once the remembered answer lapses
	if err := e.RevokeShare(dctx, alice, p, link.ID); err != nil {
		t.Fatal(err)
	}
	if !can(g2, "subscribe", "dg/p/"+p+"/events") {
		t.Error("the answer should still be remembered")
	}
	e.now = e.now.Add(time.Minute)
	if can(g2, "subscribe", "dg/p/"+p+"/events") || can(g2, "publish", "dg/f/"+f+"/yjs/up/"+g2) {
		t.Error("a revoked link still opens the project")
	}
}

func TestMQTTFileAccessAndForget(t *testing.T) {
	e := newMQTT(t)
	bobC := e.login(t, bob)
	ca, err := e.acc.FileAccess(dctx, bobC, e.fid)
	if err != nil || ca.UserID != bob || ca.Name != "bob" || ca.ProjectID != e.pid || !ca.Role.Allows(diagram.RoleEditor) || ca.Role.Allows(diagram.RoleOwner) {
		t.Errorf("access = %+v, %v", ca, err)
	}
	if _, err := e.acc.FileAccess(dctx, "bob", e.fid); err == nil {
		t.Error("a malformed client id has access")
	}
	if _, err := e.acc.FileAccess(dctx, bobC, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing file: %v", err)
	}
	if err := e.RemoveMember(dctx, alice, e.pid, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := e.acc.FileAccess(dctx, bobC, e.fid); err != nil {
		t.Error("removal should not show before the answer lapses")
	}
	e.acc.Forget(e.fid)
	if _, err := e.acc.FileAccess(dctx, bobC, e.fid); err == nil {
		t.Error("a removed member keeps access after Forget")
	}
}

type cutBus struct {
	mu   sync.Mutex
	sent []Message
}

func (b *cutBus) Publish(_ context.Context, m Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, m)
	return nil
}

type cutKicker struct{ kicked []string }

func (k *cutKicker) Enabled() bool { return true }
func (k *cutKicker) Kick(_ context.Context, id string) error {
	k.kicked = append(k.kicked, id)
	return nil
}

func TestMQTTCutoff(t *testing.T) {
	e := newMQTT(t)
	bus, kick := &cutBus{}, &cutKicker{}
	e.acc.Clients, e.acc.Bus, e.acc.Kicker = &MemoryClients{}, bus, kick
	e.Diagrams.Cutoff = e.acc
	a, p := e.acc, e.pid
	link, tok, _ := e.CreateShare(dctx, alice, p, NewShare{Role: diagram.RoleEditor})
	g := "g.guestguest"
	if !a.Authenticate(dctx, g, MQTTShareUser, tok).Allow {
		t.Fatal("guest refused")
	}
	bobC := e.login(t, bob)
	if !a.Authorize(dctx, bobC, MQTTTicketUser, "subscribe", "dg/p/"+p+"/events") || !a.Authorize(dctx, g, MQTTShareUser, "subscribe", "dg/p/"+p+"/events") {
		t.Fatal("no access to begin with")
	}
	sentTo := func() []string {
		var out []string
		for _, m := range bus.sent {
			if m.Topic == topic.ACLDrop {
				continue
			}
			if string(m.Payload) != `{"code":"denied","type":"ctl"}` {
				t.Errorf("payload = %s", m.Payload)
			}
			out = append(out, m.Topic)
		}
		return out
	}

	// role lowered: editor -> viewer cuts bob; raising does not
	ok(t, e.SetMemberRole(dctx, alice, p, bob, diagram.RoleEditor))
	if len(kick.kicked) != 0 {
		t.Fatal("same role cut")
	}
	ok(t, e.SetMemberRole(dctx, alice, p, bob, diagram.RoleViewer))
	if len(kick.kicked) != 1 || kick.kicked[0] != bobC || len(sentTo()) != 1 || sentTo()[0] != "dg/c/"+bobC+"/ctl" {
		t.Fatalf("kicked %v sent %v", kick.kicked, sentTo())
	}
	// the cache entry is gone at once, no waiting for the TTL
	if a.Authorize(dctx, bobC, MQTTTicketUser, "publish", "dg/f/"+e.fid+"/yjs/up/"+bobC) {
		t.Error("lowered member still writes")
	}

	// revoked link cuts the guest and drops its cached answer
	ok(t, e.RevokeShare(dctx, alice, p, link.ID))
	if len(kick.kicked) != 2 || kick.kicked[1] != g {
		t.Fatalf("kicked %v", kick.kicked)
	}
	if a.Authorize(dctx, g, MQTTShareUser, "subscribe", "dg/p/"+p+"/events") {
		t.Error("revoked guest still subscribes")
	}

	// removed member
	bob2 := e.login(t, bob)
	a.Authorize(dctx, bob2, MQTTTicketUser, "subscribe", "dg/p/"+p+"/events")
	ok(t, e.RemoveMember(dctx, alice, p, bob))
	if len(kick.kicked) != 3 {
		t.Fatalf("kicked %v", kick.kicked)
	}
}

// lostRecorder is a RealtimeCutoff that records MemberLost calls.
type lostRecorder struct{ lost []string }

func (r *lostRecorder) ShareRevoked(context.Context, string, string) {}
func (r *lostRecorder) MemberLost(_ context.Context, projectID string, userID int) {
	r.lost = append(r.lost, projectID+" "+strconv.Itoa(userID))
}

// slowGuests answers Get after cut() ran, like a lookup that began before it.
type racyGuests struct {
	MemoryGuests
	before func()
}

func (g *racyGuests) Get(ctx context.Context, id string) (string, error) {
	tok, err := g.MemoryGuests.Get(ctx, id)
	if g.before != nil {
		g.before()
	}
	return tok, err
}

func TestMQTTCutDuringLookupNotCached(t *testing.T) {
	e := newMQTT(t)
	g := &racyGuests{}
	e.acc.Guests = g
	_, tok, _ := e.CreateShare(dctx, alice, e.pid, NewShare{Role: diagram.RoleEditor})
	c := "g.guestguest"
	if !e.acc.Authenticate(dctx, c, MQTTShareUser, tok).Allow {
		t.Fatal("guest refused")
	}
	g.before = func() { e.acc.DropClients([]string{c}); g.before = nil } // a cut lands mid-lookup
	if !e.acc.Authorize(dctx, c, MQTTShareUser, "subscribe", "dg/p/"+e.pid+"/events") {
		t.Fatal("no access")
	}
	e.acc.mu.Lock()
	n := len(e.acc.cache)
	e.acc.mu.Unlock()
	if n != 0 {
		t.Errorf("stale answer cached: %d entries", n)
	}
	e.acc.Authorize(dctx, c, MQTTShareUser, "subscribe", "dg/p/"+e.pid+"/events")
	e.acc.mu.Lock()
	n = len(e.acc.cache)
	e.acc.mu.Unlock()
	if n != 1 {
		t.Errorf("fresh answer not cached: %d", n)
	}
}

func TestMQTTCutAnnouncedToOtherInstances(t *testing.T) {
	e := newMQTT(t)
	bus := &cutBus{}
	e.acc.Clients, e.acc.Bus = &MemoryClients{}, bus
	e.Diagrams.Cutoff = e.acc
	bobC := e.login(t, bob)
	other := &MQTTAccess{D: e.Diagrams, Guests: &MemoryGuests{}}
	if !e.acc.Authorize(dctx, bobC, MQTTTicketUser, "subscribe", "dg/p/"+e.pid+"/events") {
		t.Fatal("no access")
	}
	other.cache = map[aclKey]aclEntry{{bobC, "project", e.pid}: {}}
	ok(t, e.SetMemberRole(dctx, alice, e.pid, bob, diagram.RoleViewer))
	var got *Message
	for i := range bus.sent {
		if bus.sent[i].Topic == topic.ACLDrop {
			got = &bus.sent[i]
		}
	}
	if got == nil {
		t.Fatal("no drop message")
	}
	other.OnACLDrop(got.Payload)
	if len(other.cache) != 0 {
		t.Error("other instance kept the entry")
	}
	// no browser client may publish or subscribe to it
	for _, act := range []string{"publish", "subscribe"} {
		if e.acc.Authorize(dctx, bobC, MQTTTicketUser, act, topic.ACLDrop) {
			t.Errorf("client may %s %s", act, topic.ACLDrop)
		}
	}
	if e.acc.Authorize(dctx, bobC, MQTTTicketUser, "subscribe", "dg/s/#") || e.acc.Authorize(dctx, bobC, MQTTTicketUser, "subscribe", "#") {
		t.Error("wildcard reaches the server topic")
	}
}

func TestMemberLostOnFileRoleChange(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	rec := &lostRecorder{}
	d.Cutoff = rec
	res := project(t, d, alice, "P")
	pid, fid := res.Project.ID, res.File.ID
	must1[diagram.Member](t)(d.AddMember(dctx, alice, pid, "bob", diagram.RoleEditor))
	want := pid + " " + strconv.Itoa(bob)

	// project editor -> file viewer cuts
	ok(t, d.SetFileMember(dctx, alice, fid, bob, diagram.RoleViewer))
	if len(rec.lost) != 1 || rec.lost[0] != want {
		t.Fatalf("lowering: %v", rec.lost)
	}
	// viewer override -> editor override raises: no cut
	ok(t, d.SetFileMember(dctx, alice, fid, bob, diagram.RoleEditor))
	if len(rec.lost) != 1 {
		t.Fatalf("raising: %v", rec.lost)
	}
	// editor override -> viewer override cuts
	ok(t, d.SetFileMember(dctx, alice, fid, bob, diagram.RoleViewer))
	if len(rec.lost) != 2 {
		t.Fatalf("override lowering: %v", rec.lost)
	}
	// removing a viewer override under a project editor raises: no cut
	ok(t, d.RemoveFileMember(dctx, alice, fid, bob))
	if len(rec.lost) != 2 {
		t.Fatalf("remove raising: %v", rec.lost)
	}
	// removing an editor override under a project viewer lowers: cut
	ok(t, d.SetMemberRole(dctx, alice, pid, bob, diagram.RoleViewer))
	n := len(rec.lost)
	ok(t, d.SetFileMember(dctx, alice, fid, bob, diagram.RoleEditor))
	if len(rec.lost) != n {
		t.Fatalf("override raise: %v", rec.lost)
	}
	ok(t, d.RemoveFileMember(dctx, alice, fid, bob))
	if len(rec.lost) != n+1 {
		t.Fatalf("remove lowering: %v", rec.lost)
	}
}

// A client id is public (rosters carry it): another share link must not take
// it over and inherit the role remembered for it.
func TestMQTTGuestClientIDNotTakenOver(t *testing.T) {
	e := newMQTT(t)
	a := e.acc
	f, p := e.fid, e.pid
	_, viewTok, _ := e.CreateShare(dctx, alice, p, NewShare{Role: diagram.RoleViewer})
	_, editTok, _ := e.CreateShare(dctx, alice, p, NewShare{Role: diagram.RoleEditor})
	g := "g.editoreditor"
	up := "dg/f/" + f + "/yjs/up/" + g
	if !a.Authenticate(dctx, g, MQTTShareUser, editTok).Allow || !a.Authorize(dctx, g, MQTTShareUser, "publish", up) {
		t.Fatal("editor guest refused")
	}
	if a.Authenticate(dctx, g, MQTTShareUser, viewTok).Allow && a.Authorize(dctx, g, MQTTShareUser, "publish", up) {
		t.Error("viewer link took over an editor guest's client id and writes")
	}
	if !a.Authenticate(dctx, g, MQTTShareUser, editTok).Allow {
		t.Error("the same link cannot reconnect under its client id")
	}
}

func TestMQTTAccountBlockedCutsEverySession(t *testing.T) {
	e := newMQTT(t)
	kick := &cutKicker{}
	e.acc.Clients, e.acc.Bus, e.acc.Kicker = &MemoryClients{}, &cutBus{}, kick
	c := e.login(t, bob) // connected, in no project yet
	e.acc.AccountBlocked(dctx, bob)
	if len(kick.kicked) != 1 || kick.kicked[0] != c {
		t.Fatalf("kicked %v", kick.kicked)
	}
}

func TestCutExpiredShares(t *testing.T) {
	e := newMQTT(t)
	kick := &cutKicker{}
	e.acc.Clients, e.acc.Bus, e.acc.Kicker = &MemoryClients{}, &cutBus{}, kick
	e.Diagrams.Cutoff = e.acc
	secs := 3600
	_, soon, _ := e.CreateShare(dctx, alice, e.pid, NewShare{Role: diagram.RoleViewer, ExpiresInSeconds: &secs})
	_, never, _ := e.CreateShare(dctx, alice, e.pid, NewShare{Role: diagram.RoleViewer})
	g1, g2 := "g.expiringlink", "g.foreverlink"
	if !e.acc.Authenticate(dctx, g1, MQTTShareUser, soon).Allow || !e.acc.Authenticate(dctx, g2, MQTTShareUser, never).Allow {
		t.Fatal("guests refused")
	}
	ok(t, e.CutExpiredShares(dctx))
	if len(kick.kicked) != 0 {
		t.Fatalf("live link cut: %v", kick.kicked)
	}
	later := e.Diagrams.clock().Add(2 * time.Hour)
	e.Diagrams.now = func() time.Time { return later }
	ok(t, e.CutExpiredShares(dctx))
	ok(t, e.CutExpiredShares(dctx))
	if len(kick.kicked) != 1 || kick.kicked[0] != g1 {
		t.Fatalf("kicked %v", kick.kicked)
	}
}
