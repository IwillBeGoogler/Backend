package diagramhub_test

// The realtime channels against a real EMQX broker: connect authentication,
// access rules, the Yjs round trip, presence with a Last Will, and 4404. The
// broker asks the API's own internal routes (served here on a test listener)
// over HTTP, exactly as in production. Skipped with -short or without Docker.
//
//	TEST_MQTT_URL          use a broker you already run (tcp://host:1883); it must be configured like
//	                       docker-compose.yml's emqx with MQTT_AUTH_BASE pointing at TEST_MQTT_AUTH_LISTEN,
//	                       the dev MQTT_PASSWORD and MQTT_INTERNAL_SECRET
//	TEST_MQTT_AUTH_LISTEN  where the test serves the internal routes in that case (default 0.0.0.0:18080)

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/iBlog/iblog-monolith-go/config"
	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/diagramtest"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/topic"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/yjs"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/mqtt"
	"github.com/iBlog/iblog-monolith-go/internal/interfaces/diagramhub"
	httpapi "github.com/iBlog/iblog-monolith-go/internal/interfaces/http"
)

const emqxConf = `
node { name = "emqx@127.0.0.1", cookie = "emqx-test-cookie", data_dir = "data" }
cluster.discovery_strategy = manual
listeners.tcp.default { bind = "0.0.0.0:1883" }
listeners.ws.default { bind = "0.0.0.0:8083", websocket { mqtt_path = "/mqtt" } }
listeners.ssl.default { enable = false }
listeners.wss.default { enable = false }
dashboard.listeners.http.bind = 0
mqtt { max_packet_size = "16MB", max_topic_levels = 8 }
authentication = [{
  mechanism = password_based, backend = http, method = post
  url = "%[1]s/api/internal/mqtt/auth"
  headers { "content-type" = "application/json", authorization = "Bearer %[2]s" }
  body { clientid = "${clientid}", username = "${username}", password = "${password}" }
  pool_size = 4, connect_timeout = "5s", request_timeout = "5s"
}]
authorization {
  no_match = deny
  deny_action = ignore
  cache { enable = true, max_size = 64, ttl = "30s" }
  sources = [{
    type = http, enable = true, method = post
    url = "%[1]s/api/internal/mqtt/acl"
    headers { "content-type" = "application/json", authorization = "Bearer %[2]s" }
    body { clientid = "${clientid}", username = "${username}", action = "${action}", topic = "${topic}" }
    pool_size = 4, connect_timeout = "5s", request_timeout = "5s"
  }]
}
`

type tickets struct {
	mu sync.Mutex
	m  map[string]int
	n  int
}

func (k *tickets) Issue(_ context.Context, user int, _ time.Duration) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]int{}
	}
	k.n++
	t := "ticket" + strconv.Itoa(k.n)
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

type rig struct {
	t       *testing.T
	addr    string // host:port of the broker's TCP listener
	d       *application.Diagrams
	mem     *diagramtest.Memory
	hub     *diagramhub.Hub
	tk      *tickets
	pid     string
	fid     string
	viewTok string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	secret, serverPass := config.DevMQTTSecret, config.DevMQTTPassword

	mem := diagramtest.NewMemory()
	users := diagramtest.NewUsers()
	users.Add(1, "alice", "alice@example.com", "Alice A")
	users.Add(2, "bob", "bob@example.com", "")
	users.Add(3, "vic", "vic@example.com", "")
	users.Add(4, "eve", "eve@example.com", "")
	d := application.NewDiagrams(mem, mem)
	d.Users = users
	tk := &tickets{}
	access := &application.MQTTAccess{D: d, Tickets: tk, Guests: &application.MemoryGuests{}, ServerUser: "api", ServerPassword: serverPass,
		CollabEnabled: true, TTL: 5 * time.Second}
	hub := diagramhub.New(diagramhub.Options{Authority: access, Y: mem, CollabEnabled: true, CompactAfter: 3, FlushEvery: 50 * time.Millisecond,
		OnFileGone: access.Forget})
	t.Cleanup(hub.Close)
	access.OnDenied = hub.Denied
	d.Events = httpapi.NewDiagramEvents(hub)

	mux := http.NewServeMux()
	httpapi.NewHandler(httpapi.Deps{Diagrams: d, Realtime: hub, MQTT: access, MQTTInternalSecret: secret}).Routes(mux)

	var addr string
	if u := os.Getenv("TEST_MQTT_URL"); u != "" {
		pu, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		addr = pu.Host
		listen := os.Getenv("TEST_MQTT_AUTH_LISTEN")
		if listen == "" {
			listen = "0.0.0.0:18080"
		}
		serve(t, listen, mux)
	} else {
		l := serve(t, "0.0.0.0:0", mux)
		base := "http://host.docker.internal:" + strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
		c, err := testcontainers.Run(ctx, "emqx/emqx:5.8.6",
			testcontainers.WithExposedPorts("1883/tcp"),
			testcontainers.WithFiles(testcontainers.ContainerFile{Reader: strings.NewReader(fmt.Sprintf(emqxConf, base, secret)),
				ContainerFilePath: "/opt/emqx/etc/emqx.conf", FileMode: 0o644}),
			testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
				hc.ExtraHosts = append(hc.ExtraHosts, "host.docker.internal:host-gateway")
			}),
			testcontainers.WithWaitStrategyAndDeadline(2*time.Minute, wait.ForListeningPort("1883/tcp")),
		)
		if err != nil {
			t.Skipf("docker unavailable: %v", err)
		}
		t.Cleanup(func() { testcontainers.TerminateContainer(c) })
		host, err := c.Host(ctx)
		if err != nil {
			t.Fatal(err)
		}
		port, err := c.MappedPort(ctx, "1883/tcp")
		if err != nil {
			t.Fatal(err)
		}
		addr = net.JoinHostPort(host, port.Port())
	}

	// the API's own connection, as app.New makes it
	shared, all := diagramhub.Subscriptions()
	var subs []mqtt.Subscription
	for _, f := range append(shared, all...) {
		subs = append(subs, mqtt.Subscription{Filter: f, QoS: 1})
	}
	mctx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	bus, err := mqtt.Connect(mctx, mqtt.Config{URL: "tcp://" + addr, Username: "api", Password: serverPass}, subs, hub.OnMessage)
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := bus.Connected(cctx); err != nil {
		t.Fatalf("the API could not log in to the broker: %v", err)
	}
	hub.Attach(bus)

	res, err := d.CreateProject(ctx, 1, application.NewDiagramProject{Name: "P"})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, addr: addr, d: d, mem: mem, hub: hub, tk: tk, pid: res.Project.ID, fid: res.File.ID}
	for email, role := range map[string]diagram.Role{"bob@example.com": diagram.RoleEditor, "vic@example.com": diagram.RoleViewer} {
		if _, err := d.AddMember(ctx, 1, r.pid, email, role); err != nil {
			t.Fatal(err)
		}
	}
	_, r.viewTok, err = d.CreateShare(ctx, 1, r.pid, application.NewShare{Role: diagram.RoleViewer, FileID: &r.fid})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func serve(t *testing.T, listen string, h http.Handler) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return l
}

// client is a browser stand-in: a plain MQTT 5 client.
type client struct {
	t    *testing.T
	id   string
	conn net.Conn
	c    *paho.Client
	mu   sync.Mutex
	got  []paho.PublishReceived
}

type login struct {
	user, pass string
	will       *paho.WillMessage
}

// dial connects; it returns the CONNACK reason code (0 on success).
func (r *rig) dial(id string, l login) (*client, byte) {
	r.t.Helper()
	conn, err := net.DialTimeout("tcp", r.addr, 5*time.Second)
	if err != nil {
		r.t.Fatal(err)
	}
	cl := &client{t: r.t, id: id, conn: conn}
	cl.c = paho.NewClient(paho.ClientConfig{ClientID: id, Conn: conn,
		OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(pr paho.PublishReceived) (bool, error) {
			cl.mu.Lock()
			cl.got = append(cl.got, pr)
			cl.mu.Unlock()
			return true, nil
		}}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ack, err := cl.c.Connect(ctx, &paho.Connect{ClientID: id, KeepAlive: 30, CleanStart: true, UsernameFlag: true, Username: l.user,
		PasswordFlag: l.pass != "", Password: []byte(l.pass), WillMessage: l.will})
	if err != nil {
		conn.Close()
		if ack != nil {
			return nil, ack.ReasonCode
		}
		return nil, 0xff
	}
	r.t.Cleanup(func() { conn.Close() })
	return cl, ack.ReasonCode
}

func (r *rig) ticketClient(user int, nonce string, will *paho.WillMessage) *client {
	r.t.Helper()
	tok, _ := r.tk.Issue(context.Background(), user, time.Minute)
	cl, code := r.dial(topic.ClientID(user, nonce), login{"ticket", tok, will})
	if cl == nil {
		r.t.Fatalf("user %d could not connect: reason %#x", user, code)
	}
	return cl
}

func (c *client) subscribe(filter string) byte {
	c.t.Helper()
	ack, err := c.c.Subscribe(context.Background(), &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: filter, QoS: 1}}})
	// paho returns the SUBACK together with an error when the broker refuses: the reason code is the answer
	if ack != nil && len(ack.Reasons) > 0 {
		return ack.Reasons[0]
	}
	c.t.Fatalf("subscribe %s: %v", filter, err)
	return 0
}

func (c *client) publish(tp string, payload []byte, qos byte) byte {
	c.t.Helper()
	res, err := c.c.Publish(context.Background(), &paho.Publish{Topic: tp, Payload: payload, QoS: qos})
	if res != nil {
		return res.ReasonCode // a refused PUBLISH comes back with its PUBACK and an error
	}
	if err != nil {
		c.t.Fatalf("publish %s: %v", tp, err)
	}
	return 0
}

// waitFor returns the first received message matching cond.
func (c *client) waitFor(what string, cond func(paho.PublishReceived) bool) paho.PublishReceived {
	c.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for _, m := range c.got {
			if cond(m) {
				c.mu.Unlock()
				return m
			}
		}
		c.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	c.t.Fatalf("%s: timed out waiting for %s", c.id, what)
	return paho.PublishReceived{}
}

func onTopic(tp string) func(paho.PublishReceived) bool {
	return func(m paho.PublishReceived) bool { return m.Packet.Topic == tp }
}

func (c *client) origin(m paho.PublishReceived) string {
	if m.Packet.Properties == nil {
		return ""
	}
	return m.Packet.Properties.User.Get("origin")
}

func realUpdates(t *testing.T) [][]byte {
	t.Helper()
	raw, err := os.ReadFile("../../domain/diagram/yjs/testdata/updates.json")
	if err != nil {
		t.Fatal(err)
	}
	var fs []struct {
		Name    string
		Updates []string
	}
	if err := json.Unmarshal(raw, &fs); err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for _, f := range fs {
		if f.Name == "two clients" {
			for _, u := range f.Updates {
				b, _ := hex.DecodeString(u)
				out = append(out, b)
			}
		}
	}
	return out
}

const denied = 0x87

func TestEMQXConnectAuthentication(t *testing.T) {
	r := newRig(t)
	if cl, code := r.dial("u1.aaaaaaaa", login{"ticket", "no-such-ticket", nil}); cl != nil || code == 0 {
		t.Errorf("a bad ticket connected (%#x)", code)
	}
	// bob's ticket cannot be used under alice's client id
	tok, _ := r.tk.Issue(context.Background(), 2, time.Minute)
	if cl, _ := r.dial("u1.bbbbbbbb", login{"ticket", tok, nil}); cl != nil {
		t.Error("bob's ticket opened a connection named for alice")
	}
	// a ticket works once
	tok, _ = r.tk.Issue(context.Background(), 2, time.Minute)
	if cl, _ := r.dial("u2.cccccccc", login{"ticket", tok, nil}); cl == nil {
		t.Fatal("a valid ticket was refused")
	}
	if cl, _ := r.dial("u2.dddddddd", login{"ticket", tok, nil}); cl != nil {
		t.Error("a ticket worked twice")
	}
	// no login at all
	if cl, _ := r.dial("anon-client", login{"", "", nil}); cl != nil {
		t.Error("anonymous connection accepted")
	}
	// a share-link holder connects as a guest
	if cl, code := r.dial("g.eeeeeeee", login{"share", r.viewTok, nil}); cl == nil {
		t.Errorf("share login refused: %#x", code)
	}
	if cl, _ := r.dial("g.ffffffff", login{"share", strings.Repeat("a", 22), nil}); cl != nil {
		t.Error("a bogus share token connected")
	}
}

func TestEMQXAccessRules(t *testing.T) {
	r := newRig(t)
	f, p := r.fid, r.pid
	bob := r.ticketClient(2, "bobbobbob", nil)
	vic := r.ticketClient(3, "vicvicvic", nil)
	eve := r.ticketClient(4, "eveeveeve", nil)
	guest, code := r.dial("g.guestguest", login{"share", r.viewTok, nil})
	if guest == nil {
		t.Fatalf("guest: %#x", code)
	}

	for _, tp := range []string{"dg/f/" + f + "/events", "dg/p/" + p + "/events", "dg/f/" + f + "/yjs/down", "dg/f/" + f + "/roster/+", "dg/c/" + bob.id + "/ctl"} {
		if rc := bob.subscribe(tp); rc >= 0x80 {
			t.Errorf("bob cannot subscribe to %s (%#x)", tp, rc)
		}
	}
	for _, tp := range []string{"dg/#", "#", "$SYS/#", "dg/f/" + f + "/yjs/sync/" + vic.id, "dg/c/" + vic.id + "/ctl", "$share/dg/dg/f/+/yjs/up/+"} {
		if rc := bob.subscribe(tp); rc != denied {
			t.Errorf("bob subscribed to %s (%#x)", tp, rc)
		}
	}
	if rc := eve.subscribe("dg/f/" + f + "/events"); rc != denied {
		t.Errorf("a stranger subscribed (%#x)", rc)
	}
	if rc := guest.subscribe("dg/f/" + f + "/events"); rc >= 0x80 {
		t.Errorf("a file-link guest cannot read its file (%#x)", rc)
	}
	if rc := guest.subscribe("dg/p/" + p + "/events"); rc != denied {
		t.Errorf("a file-link guest read the project (%#x)", rc)
	}

	u := realUpdates(t)
	// a viewer's write is refused by the broker, and the viewer is told why
	vic.subscribe("dg/c/" + vic.id + "/ctl")
	if rc := vic.publish("dg/f/"+f+"/yjs/up/"+vic.id, yjs.Update(u[0]), 1); rc != denied {
		t.Errorf("a viewer's Yjs write: reason %#x", rc)
	}
	m := vic.waitFor("denied", onTopic("dg/c/"+vic.id+"/ctl"))
	if !strings.Contains(string(m.Packet.Payload), `"denied"`) {
		t.Errorf("ctl = %s", m.Packet.Payload)
	}
	// nobody publishes the server-only topics, or other clients' topics
	for _, tp := range []string{"dg/f/" + f + "/yjs/down", "dg/f/" + f + "/events", "dg/f/" + f + "/yjs/up/" + bob.id, "dg/f/" + f + "/awareness/out"} {
		if rc := vic.publish(tp, []byte("x"), 1); rc != denied {
			t.Errorf("vic published to %s (%#x)", tp, rc)
		}
	}
	if st, _ := r.mem.YLoad(context.Background(), f); len(st.Updates) != 0 {
		t.Error("a refused write was stored")
	}
	if rc := bob.publish("dg/f/"+f+"/yjs/up/"+bob.id, yjs.Update(u[0]), 1); rc >= 0x80 {
		t.Errorf("an editor's write was refused (%#x)", rc)
	}
}

func TestEMQXYjsRoundTrip(t *testing.T) {
	r := newRig(t)
	f := r.fid
	alice := r.ticketClient(1, "alicealice", nil)
	bob := r.ticketClient(2, "bobbobbob", nil)
	vic := r.ticketClient(3, "vicvicvic", nil)
	for _, c := range []*client{alice, bob, vic} {
		for _, tp := range []string{"dg/f/" + f + "/yjs/down", "dg/f/" + f + "/yjs/sync/" + c.id} {
			if rc := c.subscribe(tp); rc >= 0x80 {
				t.Fatalf("%s subscribe %s: %#x", c.id, tp, rc)
			}
		}
	}
	u := realUpdates(t)
	alice.publish("dg/f/"+f+"/yjs/up/"+alice.id, yjs.Update(u[0]), 1)
	m := bob.waitFor("the update", onTopic("dg/f/"+f+"/yjs/down"))
	d, err := yjs.Decode(m.Packet.Payload)
	if err != nil || d.Sub != yjs.SyncUpdate || hex.EncodeToString(d.Payload) != hex.EncodeToString(u[0]) || bob.origin(m) != alice.id {
		t.Errorf("down = %x origin %q (%v)", m.Packet.Payload, bob.origin(m), err)
	}
	// stored in a batch
	deadline := time.Now().Add(10 * time.Second)
	for {
		if st, _ := r.mem.YLoad(context.Background(), f); len(st.Updates) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the update was not stored")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// a viewer joins: asks with sync step 1, gets the history, an empty step 2 and the server's step 1
	vic.publish("dg/f/"+f+"/yjs/req/"+vic.id, yjs.Step1([]byte{0}), 1)
	vic.waitFor("history", func(m paho.PublishReceived) bool {
		d, err := yjs.Decode(m.Packet.Payload)
		return m.Packet.Topic == "dg/f/"+f+"/yjs/sync/"+vic.id && err == nil && d.Sub == yjs.SyncUpdate
	})
	vic.waitFor("the server's step 1", func(m paho.PublishReceived) bool {
		d, err := yjs.Decode(m.Packet.Payload)
		return m.Packet.Topic == "dg/f/"+f+"/yjs/sync/"+vic.id && err == nil && d.Sub == yjs.SyncStep1
	})
}

func TestEMQXPresenceLastWill(t *testing.T) {
	r := newRig(t)
	f, p := r.fid, r.pid
	alice := r.ticketClient(1, "alicealice", nil)
	will := &paho.WillMessage{Topic: "dg/f/" + f + "/presence/" + topic.ClientID(2, "bobbobbob"), QoS: 1}
	bob := r.ticketClient(2, "bobbobbob", will)
	alice.subscribe("dg/f/" + f + "/roster/+")
	alice.subscribe("dg/p/" + p + "/roster/+/+")

	bob.publish(will.Topic, []byte(`{"state":"online"}`), 0)
	rosterTopic := "dg/f/" + f + "/roster/" + bob.id
	m := alice.waitFor("bob's roster entry", func(m paho.PublishReceived) bool {
		return m.Packet.Topic == rosterTopic && len(m.Packet.Payload) > 0
	})
	var e struct{ ClientID, Name, Role string }
	_ = json.Unmarshal(m.Packet.Payload, &e)
	if e.ClientID != bob.id || e.Name != "bob" || e.Role != "editor" {
		t.Errorf("roster entry = %s", m.Packet.Payload)
	}
	alice.waitFor("the project roster", onTopic("dg/p/"+p+"/roster/"+f+"/"+bob.id))

	// a late subscriber gets the retained entry
	late := r.ticketClient(3, "vicvicvic", nil)
	late.subscribe("dg/f/" + f + "/roster/+")
	late.waitFor("the retained roster", onTopic(rosterTopic))

	// bob's connection dies without DISCONNECT: the broker publishes his Last Will, the server clears the entry
	bob.conn.Close()
	alice.waitFor("the cleared entry", func(m paho.PublishReceived) bool {
		return m.Packet.Topic == rosterTopic && len(m.Packet.Payload) == 0
	})
}

func TestEMQXFileDeletedIs4404(t *testing.T) {
	r := newRig(t)
	f := r.fid
	alice := r.ticketClient(1, "alicealice", nil)
	bob := r.ticketClient(2, "bobbobbob", nil)
	bob.subscribe("dg/f/" + f + "/events")

	if err := r.d.DeleteFile(context.Background(), 1, f); err != nil {
		t.Fatal(err)
	}
	m := bob.waitFor("4404", func(m paho.PublishReceived) bool {
		return m.Packet.Topic == "dg/f/"+f+"/events" && strings.Contains(string(m.Packet.Payload), `"code":4404`)
	})
	if !strings.Contains(string(m.Packet.Payload), `"type":"ctl"`) {
		t.Errorf("ctl = %s", m.Packet.Payload)
	}
	_ = alice
	// the file is gone: nobody can subscribe again (a fresh connection has no cached answer)
	again := r.ticketClient(2, "bobagain1", nil)
	deadline := time.Now().Add(15 * time.Second)
	for again.subscribe("dg/f/"+f+"/events") != denied {
		if time.Now().After(deadline) {
			t.Fatal("a deleted file can still be subscribed to")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
