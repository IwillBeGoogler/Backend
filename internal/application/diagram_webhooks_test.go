package application

// Webhook deliveries (queueing, signing, retries with backoff), the
// webhook use cases' rules, and personal access tokens with their project
// limit.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
)

type queued struct {
	id      string
	attempt int
	at      time.Time
}

type memQueue struct {
	mu   sync.Mutex
	jobs []queued
	err  error
}

func (q *memQueue) DeliverWebhookAt(_ context.Context, id string, attempt int, at time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	q.jobs = append(q.jobs, queued{id, attempt, at})
	return nil
}

func (q *memQueue) last() queued { return q.jobs[len(q.jobs)-1] }

type scriptedSender struct {
	status int
	err    error
	sent   []WebhookRequest
}

func (s *scriptedSender) Send(_ context.Context, r WebhookRequest) (int, error) {
	s.sent = append(s.sent, r)
	return s.status, s.err
}

type hookEnv struct {
	*Diagrams
	mem    *diagramtest.Memory
	q      *memQueue
	sender *scriptedSender
	pid    string
	fid    string
	clock  *time.Time
}

func newHooks(t *testing.T) hookEnv {
	t.Helper()
	d, mem := newDiagrams(t)
	now := time.Unix(1_700_000_000, 0)
	d.now = func() time.Time { return now }
	users := diagramtest.NewUsers()
	users.Add(alice, "alice", "alice@example.com", "")
	users.Add(bob, "bob", "bob@example.com", "")
	users.Add(eve, "eve", "eve@example.com", "")
	d.Users = users
	q, sender := &memQueue{}, &scriptedSender{status: 200}
	d.Hooks = NewWebhookDispatcher(d.Webhooks, q, sender, func(_ context.Context, u string) error {
		if u == "http://internal.test/" {
			return errors.New("private address")
		}
		return nil
	})
	d.Hooks.now = func() time.Time { return now }
	d.Hooks.jitter = func() float64 { return 0 }
	d.Events = d.Hooks
	res := project(t, d, alice, "Hooks")
	return hookEnv{Diagrams: d, mem: mem, q: q, sender: sender, pid: res.Project.ID, fid: res.File.ID, clock: &now}
}

func (e hookEnv) hook(t *testing.T, events ...string) diagram.Webhook {
	t.Helper()
	return must1[diagram.Webhook](t)(e.CreateWebhook(dctx, alice, e.pid, "https://hooks.example.com/in", events))
}

func TestWebhookEmitMapsEventsToSubscribers(t *testing.T) {
	e := newHooks(t)
	all := e.hook(t, diagram.WebhookEvents...)
	updatedOnly := e.hook(t, diagram.WebhookFileUpdated)
	off := e.hook(t, diagram.WebhookFileUpdated)
	active := false
	must1[diagram.Webhook](t)(e.UpdateWebhook(dctx, alice, e.pid, off.ID, WebhookPatch{Active: &active}))

	doc := docOf("a")
	must1[DiagramFile](t)(e.SaveFile(dctx, alice, e.fid, nil, doc, `"1"`))
	got := func(h diagram.Webhook) []diagram.Delivery {
		return must1[[]diagram.Delivery](t)(e.WebhookDeliveries(dctx, alice, e.pid, h.ID))
	}
	if n := len(got(all)); n != 1 {
		t.Fatalf("all: %d deliveries", n)
	}
	if n := len(got(updatedOnly)); n != 1 {
		t.Fatalf("updatedOnly: %d deliveries", n)
	}
	if n := len(got(off)); n != 0 {
		t.Fatalf("an inactive webhook got %d deliveries", n)
	}
	d := got(all)[0]
	var p map[string]any
	if err := json.Unmarshal([]byte(d.Payload), &p); err != nil {
		t.Fatal(err)
	}
	if p["event"] != "file.updated" || p["projectId"] != e.pid || p["fileId"] != e.fid || p["version"] != float64(2) || p["data"] == nil ||
		p["id"] == "" || p["at"] == "" || d.Status != diagram.DeliveryPending || d.Attempt != 0 {
		t.Errorf("payload %v, delivery %+v", p, d)
	}

	// a created file, a deleted file, a library save
	created, _, err := e.CreateFile(dctx, alice, e.pid, NewDiagramFile{Name: "Two"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(got(all), "file.created") || hasEvent(got(updatedOnly), "file.created") {
		t.Errorf("file.created fan-out: %v / %v", got(all), got(updatedOnly))
	}
	if err := e.DeleteFile(dctx, alice, created.ID); err != nil {
		t.Fatal(err)
	}
	if !hasEvent(got(all), "file.deleted") {
		t.Error("no file.deleted delivery")
	}
	if _, err := e.SaveLibrary(dctx, alice, e.pid, 0, json.RawMessage(`{"components":{},"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{}}`)); err != nil {
		t.Fatal(err)
	}
	if !hasEvent(got(all), "library.updated") {
		t.Error("no library.updated delivery")
	}
	// folder changes and renames are not webhook events
	before := len(got(all))
	if _, _, err := e.CreateFolder(dctx, alice, e.pid, NewFolder{Name: "Docs"}); err != nil {
		t.Fatal(err)
	}
	if len(got(all)) != before {
		t.Error("a folder change produced a delivery")
	}
	// every delivery was queued, first attempt, now
	if len(e.q.jobs) == 0 || e.q.jobs[0].attempt != 1 || !e.q.jobs[0].at.Equal(*e.clock) {
		t.Errorf("jobs = %+v", e.q.jobs)
	}
}

func hasEvent(ds []diagram.Delivery, ev string) bool {
	for _, d := range ds {
		if d.Event == ev {
			return true
		}
	}
	return false
}

func TestWebhookDeliveryRetriesWithBackoff(t *testing.T) {
	e := newHooks(t)
	h := e.hook(t, diagram.WebhookFileUpdated)
	must1[DiagramFile](t)(e.SaveFile(dctx, alice, e.fid, nil, docOf("a"), `"1"`))
	id := e.q.last().id
	e.sender.status = 503

	want := []time.Duration{10 * time.Second, time.Minute, 6 * time.Minute, 36 * time.Minute}
	for i, delay := range want {
		if err := e.Hooks.Deliver(dctx, id); err != nil {
			t.Fatal(err)
		}
		d := must1[diagram.Delivery](t)(e.Webhooks.Delivery(dctx, id))
		if d.Status != diagram.DeliveryPending || d.Attempt != i+1 || d.Error != "HTTP 503" || *d.ResponseCode != 503 {
			t.Fatalf("attempt %d: %+v", i+1, d)
		}
		if d.NextAttemptAt == nil || d.NextAttemptAt.Sub(*e.clock) != delay {
			t.Fatalf("attempt %d: next = %v, want +%v", i+1, d.NextAttemptAt, delay)
		}
		if j := e.q.last(); j.id != id || j.attempt != i+2 || !j.at.Equal(*d.NextAttemptAt) {
			t.Fatalf("attempt %d: queued %+v", i+1, j)
		}
		// an early run waits for the backoff instead of sending
		sent := len(e.sender.sent)
		if err := e.Hooks.Deliver(dctx, id); err != nil || len(e.sender.sent) != sent {
			t.Fatalf("attempt %d: early retry sent (err %v)", i+1, err)
		}
		*e.clock = *d.NextAttemptAt
	}
	// the fifth attempt is the last
	jobs := len(e.q.jobs)
	if err := e.Hooks.Deliver(dctx, id); err != nil {
		t.Fatal(err)
	}
	d := must1[diagram.Delivery](t)(e.Webhooks.Delivery(dctx, id))
	if d.Status != diagram.DeliveryFailed || d.Attempt != diagram.MaxDeliveryAttempts || d.NextAttemptAt != nil || len(e.q.jobs) != jobs {
		t.Errorf("after the last attempt: %+v, %d jobs", d, len(e.q.jobs))
	}
	// a repeated task does nothing
	n := len(e.sender.sent)
	if err := e.Hooks.Deliver(dctx, id); err != nil || len(e.sender.sent) != n {
		t.Error("a finished delivery was sent again")
	}
	// every attempt was signed with the secret
	secret := h.Secret
	for _, r := range e.sender.sent {
		if !diagram.VerifySignature(secret, r.Timestamp, r.Body, r.Signature, time.Unix(r.Timestamp, 0), 0) || r.DeliveryID != id || r.Event != "file.updated" {
			t.Fatalf("request %+v", r)
		}
	}
}

func TestWebhookDeliveryOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		err    error
		want   string
		retry  bool
		errMsg string
	}{
		{"2xx", 204, nil, diagram.DeliveryOK, false, ""},
		{"4xx is final", 404, nil, diagram.DeliveryFailed, false, "HTTP 404"},
		{"429 retries", 429, nil, diagram.DeliveryPending, true, "HTTP 429"},
		{"408 retries", 408, nil, diagram.DeliveryPending, true, "HTTP 408"},
		{"redirect is final", 302, nil, diagram.DeliveryFailed, false, "HTTP 302"},
		{"network error retries", 0, errors.New("connection refused"), diagram.DeliveryPending, true, "connection refused"},
		{"timeout retries", 0, context.DeadlineExceeded, diagram.DeliveryPending, true, "timeout"},
		{"a rejected target is final", 0, errors.New("x"), diagram.DeliveryFailed, false, "URL rejected: blocked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newHooks(t)
			e.hook(t, diagram.WebhookFileUpdated)
			must1[DiagramFile](t)(e.SaveFile(dctx, alice, e.fid, nil, docOf("a"), `"1"`))
			id := e.q.last().id
			e.sender.status, e.sender.err = c.status, c.err
			if c.name == "a rejected target is final" {
				e.sender.err = wrapURL("blocked")
			}
			if err := e.Hooks.Deliver(dctx, id); err != nil {
				t.Fatal(err)
			}
			d := must1[diagram.Delivery](t)(e.Webhooks.Delivery(dctx, id))
			if d.Status != c.want || d.Attempt != 1 || (d.NextAttemptAt != nil) != c.retry || d.Error != c.errMsg {
				t.Errorf("%+v", d)
			}
			if c.want == diagram.DeliveryOK && d.Error != "" {
				t.Error("a delivered attempt keeps no error")
			}
		})
	}
}

func wrapURL(msg string) error { return &urlErr{msg} }

type urlErr struct{ msg string }

func (e *urlErr) Error() string { return ErrWebhookURL.Error() + ": " + e.msg }
func (e *urlErr) Unwrap() error { return ErrWebhookURL }

func TestWebhookDeliveryEdgeCases(t *testing.T) {
	e := newHooks(t)
	h := e.hook(t, diagram.WebhookFileUpdated)
	must1[DiagramFile](t)(e.SaveFile(dctx, alice, e.fid, nil, docOf("a"), `"1"`))
	id := e.q.last().id

	// disabled between queueing and sending: final, nothing is sent
	off := false
	must1[diagram.Webhook](t)(e.UpdateWebhook(dctx, alice, e.pid, h.ID, WebhookPatch{Active: &off}))
	if err := e.Hooks.Deliver(dctx, id); err != nil {
		t.Fatal(err)
	}
	d := must1[diagram.Delivery](t)(e.Webhooks.Delivery(dctx, id))
	if d.Status != diagram.DeliveryFailed || d.Error != "webhook disabled" || len(e.sender.sent) != 0 {
		t.Errorf("disabled: %+v sent %d", d, len(e.sender.sent))
	}

	// a delivery whose webhook (and so the row) is gone is dropped quietly
	if err := e.DeleteWebhook(dctx, alice, e.pid, h.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Hooks.Deliver(dctx, id); err != nil {
		t.Errorf("deleted webhook: %v", err)
	}

	// the URL is checked again before every attempt
	h2 := e.hook(t, diagram.WebhookFileUpdated)
	e.mem.ReplaceURL(h2.ID, "http://internal.test/")
	rejected := 0
	e.Hooks.OnRejected = func() { rejected++ }
	must1[DiagramFile](t)(e.SaveFile(dctx, alice, e.fid, nil, docOf("b"), `"2"`))
	id2 := e.q.last().id
	if err := e.Hooks.Deliver(dctx, id2); err != nil {
		t.Fatal(err)
	}
	d = must1[diagram.Delivery](t)(e.Webhooks.Delivery(dctx, id2))
	if d.Status != diagram.DeliveryFailed || d.Error != "URL rejected: private address" || len(e.sender.sent) != 0 {
		t.Errorf("rebound url: %+v", d)
	}
	if rejected != 1 {
		t.Errorf("OnRejected called %d times", rejected)
	}

	// a queue that is down: the delivery is marked failed, the save still works
	e.q.err = errors.New("redis down")
	h3 := e.hook(t, diagram.WebhookFileUpdated)
	if _, err := e.SaveFile(dctx, alice, e.fid, nil, docOf("c"), `"3"`); err != nil {
		t.Fatalf("a webhook problem failed the save: %v", err)
	}
	ds := must1[[]diagram.Delivery](t)(e.WebhookDeliveries(dctx, alice, e.pid, h3.ID))
	if len(ds) != 1 || ds[0].Status != diagram.DeliveryFailed || ds[0].Error != "could not be queued" {
		t.Errorf("unqueueable: %+v", ds)
	}
}

func TestWebhookRules(t *testing.T) {
	e := newHooks(t)
	// the URL guard and field rules
	for _, u := range []string{"http://internal.test/", "", " "} {
		if _, err := e.CreateWebhook(dctx, alice, e.pid, u, []string{"file.updated"}); err == nil {
			t.Errorf("url %q accepted", u)
		}
	}
	_, err := e.CreateWebhook(dctx, alice, e.pid, "http://internal.test/", []string{"file.updated"})
	var de *diagram.Error
	if !errors.As(err, &de) || de.Code != "WEBHOOK_URL_REJECTED" || !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("rejected url error = %v", err)
	}
	for _, evs := range [][]string{nil, {}, {"file.nope"}, {"ping"}} {
		if _, err := e.CreateWebhook(dctx, alice, e.pid, "https://hooks.example.com/", evs); err == nil {
			t.Errorf("events %v accepted", evs)
		}
	}
	// roles: editors manage, viewers do not, strangers see nothing
	must1[diagram.Member](t)(e.AddMember(dctx, alice, e.pid, "bob", diagram.RoleEditor))
	if _, err := e.CreateWebhook(dctx, bob, e.pid, "https://hooks.example.com/", []string{"file.updated"}); err != nil {
		t.Errorf("editor: %v", err)
	}
	if _, err := e.ListWebhooks(dctx, eve, e.pid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}
	// webhooks of another project are not reachable through this one
	other := project(t, e.Diagrams, alice, "Other")
	h := e.hook(t, diagram.WebhookFileUpdated)
	if _, err := e.UpdateWebhook(dctx, alice, other.Project.ID, h.ID, WebhookPatch{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-project update: %v", err)
	}
	if err := e.DeleteWebhook(dctx, alice, other.Project.ID, h.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-project delete: %v", err)
	}
}

func TestWebhookPingIsOneAttempt(t *testing.T) {
	e := newHooks(t)
	h := e.hook(t, diagram.WebhookFileUpdated)
	e.sender.status = 500
	d := must1[diagram.Delivery](t)(e.TestWebhook(dctx, alice, e.pid, h.ID))
	if d.Event != "ping" || d.Status != diagram.DeliveryFailed || d.NextAttemptAt != nil || d.Attempt != 1 || len(e.q.jobs) != 0 {
		t.Errorf("ping = %+v, jobs %v", d, e.q.jobs)
	}
	stored := must1[diagram.Delivery](t)(e.Webhooks.Delivery(dctx, d.ID))
	if stored.Status != diagram.DeliveryFailed || stored.NextAttemptAt != nil {
		t.Errorf("stored ping = %+v", stored)
	}
	// an inactive webhook can still be tested
	off := false
	must1[diagram.Webhook](t)(e.UpdateWebhook(dctx, alice, e.pid, h.ID, WebhookPatch{Active: &off}))
	e.sender.status = 200
	if d := must1[diagram.Delivery](t)(e.TestWebhook(dctx, alice, e.pid, h.ID)); d.Status != diagram.DeliveryOK {
		t.Errorf("ping of an inactive webhook = %+v", d)
	}
}

// Tokens

func TestAccessTokens(t *testing.T) {
	d, mem := newDiagrams(t)
	a := project(t, d, alice, "A")
	b := project(t, d, alice, "B")
	bobs := project(t, d, bob, "Bob's")

	secret, tok, err := d.CreateToken(dctx, alice, NewAccessToken{Name: "ci", Scopes: []string{"files:read"}, ProjectIDs: []string{a.Project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := mem.TokenByHash(dctx, diagram.HashToken(secret))
	if stored.ID != tok.ID || stored.Hash == secret || stored.Prefix != "sdd_pat_" {
		t.Errorf("stored = %+v", stored)
	}
	// a project the owner cannot read cannot be added to the limit
	if _, _, err := d.CreateToken(dctx, alice, NewAccessToken{Name: "x", Scopes: []string{"files:read"}, ProjectIDs: []string{bobs.Project.ID}}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("foreign project: %v", err)
	}

	got, err := d.AuthenticateToken(dctx, secret)
	if err != nil || got.UserID != alice || !got.Allows("files:read") || got.Allows("files:write") || !got.Reaches(a.Project.ID) || got.Reaches(b.Project.ID) {
		t.Fatalf("authenticate = %+v, %v", got, err)
	}

	// the limit hides other projects from every use case, and from the list
	lctx := WithTokenLimit(dctx, got.ProjectIDs)
	if _, err := d.GetFile(lctx, alice, a.File.ID); err != nil {
		t.Errorf("limited token on its project: %v", err)
	}
	if _, err := d.GetFile(lctx, alice, b.File.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("limited token on another project: %v", err)
	}
	if _, err := d.Tree(lctx, alice, b.Project.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("limited tree: %v", err)
	}
	if ps := must1[[]diagram.ProjectSummary](t)(d.ListAllProjects(lctx, alice)); len(ps) != 1 || ps[0].ID != a.Project.ID {
		t.Errorf("limited list = %v", ps)
	}
	if ps := must1[[]diagram.ProjectSummary](t)(d.ListAllProjects(dctx, alice)); len(ps) != 2 {
		t.Errorf("unlimited list = %d", len(ps))
	}
	// an empty limit reaches nothing
	if ps := must1[[]diagram.ProjectSummary](t)(d.ListAllProjects(WithTokenLimit(dctx, []string{}), alice)); len(ps) != 0 {
		t.Errorf("empty limit = %v", ps)
	}

	// revoked, expired, unknown, malformed
	if err := d.RevokeToken(dctx, bob, tok.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revoking someone else's token: %v", err)
	}
	if err := d.RevokeToken(dctx, alice, tok.ID); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{"revoked": secret, "unknown": "sdd_pat_" + "A12345678901234567890123456789ab", "malformed": "nope", "empty": ""} {
		if _, err := d.AuthenticateToken(dctx, raw); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("%s: %v", name, err)
		}
	}
	s2, t2 := must3(t)(d.CreateToken(dctx, alice, NewAccessToken{Name: "short", Scopes: []string{"files:read"}, ExpiresInDays: 1}))
	d.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	if _, err := d.AuthenticateToken(dctx, s2); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("expired: %v (%+v)", err, t2)
	}
}

func must3(t *testing.T) func(string, diagram.AccessToken, error) (string, diagram.AccessToken) {
	return func(s string, tok diagram.AccessToken, err error) (string, diagram.AccessToken) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return s, tok
	}
}
