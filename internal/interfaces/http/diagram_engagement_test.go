package http

// Wire format of claps and bookmarks on published diagrams.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/diagramtest"
	"github.com/iBlog/iblog-monolith-go/internal/domain/social"
)

func TestDiagramClapsAndBookmarksWire(t *testing.T) {
	notices := diagramtest.NewNotices()
	var calls atomic.Int32
	e := &blogEnv{t: t, audit: &auditLog{}}
	e.srv, e.mem = newDiagramServerWith(t, func(d *application.Diagrams, deps *Deps) {
		d.Notices = notices
		deps.Limit = func(_ context.Context, key string, n int, _ time.Duration) (bool, time.Duration) {
			if strings.HasPrefix(key, "like:user:3") && calls.Add(1) > 2 {
				return false, time.Second
			}
			return true, 0
		}
	})
	e.mem.SetUser(1, "alice")
	_, fid := e.project(1, "Checkout")
	pub := DiagramPrefix + "/files/" + fid + "/publish"
	slug, _ := e.want(e.do(1, "POST", pub, map[string]any{"visibility": "public", "title": "Checkout flow"}), 200).Body["slug"].(string)
	base := DiagramPrefix + "/public/" + slug

	// anonymous: 401 on writes, zeros on /me
	e.problem(e.do(0, "POST", base+"/clap", map[string]any{"count": 1}), 401)
	e.problem(e.do(0, "POST", base+"/bookmark", nil), 401)
	e.problem(e.do(0, "GET", "/api/me/diagram-bookmarks", nil), 401)
	if d := e.want(e.do(0, "GET", base+"/me", nil), 200).data(); d["my_claps"] != float64(0) || d["bookmarked"] != false {
		t.Errorf("anon me = %v", d)
	}

	// validation and unknown slugs
	e.problem(e.do(2, "POST", base+"/clap", map[string]any{"count": 0}), 400)
	e.problem(e.do(2, "POST", base+"/clap", map[string]any{"count": 51}), 400)
	e.problem(e.do(2, "POST", DiagramPrefix+"/public/nope-123/clap", map[string]any{"count": 1}), 404)
	e.problem(e.do(2, "POST", DiagramPrefix+"/public/nope-123/bookmark", nil), 404)

	// claps add up and cap at 50 per reader
	if d := e.want(e.do(2, "POST", base+"/clap", map[string]any{"count": 30}), 200).data(); d["claps"] != float64(30) || d["my_claps"] != float64(30) {
		t.Errorf("clap = %v", d)
	}
	if d := e.want(e.do(2, "POST", base+"/clap", map[string]any{"count": 30}), 200).data(); d["claps"] != float64(50) || d["my_claps"] != float64(50) {
		t.Errorf("capped clap = %v", d)
	}
	e.want(e.do(1, "POST", base+"/clap", map[string]any{"count": 5}), 200) // the author may clap, but is not told
	if d := e.want(e.do(2, "GET", base+"/me", nil), 200).data(); d["my_claps"] != float64(50) || d["bookmarked"] != false {
		t.Errorf("me = %v", d)
	}
	if cc := e.do(2, "GET", base+"/me", nil).Header.Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("me Cache-Control = %q", cc)
	}

	// one notification, to the author, on the first clap only
	var got []social.Notification
	for _, n := range notices.All() {
		got = append(got, n)
	}
	if len(got) != 1 || got[0].Type != social.NotifyDiagramClap || got[0].UserID != 1 || got[0].ActorID != 2 || got[0].DiagramFileID != fid {
		t.Errorf("notifications = %+v", got)
	}

	// counts in public reads
	if d := e.want(e.do(0, "GET", base, nil), 200).data(); d["claps"] != float64(55) {
		t.Errorf("page claps = %v", d["claps"])
	}
	if l := e.want(e.do(0, "GET", DiagramPrefix+"/public", nil), 200).list(); len(l) != 1 || l[0].(map[string]any)["claps"] != float64(55) {
		t.Errorf("list = %v", l)
	}

	// bookmarks: idempotent, listed, removable
	e.want(e.do(2, "POST", base+"/bookmark", nil), 204)
	e.want(e.do(2, "POST", base+"/bookmark", nil), 204)
	if d := e.want(e.do(2, "GET", base+"/me", nil), 200).data(); d["bookmarked"] != true {
		t.Errorf("me = %v", d)
	}
	r := e.want(e.do(2, "GET", "/api/me/diagram-bookmarks", nil), 200)
	if l := r.list(); len(l) != 1 || l[0].(map[string]any)["slug"] != slug || r.meta()["total"] != float64(1) {
		t.Errorf("bookmarks = %s", r.Raw)
	}
	if l := e.want(e.do(1, "GET", "/api/me/diagram-bookmarks", nil), 200).list(); len(l) != 0 {
		t.Errorf("other reader's bookmarks = %v", l)
	}
	e.want(e.do(2, "DELETE", base+"/bookmark", nil), 204)
	e.want(e.do(2, "DELETE", base+"/bookmark", nil), 204)
	if l := e.want(e.do(2, "GET", "/api/me/diagram-bookmarks", nil), 200).list(); len(l) != 0 {
		t.Errorf("after unbookmark = %v", l)
	}

	// rate limit like posts
	e.want(e.do(3, "POST", base+"/clap", map[string]any{"count": 1}), 200)
	e.want(e.do(3, "POST", base+"/clap", map[string]any{"count": 1}), 200)
	e.problem(e.do(3, "POST", base+"/clap", map[string]any{"count": 1}), 429)

	// unpublished: not found, bookmark vanishes from the list
	e.want(e.do(2, "POST", base+"/bookmark", nil), 204)
	e.want(e.do(1, "DELETE", pub, nil), 204)
	e.problem(e.do(2, "POST", base+"/clap", map[string]any{"count": 1}), 404)
	e.problem(e.do(2, "POST", base+"/bookmark", nil), 404)
	if l := e.want(e.do(2, "GET", "/api/me/diagram-bookmarks", nil), 200).list(); len(l) != 0 {
		t.Errorf("unpublished still listed = %v", l)
	}
}
