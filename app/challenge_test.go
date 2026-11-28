package app_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type challengeView struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	DaysDone int    `json:"days_done"`
	PostIDs  []int  `json:"post_ids"`
	Stats    struct {
		DaysDone      int `json:"days_done"`
		CurrentStreak int `json:"current_streak"`
		Today         int `json:"today"`
	} `json:"stats"`
	Entries []struct {
		N      int `json:"n"`
		PostID int `json:"post_id"`
	} `json:"entries"`
}

type challengeList struct {
	Items      []challengeView `json:"items"`
	Total      int             `json:"total"`
	NextCursor string          `json:"next_cursor"`
}

func TestChallengeSocial(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")
	gani := anon.register("gani")

	create := func(title, visibility string) int {
		var c challengeView
		ali.do("POST", "/api/challenges", map[string]any{"title": title, "days": 30, "visibility": visibility, "timezone": "UTC"}, 201, &c)
		return c.ID
	}
	pub, priv := create("public", "public"), create("private", "private")
	path := fmt.Sprintf("/api/challenges/%d", pub)
	privPath := fmt.Sprintf("/api/challenges/%d", priv)

	anon.do("POST", path+"/follow", nil, 401, nil)
	anon.do("POST", path+"/cheer", nil, 401, nil)
	anon.do("GET", "/api/challenges?tab=following", nil, 401, nil)
	ali.do("POST", path+"/follow", nil, 400, nil)
	ali.do("POST", path+"/cheer", nil, 400, nil)
	vali.do("POST", privPath+"/follow", nil, 404, nil)
	vali.do("POST", privPath+"/cheer", nil, 404, nil)
	vali.do("POST", "/api/challenges/99999/follow", nil, 404, nil)

	gani.do("POST", "/api/users/ali/block", nil, 204, nil)
	gani.do("POST", path+"/follow", nil, 400, nil)
	gani.do("POST", path+"/cheer", nil, 400, nil)

	vali.do("POST", path+"/follow", nil, 204, nil)
	vali.do("POST", path+"/follow", nil, 204, nil)
	var cheers struct {
		CheersToday int  `json:"cheers_today"`
		Cheered     bool `json:"cheered"`
	}
	for range 2 {
		vali.do("POST", path+"/cheer", nil, 200, &cheers)
		if cheers.CheersToday != 1 || !cheers.Cheered {
			t.Fatalf("cheers = %+v", cheers)
		}
	}
	var detail map[string]any
	vali.do("GET", path, nil, 200, &detail)
	if detail["followers"] != 1.0 || detail["is_following"] != true || detail["cheered_today"] != true || detail["remind_at"] != nil {
		t.Errorf("follower detail = %v", detail)
	}
	var list challengeList
	vali.do("GET", "/api/challenges?tab=following", nil, 200, &list)
	if list.Total != 1 || list.Items[0].ID != pub {
		t.Errorf("following = %+v", list)
	}

	ali.do("PATCH", path, map[string]any{"remind_at": "25:00"}, 400, nil)
	ali.do("PATCH", path, map[string]any{"remind_at": "00:00"}, 200, nil)
	detail = nil
	ali.do("GET", path, nil, 200, &detail)
	if detail["reminders"] != true || detail["remind_at"] != "00:00" {
		t.Errorf("owner detail = %v", detail)
	}

	count := func(c *client, typ string) int {
		var page struct {
			Items []struct {
				Type        string `json:"type"`
				ChallengeID int    `json:"challenge_id"`
			} `json:"items"`
		}
		c.do("GET", "/api/me/notifications?limit=50", nil, 200, &page)
		n := 0
		for _, i := range page.Items {
			if i.Type == typ && i.ChallengeID != 0 {
				n++
			}
		}
		return n
	}
	ctx := t.Context()
	must(t, e.app.Blog.ChallengeReminders(ctx))
	must(t, e.app.Blog.ChallengeReminders(ctx))
	if n := count(ali, "challenge_nudge"); n != 1 {
		t.Errorf("nudges = %d, want 1", n)
	}

	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "Day", "body": strings.Repeat("a", 300), "status": "published"}, 201, &p)
	link := fmt.Sprintf("%s/posts/%d", path, p.ID)
	ali.do("PUT", link, nil, 204, nil)
	ali.do("PUT", link, nil, 204, nil)
	if n := count(vali, "challenge_day"); n != 1 {
		t.Errorf("follower day notifications = %d, want 1", n)
	}
	if n := count(ali, "challenge_praise"); n != 1 {
		t.Errorf("praise = %d, want 1", n)
	}

	vali.do("DELETE", path+"/follow", nil, 204, nil)
	vali.do("DELETE", path+"/follow", nil, 204, nil)
	vali.do("GET", "/api/challenges?tab=following", nil, 200, &list)
	if list.Total != 0 {
		t.Errorf("following after unfollow = %+v", list)
	}
}

func TestChallengeAPI(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")

	input := func(title, visibility string) map[string]any {
		return map[string]any{"title": title, "days": 30, "visibility": visibility, "timezone": "Asia/Tashkent"}
	}
	anon.do("POST", "/api/challenges", input("x", "public"), 401, nil)
	for _, bad := range []map[string]any{
		{"title": "x", "days": 30, "timezone": "Mars/Olympus"},
		{"title": "x", "days": 30},
		{"title": "x", "days": 6, "timezone": "UTC"},
		{"title": "x", "days": 366, "timezone": "UTC"},
	} {
		ali.do("POST", "/api/challenges", bad, 400, nil)
	}

	var pub, priv, third challengeView
	ali.do("POST", "/api/challenges", input("100 days of code", "public"), 201, &pub)
	ali.do("POST", "/api/challenges", input("secret", "private"), 201, &priv)
	ali.do("POST", "/api/challenges", input("third", "public"), 201, &third)
	ali.do("POST", "/api/challenges", input("fourth", "public"), 400, nil)

	path := fmt.Sprintf("/api/challenges/%d", pub.ID)
	privPath := fmt.Sprintf("/api/challenges/%d", priv.ID)
	anon.do("GET", privPath, nil, 404, nil)
	vali.do("GET", privPath, nil, 404, nil)
	vali.do("PATCH", privPath, map[string]any{"title": "x"}, 404, nil)
	ali.do("GET", privPath, nil, 200, nil)
	anon.login("admin", "admin-pass-123").do("GET", privPath, nil, 200, nil)

	var list challengeList
	anon.do("GET", "/api/challenges?limit=1", nil, 200, &list)
	if list.Total != 2 || len(list.Items) != 1 || list.Items[0].ID != third.ID || list.NextCursor == "" {
		t.Fatalf("active page 1 = %+v", list)
	}
	var next challengeList
	anon.do("GET", "/api/challenges?limit=1&cursor="+list.NextCursor, nil, 200, &next)
	if len(next.Items) != 1 || next.Items[0].ID != pub.ID || next.NextCursor != "" {
		t.Fatalf("active page 2 = %+v", next)
	}
	anon.do("GET", "/api/challenges?tab=mine", nil, 401, nil)
	anon.do("GET", "/api/challenges?tab=nope", nil, 400, nil)
	ali.do("GET", "/api/challenges?tab=mine", nil, 200, &list)
	if list.Total != 3 {
		t.Errorf("mine = %+v", list)
	}

	vali.do("PATCH", path, map[string]any{"title": "mine now"}, 403, nil)
	ali.do("PATCH", path, map[string]any{"title": ""}, 400, nil)
	var updated challengeView
	ali.do("PATCH", path, map[string]any{"title": "Go", "days": 9, "timezone": "UTC"}, 200, &updated)
	if updated.Title != "Go" {
		t.Errorf("updated = %+v", updated)
	}

	long := strings.Repeat("a", 300)
	newPost := func(c *client, body, status string) post {
		var p post
		c.do("POST", "/api/posts", map[string]any{"title": "Day", "body": body, "status": status}, 201, &p)
		return p
	}
	link := func(challengeID, postID int) string {
		return fmt.Sprintf("/api/challenges/%d/posts/%d", challengeID, postID)
	}
	others := newPost(vali, long, "published")
	ali.do("PUT", link(pub.ID, others.ID), nil, 400, nil)
	ali.do("PUT", link(pub.ID, 99999), nil, 404, nil)
	draft := newPost(ali, long, "draft")
	short := newPost(ali, long[1:], "published")
	vali.do("PUT", link(pub.ID, draft.ID), nil, 403, nil)
	ali.do("PUT", link(pub.ID, draft.ID), nil, 204, nil)
	ali.do("PUT", link(pub.ID, draft.ID), nil, 204, nil)
	ali.do("PUT", link(third.ID, draft.ID), nil, 409, nil)
	ali.do("PUT", link(pub.ID, short.ID), nil, 204, nil)

	var got challengeView
	ali.do("GET", path, nil, 200, &got)
	if got.DaysDone != 0 || got.Stats.DaysDone != 0 || len(got.Entries) != 0 || got.Stats.Today != 1 {
		t.Fatalf("draft and short counted: %+v", got)
	}
	ali.do("GET", fmt.Sprintf("/api/posts/%d/challenge", short.ID), nil, 404, nil)

	var running []challengeView
	ali.do("GET", "/api/me/challenges", nil, 200, &running)
	if len(running) != 3 || len(running[2].PostIDs) != 2 {
		t.Errorf("running = %+v", running)
	}

	postPath := fmt.Sprintf("/api/posts/%d", draft.ID)
	check := func(done int) {
		t.Helper()
		got = challengeView{}
		anon.do("GET", path, nil, 200, &got)
		if got.DaysDone != done || got.Stats.DaysDone != done || len(got.Entries) != done || got.Stats.CurrentStreak != done {
			t.Fatalf("done != %d: %+v", done, got)
		}
	}
	ali.do("PUT", postPath, map[string]any{"status": "published"}, 200, nil)
	check(1)
	if got.Entries[0].N != 1 || got.Entries[0].PostID != draft.ID {
		t.Errorf("entry = %+v", got.Entries[0])
	}
	var place struct {
		Day int `json:"day"`
		Of  int `json:"of"`
	}
	anon.do("GET", postPath+"/challenge", nil, 200, &place)
	if place.Day != 1 || place.Of != 30 {
		t.Errorf("place = %+v", place)
	}
	ali.do("PUT", postPath, map[string]any{"status": "draft"}, 200, nil)
	check(0)
	ali.do("PUT", postPath, map[string]any{"status": "published"}, 200, nil)
	check(1)
	ali.do("DELETE", postPath, nil, 204, nil)
	check(0)

	ali.do("DELETE", link(pub.ID, short.ID), nil, 204, nil)
	ali.do("DELETE", link(pub.ID, short.ID), nil, 404, nil)
	vali.do("DELETE", path, nil, 403, nil)
	ali.do("DELETE", path, nil, 204, nil)
	anon.do("GET", path, nil, 404, nil)
}

func TestCertificateAPI(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")
	admin := anon.login("admin", "admin-pass-123")

	var c challengeView
	ali.do("POST", "/api/challenges", map[string]any{"title": "30 days", "days": 30, "visibility": "private", "timezone": "UTC"}, 201, &c)
	path := fmt.Sprintf("/api/challenges/%d", c.ID)
	claim := path + "/certificate"
	anon.do("POST", claim, nil, 401, nil)
	vali.do("POST", claim, nil, 404, nil)
	admin.do("POST", claim, nil, 403, nil)
	ali.do("POST", claim, nil, 400, nil)

	// Move the challenge into the past with one post on each of its days.
	ctx := t.Context()
	db, err := pgx.Connect(ctx, e.dsn)
	must(t, err)
	defer db.Close(context.Background())
	_, err = db.Exec(ctx, `WITH made AS (
			INSERT INTO posts (user_id, title, slug, body, status, published_at)
			SELECT $2, 'Day', 'cert-day-' || i, repeat('a', 300), 'published', now() - make_interval(days => 31 - i)
			FROM generate_series(0, 29) i RETURNING id)
		INSERT INTO challenge_posts (challenge_id, post_id) SELECT $1, id FROM made`, c.ID, ali.user.ID)
	must(t, err)
	_, err = db.Exec(ctx, `UPDATE challenges SET start_date = (now() AT TIME ZONE 'UTC')::date - 31 WHERE id = $1`, c.ID)
	must(t, err)

	type certificate struct {
		ID           string `json:"id"`
		ChallengeID  *int   `json:"challenge_id"`
		HolderName   string `json:"holder_name"`
		DaysDone     int    `json:"days_done"`
		RevokedAt    string `json:"revoked_at"`
		RevokeReason string `json:"revoke_reason"`
	}
	var cert, again certificate
	ali.do("POST", claim, nil, 201, &cert)
	if len(cert.ID) != 16 || cert.HolderName != "ali" || cert.DaysDone != 30 || cert.ChallengeID != nil {
		t.Fatalf("certificate = %+v", cert)
	}
	ali.do("POST", claim, nil, 200, &again)
	if again.ID != cert.ID {
		t.Errorf("second claim = %+v", again)
	}
	var detail struct {
		CertificateID string `json:"certificate_id"`
	}
	ali.do("GET", path, nil, 200, &detail)
	if detail.CertificateID != cert.ID {
		t.Errorf("detail = %+v", detail)
	}

	verify := "/api/certificates/" + cert.ID
	anon.do("GET", verify, nil, 200, nil)
	anon.do("GET", "/api/certificates/aaaaaaaaaaaaaaaa", nil, 404, nil)
	anon.do("GET", "/api/admin/certificates", nil, 401, nil)
	ali.do("GET", "/api/admin/certificates", nil, 403, nil)
	var list struct {
		Items []certificate `json:"items"`
		Total int           `json:"total"`
	}
	admin.do("GET", "/api/admin/certificates", nil, 200, &list)
	if list.Total != 1 || list.Items[0].ID != cert.ID {
		t.Errorf("list = %+v", list)
	}
	revoke := "/api/admin/certificates/" + cert.ID + "/revoke"
	ali.do("POST", revoke, map[string]any{"reason": "x"}, 403, nil)
	admin.do("POST", revoke, map[string]any{"reason": " "}, 400, nil)
	admin.do("POST", revoke, map[string]any{"reason": "copied posts"}, 200, nil)
	admin.do("POST", revoke, map[string]any{"reason": "again"}, 400, nil)
	var revoked certificate
	anon.do("GET", verify, nil, 200, &revoked) // the cached response is gone
	if revoked.RevokedAt == "" || revoked.RevokeReason != "copied posts" {
		t.Errorf("revoked = %+v", revoked)
	}
	ali.do("POST", claim, nil, 200, &again)
	if again.ID != cert.ID || again.RevokedAt == "" {
		t.Errorf("reissued = %+v", again)
	}
	ali.do("DELETE", path, nil, 204, nil)
	anon.do("GET", verify, nil, 200, nil)
}
