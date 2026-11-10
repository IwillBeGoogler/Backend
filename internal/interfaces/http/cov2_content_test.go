package http

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/captcha"
)

type richPosts struct{ zPosts }

func (f *richPosts) List(_ context.Context, _ post.Filter) (post.Page, error) {
	if f.c.err != nil {
		return post.Page{}, f.c.err
	}
	now := time.Now()
	return post.Page{Items: []post.Post{
		{ID: 1, Title: "A", Slug: "a", Author: "alice", CreatedAt: now, PublishedAt: &now},
		{ID: 2, Title: "B", Slug: "b", Subtitle: "sub", CreatedAt: now},
	}, Total: 2, Page: 1, Limit: 100}, nil
}

func (f *richPosts) GetBySlug(_ context.Context, slug string, _ int) (post.Post, error) {
	if f.c.err != nil {
		return post.Post{}, f.c.err
	}
	return post.Post{ID: 1, Title: "T", Slug: slug, Author: "alice", Body: "hello **world**", Tags: []string{"go"}, CanonicalURL: "https://x.example/c", CreatedAt: time.Now()}, nil
}

type richUsers struct{ zUsers }

func (f *richUsers) Profile(_ context.Context, name string, _ int) (user.Profile, error) {
	if f.c.err != nil {
		return user.Profile{}, f.c.err
	}
	return user.Profile{ID: 3, Username: name, AvatarURL: "/a.png"}, nil
}

func newRichRig(t *testing.T, tweak func(*Deps)) *blogRig {
	t.Helper()
	c := &zctl{}
	users := &richUsers{zUsers{c}}
	ident := &zIdent{c}
	blog := application.NewBlog(application.Repos{
		Posts: &richPosts{zPosts{c}}, Comments: &zComments{c}, Categories: &zCats{c}, Users: users,
		Social: &zSocial{c}, Reports: &zReports{c}, Feedback: &zFeedback{c}, ClientErrors: &zClientErrs{c},
		Publications: &zPubs{c}, Stats: &zStats{c}, Relations: &zRel{c}, Revisions: &zRev{c},
		Series: &zSeries{c}, Discovery: &zDisc{c}, Lists: &zLists{c},
	}, application.Ports{
		Authz: &zAuthz{c}, Audit: &zAudit{c}, Jobs: &zJobs{c}, Events: &zEventsOK{zEvents{c}},
		Views: &zViews{c}, Importer: &zImporter{c}, Diagrams: &zDiagMod{c},
	})
	acc := application.NewAccounts(users, ident, &zTokens{c}, &zTokens{c}, blog, "https://site.example")
	acc.MFA, acc.Challenges, acc.Secrets, acc.Sanctions, acc.Social = &zMFA{c}, &zChal{c}, &zSecret{c}, &zSanc{c}, &zSocLinks{c}
	deps := Deps{
		Blog: blog, Accounts: acc,
		Storage:   &zStorageOK{zStorage{c}},
		Jobs:      &zJobs{c},
		Events:    &zEventsOK{zEvents{c}},
		Tickets:   &zTokens{c},
		Analytics: application.NewAnalytics(&zAnaRepo{c}, &zAuthz{c}),
		SiteStats: application.NewSiteStats(&zSiteRepo{c}, &zViews{c}, nil, &zAuthz{c}),
		SiteURL:   "https://site.example",
		Social:    map[string]SocialProvider{"github": zSocialProv{c}},
		UserID: func(ctx context.Context) int {
			id, _ := ctx.Value(testUser{}).(int)
			return id
		},
		Limit: func(context.Context, string, int, time.Duration) (bool, time.Duration) { return true, 0 },
	}
	if tweak != nil {
		tweak(&deps)
	}
	h := NewHandler(deps)
	mux := http.NewServeMux()
	h.Routes(mux)
	return &blogRig{c: c, mux: mux, h: h}
}

func TestFeedsAndPrerender(t *testing.T) {
	g := newRichRig(t, nil)
	paths := []string{
		"/api/feed.xml", "/api/sitemap.xml", "/api/users/alice/feed.xml",
		"/api/prerender/", "/api/prerender/p/abc", "/api/prerender/tag/go",
		"/api/prerender/@alice", "/api/prerender/@alice/slug", "/api/prerender/zzz",
	}
	for _, e := range []error{nil, errors.New("boom")} {
		g.c.err = e
		for _, p := range paths {
			w := g.do(http.MethodGet, p, "", 0)
			if e == nil && w.Code != http.StatusOK && !strings.HasSuffix(p, "zzz") {
				t.Errorf("%s: %d", p, w.Code)
			}
		}
	}
	g.c.err = nil
	if w := g.do(http.MethodGet, "/api/feed.xml", "", 0); !strings.Contains(w.Body.String(), "/@alice/a") {
		t.Errorf("rss body: %s", w.Body.String())
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func multipartReq(t *testing.T, field string, data []byte) (*http.Request, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, err := mw.CreateFormFile(field, "f.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/uploads", &b)
	return req, mw.FormDataContentType()
}

func TestUploadCases(t *testing.T) {
	big := make([]byte, maxUpload+10)
	copy(big, pngBytes(t))
	cases := []struct {
		name  string
		field string
		data  []byte
		err   error
		uid   int
		want  int
	}{
		{"ok", "file", pngBytes(t), nil, 1, http.StatusCreated},
		{"anon", "file", pngBytes(t), nil, 0, http.StatusUnauthorized},
		{"nofile", "other", pngBytes(t), nil, 1, http.StatusBadRequest},
		{"toobig", "file", big, nil, 1, http.StatusBadRequest},
		{"notimage", "file", []byte("hello world text"), nil, 1, http.StatusBadRequest},
		{"badimage", "file", append([]byte("\x89PNG\r\n\x1a\n"), 1, 2, 3), nil, 1, http.StatusBadRequest},
		{"storefail", "file", pngBytes(t), errors.New("boom"), 1, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newRichRig(t, nil)
			req, ct := multipartReq(t, tc.field, tc.data)
			req.Header.Set("Content-Type", ct)
			if tc.uid != 0 {
				req = req.WithContext(context.WithValue(req.Context(), testUser{}, tc.uid))
			}
			if tc.name == "storefail" {
				g.h.Storage = &zStorage{&zctl{err: tc.err}}
			}
			w := httptest.NewRecorder()
			func() {
				defer func() { _ = recover() }()
				g.mux.ServeHTTP(w, req)
			}()
			if w.Code != tc.want && tc.name != "badimage" {
				t.Errorf("code %d want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	g := newRichRig(t, nil)
	if w := g.do(http.MethodPost, "/api/uploads", "x", 1, "Content-Type", "text/plain"); w.Code == http.StatusCreated {
		t.Error("non multipart accepted")
	}
	g.c.err = errors.New("jobs")
	g.h.Jobs = &zJobs{&zctl{err: errors.New("jobs")}}
	req, ct := multipartReq(t, "file", pngBytes(t))
	req.Header.Set("Content-Type", ct)
	req = req.WithContext(context.WithValue(req.Context(), testUser{}, 1))
	w := httptest.NewRecorder()
	func() {
		defer func() { _ = recover() }()
		g.mux.ServeHTTP(w, req)
	}()
}

func TestDownloadCases(t *testing.T) {
	g := newRichRig(t, nil)
	for _, p := range []string{"/api/uploads/a.png", "/api/uploads/..%2fx"} {
		g.do(http.MethodGet, p, "", 0)
	}
	g.h.Storage = &zStorage{&zctl{err: errors.New("boom")}}
	g.do(http.MethodGet, "/api/uploads/a.png", "", 0)
}

type flushRecorder struct{ *httptest.ResponseRecorder }

func (flushRecorder) SetWriteDeadline(time.Time) error { return nil }

func TestStreams(t *testing.T) {
	g := newRichRig(t, nil)
	for _, target := range []string{
		"/api/posts/1/comments/stream", "/api/me/notifications/stream", "/api/me/notifications/stream?ticket=t",
	} {
		for _, uid := range []int{0, 1} {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
			if uid != 0 {
				req = req.WithContext(context.WithValue(req.Context(), testUser{}, uid))
			}
			rec := flushRecorder{httptest.NewRecorder()}
			func() {
				defer func() { _ = recover() }()
				g.mux.ServeHTTP(rec, req)
			}()
		}
	}
	// Subscribe failure and unsupported streaming.
	g.c.err = errors.New("boom")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/me/notifications/stream", nil)
	req = req.WithContext(context.WithValue(req.Context(), testUser{}, 1))
	g.h.stream(flushRecorder{httptest.NewRecorder()}, req, "ch")
	g.c.err = nil
	g.h.stream(httptest.NewRecorder(), req, "ch")
	// Shutdown ends the stream.
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	g.h.stream(flushRecorder{httptest.NewRecorder()}, req.WithContext(ctx), "ch")
}

func TestHumanCheck(t *testing.T) {
	cases := []struct {
		name string
		fn   func(context.Context, string, string) error
		want int
	}{
		{"failed", func(context.Context, string, string) error { return captcha.ErrFailed }, http.StatusBadRequest},
		{"outage", func(context.Context, string, string) error { return errors.New("down") }, 0},
		{"ok", func(context.Context, string, string) error { return nil }, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newRichRig(t, func(d *Deps) { d.Captcha = tc.fn })
			body := `{"username":"alice","email":"a@b.co","password":"Passw0rd!x","captcha_token":"t"}`
			w := g.do(http.MethodPost, "/api/auth/register", body, 0)
			if tc.want != 0 && w.Code != tc.want {
				t.Errorf("code %d", w.Code)
			}
			g.do(http.MethodPost, "/api/feedback", `{"message":"hi there friend","captcha_token":"t"}`, 0)
		})
	}
	g := newRichRig(t, nil)
	if w := g.do(http.MethodPost, "/api/auth/register", `{"username":"alice","email":"a@b.co","password":"Passw0rd!x","website":"bot"}`, 0); w.Code != http.StatusBadRequest {
		t.Errorf("honeypot %d", w.Code)
	}
}
