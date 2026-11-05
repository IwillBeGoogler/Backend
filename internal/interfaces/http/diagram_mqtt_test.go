package http

// The broker's HTTP questions, the Yjs compaction route and the audio room
// config, against the in-memory repository. The realtime itself is tested in
// diagramhub (a fake broker) and against EMQX (diagramhub's integration test).

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/interfaces/diagramhub"
)

// tickets is single-use stream tickets in memory.
type tickets struct {
	mu sync.Mutex
	m  map[string]int
}

func (k *tickets) Issue(_ context.Context, user int, _ time.Duration) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]int{}
	}
	t := "tk" + strconv.Itoa(len(k.m)+1) + strings.Repeat("x", 20)
	k.m[t] = user
	return t, nil
}

func (k *tickets) Consume(_ context.Context, t string) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	u, ok := k.m[t]
	if !ok {
		return 0, fmt.Errorf("no such ticket")
	}
	delete(k.m, t)
	return u, nil
}

const testSecret = "internal-secret-for-tests-0123456789"

type rtFixture struct {
	commentFixture
	hub *diagramhub.Hub
	tk  *tickets
}

func newRTFixture(t *testing.T, o diagramhub.Options, secret string) rtFixture {
	t.Helper()
	var hub *diagramhub.Hub
	tk := &tickets{}
	f := newCommentFixture(t, func(d *application.Diagrams, deps *Deps) {
		o.Y = d.Y
		hub = diagramhub.New(o)
		t.Cleanup(hub.Close)
		d.Events = NewDiagramEvents(hub)
		deps.Realtime = hub
		deps.MQTT = &application.MQTTAccess{D: d, Tickets: tk, Guests: &application.MemoryGuests{}, ServerUser: "api", ServerPassword: "server-pass", CollabEnabled: o.CollabEnabled}
		deps.MQTTInternalSecret = secret
	})
	return rtFixture{f, hub, tk}
}

// ask posts to an internal route as the broker would.
func (f rtFixture) ask(t *testing.T, path, secret string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func counterVal(c interface{ Write(*dto.Metric) error }) float64 {
	var m dto.Metric
	_ = c.Write(&m)
	return m.GetCounter().GetValue()
}

func TestMQTTInternalRoutes(t *testing.T) {
	f := newRTFixture(t, diagramhub.Options{CollabEnabled: true}, testSecret)
	const auth, acl = "/api/internal/mqtt/auth", "/api/internal/mqtt/acl"

	// only the broker may ask
	for _, secret := range []string{"", "wrong", testSecret + "x"} {
		if code, _ := f.ask(t, auth, secret, map[string]string{}); code != 401 {
			t.Errorf("auth with secret %q: %d", secret, code)
		}
		if code, _ := f.ask(t, acl, secret, map[string]string{}); code != 401 {
			t.Errorf("acl with secret %q: %d", secret, code)
		}
	}

	tk, _ := f.tk.Issue(context.Background(), 2, time.Minute)
	code, body := f.ask(t, auth, testSecret, map[string]string{"clientid": "u2.abcdefgh", "username": "ticket", "password": tk})
	if code != 200 || body["result"] != "allow" || body["is_superuser"] != false {
		t.Errorf("ticket login: %d %v", code, body)
	}
	if _, body := f.ask(t, auth, testSecret, map[string]string{"clientid": "u2.abcdefgh", "username": "ticket", "password": tk}); body["result"] != "deny" {
		t.Errorf("a ticket worked twice: %v", body)
	}
	if _, body := f.ask(t, auth, testSecret, map[string]string{"clientid": "srv.1", "username": "api", "password": "server-pass"}); body["result"] != "allow" || body["is_superuser"] != true {
		t.Errorf("server login: %v", body)
	}
	if _, body := f.ask(t, auth, testSecret, map[string]string{"clientid": "srv.1", "username": "api", "password": "nope"}); body["result"] != "deny" || body["is_superuser"] != false {
		t.Errorf("bad server login: %v", body)
	}

	// access: bob is an editor, vic a viewer, eve a stranger
	ask := func(client, user, action, tp string) any {
		_, body := f.ask(t, acl, testSecret, map[string]string{"clientid": client, "username": user, "action": action, "topic": tp})
		return body["result"]
	}
	if ask("u2.abcdefgh", "ticket", "publish", "dg/f/"+f.fid+"/yjs/up/u2.abcdefgh") != "allow" {
		t.Error("an editor cannot write")
	}
	if ask("u5.abcdefgh", "ticket", "publish", "dg/f/"+f.fid+"/yjs/up/u5.abcdefgh") != "deny" {
		t.Error("a viewer can write")
	}
	if ask("u5.abcdefgh", "ticket", "subscribe", "dg/f/"+f.fid+"/yjs/down") != "allow" {
		t.Error("a viewer cannot read")
	}
	if ask("u3.abcdefgh", "ticket", "subscribe", "dg/f/"+f.fid+"/events") != "deny" {
		t.Error("a stranger can read")
	}
	if code, body := f.ask(t, acl, testSecret, "not an object"); code != 400 {
		t.Errorf("garbage: %d %v", code, body)
	}
	for _, c := range []struct{ kind, result string }{{"auth", "allow"}, {"auth", "deny"}, {"publish", "allow"}, {"publish", "deny"}, {"subscribe", "allow"}, {"subscribe", "deny"}} {
		if counterVal(mqttDecisions.WithLabelValues(c.kind, c.result)) < 1 {
			t.Errorf("decision %s/%s not counted", c.kind, c.result)
		}
	}
}

func TestMQTTInternalRoutesAreOffWithoutASecret(t *testing.T) {
	f := newRTFixture(t, diagramhub.Options{}, "")
	for _, p := range []string{"/api/internal/mqtt/auth", "/api/internal/mqtt/acl"} {
		if code, _ := f.ask(t, p, "", map[string]string{}); code != 404 && code != 405 {
			t.Errorf("%s answers (%d) without MQTT_INTERNAL_SECRET", p, code)
		}
	}
}

func TestDiagramRTCConfig(t *testing.T) {
	ice := []map[string]any{{"urls": []string{"stun:s.example:3478"}}, {"urls": []string{"turn:t.example:3478"}, "username": "u", "credential": "c"}}
	f := newRTFixture(t, diagramhub.Options{MaxPeers: 4, ICEServers: ice}, testSecret)
	if r := f.anon.do("GET", "/rtc/config", nil); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
	if r := f.anon.do("GET", "/rtc/config", nil, "X-Share-Token", "bogus"); r.Code != 404 {
		t.Errorf("bogus share: %d %v", r.Code, r.Body)
	}
	r := f.bob.expect(f.bob.do("GET", "/rtc/config", nil), 200)
	servers := r.Body["iceServers"].([]any)
	if r.Body["enabled"] != true || r.Body["maxPeers"] != float64(4) || len(servers) != 2 || servers[1].(map[string]any)["credential"] != "c" {
		t.Errorf("config = %v", r.Body)
	}
	made := f.alice.expect(f.alice.do("POST", "/projects/"+f.pid+"/shares", map[string]any{"role": "viewer", "fileId": f.fid}), 201)
	f.anon.expect(f.anon.do("GET", "/rtc/config", nil, "X-Share-Token", made.Body["token"].(string)), 200)
}

func TestDiagramRealtimeConfig(t *testing.T) {
	f := newRTFixture(t, diagramhub.Options{CollabEnabled: true}, testSecret)
	if r := f.anon.do("GET", "/realtime/config", nil); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
	r := f.bob.expect(f.bob.do("GET", "/realtime/config", nil), 200)
	if r.Body["wsPath"] != "/mqtt" || r.Body["collab"] != true || r.Body["presenceEveryMs"] != float64(30000) || r.Body["awarenessMaxPerSec"] != float64(15) {
		t.Errorf("config = %v", r.Body)
	}
}

// yjsUpdates are real Yjs updates (see the yjs package's testdata).
func yjsUpdates(t *testing.T, name string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile("../../domain/diagram/yjs/testdata/updates.json")
	if err != nil {
		t.Fatal(err)
	}
	var fs []struct {
		Name    string
		Updates []string
		Prefix  []string
	}
	if err := json.Unmarshal(raw, &fs); err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Name == name {
			var out [][]byte
			for _, u := range append(f.Updates, f.Prefix...) {
				b, _ := hex.DecodeString(u)
				out = append(out, b)
			}
			return out
		}
	}
	t.Fatalf("no fixture %q", name)
	return nil
}

func TestDiagramYDocCompaction(t *testing.T) {
	f := newRTFixture(t, diagramhub.Options{CollabEnabled: true}, testSecret)
	ups := yjsUpdates(t, "two clients")
	ctx := context.Background()
	for _, u := range ups[:2] {
		if _, err := f.mem.YAppend(ctx, f.fid, u); err != nil {
			t.Fatal(err)
		}
	}
	put := func(user, query string, b []byte) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodPut, f.srv.URL+DiagramPrefix+"/files/"+f.fid+"/ydoc"+query, bytes.NewReader(b))
		req.Header.Set("X-Test-User", user)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := put("4", "?seq=1", ups[0]); code != http.StatusForbidden { // a commenter
		t.Errorf("commenter compaction: %d", code)
	}
	if code, _ := put("2", "", ups[0]); code != 400 {
		t.Errorf("no seq: %d", code)
	}
	if code, body := put("2", "?seq=1", []byte{9, 9, 9}); code != 400 || body["error"].(map[string]any)["code"] != "BAD_SNAPSHOT" {
		t.Errorf("garbage: %d %v", code, body)
	}
	if code, body := put("2", "?seq=1", ups[1]); code != 409 || body["error"].(map[string]any)["code"] != "SNAPSHOT_INCOMPLETE" {
		t.Errorf("incomplete: %d %v", code, body)
	}
	if code, body := put("2", "?seq=1", ups[0]); code != 200 || body["compacted"] != true {
		t.Errorf("compact: %d %v", code, body)
	}
	if code, body := put("2", "?seq=1", ups[0]); code != 200 || body["compacted"] != false {
		t.Errorf("compact again: %d %v", code, body)
	}
}

func TestDiagramYDocOffWithoutCollab(t *testing.T) {
	f := newRTFixture(t, diagramhub.Options{}, testSecret)
	req, _ := http.NewRequest(http.MethodPut, f.srv.URL+DiagramPrefix+"/files/"+f.fid+"/ydoc?seq=1", strings.NewReader("x"))
	req.Header.Set("X-Test-User", "1")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusNotFound {
		t.Errorf("ydoc with COLLAB_ENABLED off: %v %v", res, err)
	}
}

func TestDiagramRealtimeOffWithoutHub(t *testing.T) {
	srv, _ := newDiagramServer(t)
	c := dgClient{t, srv, 1}
	if r := c.do("GET", "/rtc/config", nil); r.Code != 404 {
		t.Errorf("no hub: %d", r.Code)
	}
}

type captureBus struct {
	mu   sync.Mutex
	msgs []application.Message
}

func (c *captureBus) Publish(_ context.Context, m application.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, m)
	return nil
}

func (c *captureBus) wait(t *testing.T, n int) []application.Message {
	t.Helper()
	for range 400 {
		c.mu.Lock()
		got := append([]application.Message(nil), c.msgs...)
		c.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d messages", n)
	return nil
}

// The use cases' events keep the Node server's wire shapes; they travel on the project and file topics with
// the originating client as the "origin" property.
func TestDiagramEventsWire(t *testing.T) {
	hub := diagramhub.New(diagramhub.Options{})
	t.Cleanup(hub.Close)
	bus := &captureBus{}
	hub.Attach(bus)
	ev := NewDiagramEvents(hub)
	by := application.DiagramActor{UserID: 7, Name: "Ann"}
	ev.Emit(context.Background(), application.DiagramEvent{Kind: application.EventTree, ProjectID: "p", Rev: 4, Reason: "file.created", FileID: "f", By: by, ClientID: "c1"})
	ev.Emit(context.Background(), application.DiagramEvent{Kind: application.EventFile, ProjectID: "p", FileID: "f", Version: 3, Name: "x", FileKind: "sdd", UpdatedAt: 99, By: application.DiagramActor{Share: true}, Restored: true})
	ev.Emit(context.Background(), application.DiagramEvent{Kind: application.EventFileDeleted, ProjectID: "p", FileID: "f"})
	got := bus.wait(t, 4)

	var tree map[string]any
	_ = json.Unmarshal(got[0].Payload, &tree)
	if got[0].Topic != "dg/p/p/events" || got[0].Origin != "c1" || got[0].QoS != 1 || tree["type"] != "tree.updated" || tree["rev"] != float64(4) ||
		tree["reason"] != "file.created" || tree["fileId"] != "f" || tree["by"].(map[string]any)["userId"] != "7" || tree["by"].(map[string]any)["name"] != "Ann" {
		t.Errorf("tree event = %s %+v", got[0].Topic, tree)
	}
	topics := []string{got[1].Topic, got[2].Topic}
	var file map[string]any
	_ = json.Unmarshal(got[1].Payload, &file)
	if !(topics[0] == "dg/f/f/events" && topics[1] == "dg/p/p/events") || file["type"] != "file.updated" || file["version"] != float64(3) ||
		file["restored"] != true || file["by"].(map[string]any)["share"] != true || file["kind"] != "sdd" {
		t.Errorf("file event = %v %+v", topics, file)
	}
	var ctl map[string]any
	_ = json.Unmarshal(got[3].Payload, &ctl)
	if got[3].Topic != "dg/f/f/events" || ctl["type"] != "ctl" || ctl["code"] != float64(diagramhub.CtlDeleted) {
		t.Errorf("delete event = %s %v", got[3].Topic, ctl)
	}
}
