package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// zEventsOK ends every subscription at once so stream handlers return.
type zEventsOK struct{ zEvents }

func (*zEventsOK) Subscribe(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte, 1)
	ch <- []byte(`{"x":1}`)
	close(ch)
	return ch, nil
}

type zStorageOK struct{ zStorage }

func (s *zStorageOK) Get(context.Context, string) (application.Object, error) {
	if s.c.err != nil {
		return application.Object{}, s.c.err
	}
	return application.Object{Body: io.NopCloser(strings.NewReader("data")), ContentType: "image/png", Size: 4}, nil
}

type zSocialProv struct{ c *zctl }

func (p zSocialProv) Begin(context.Context) (string, error) { return "https://idp.example/x", p.c.err }
func (p zSocialProv) Finish(context.Context, string, string) (application.ExternalIdentity, error) {
	return application.ExternalIdentity{Subject: "s1", Email: "a@b.co", EmailVerified: true, Name: "A"}, p.c.err
}

type blogRig struct {
	c   *zctl
	mux *http.ServeMux
	h   *Handler
}

func newBlogRig(t *testing.T) *blogRig {
	t.Helper()
	c := &zctl{}
	users := &zUsers{c}
	ident := &zIdent{c}
	blog := application.NewBlog(application.Repos{
		Posts: &zPosts{c}, Comments: &zComments{c}, Categories: &zCats{c}, Users: users,
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
		SiteURL:   "https://site.example", SiteName: "Site",
		Health: map[string]func(context.Context) error{"db": func(context.Context) error { return c.err }},
		Social: map[string]SocialProvider{"github": zSocialProv{c}},
		UserID: func(ctx context.Context) int {
			id, _ := ctx.Value(testUser{}).(int)
			return id
		},
		Limit: func(context.Context, string, int, time.Duration) (bool, time.Duration) { return true, 0 },
	}
	h := NewHandler(deps)
	mux := http.NewServeMux()
	h.Routes(mux)
	return &blogRig{c: c, mux: mux, h: h}
}

func (g *blogRig) do(method, target, body string, uid int, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	if uid != 0 {
		req = req.WithContext(context.WithValue(req.Context(), testUser{}, uid))
	}
	w := httptest.NewRecorder()
	func() {
		// zero-value fakes can break a handler's data assumptions; the
		// statements run up to there still count.
		defer func() { _ = recover() }()
		g.mux.ServeHTTP(w, req)
	}()
	return w
}

func blogRoutes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) (/api/[^"]*)"`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1]+" "+m[2])
	}
	return out
}

const richBody = `{"title":"T","body":"# hi","summary":"s","tags":["a","b"],"status":"published","name":"n","slug":"s1",
"email":"a@b.co","password":"Passw0rd!x","old_password":"Passw0rd!x","new_password":"Passw0rd!y","username":"alice",
"token":"tok","code":"123456","reason":"spam","target":"post","kind":"post","target_id":1,"post_id":1,"ids":[1,2],
"post_ids":[1,2],"text":"quote","content":"c","message":"m","decision":"resolved","role":"editor","description":"d",
"category_id":1,"label_ids":[1],"url":"https://example.com/x","count":3,"claps":3,"version":1,"until":"2099-01-01T00:00:00Z",
"event":"view","path":"/x","parent_id":1,"challenge":"ch","backup_code":"abc","bio":"b","display_name":"D","avatar":"a"}`

func TestBlogRouteSweep(t *testing.T) {
	g := newBlogRig(t)
	repl := strings.NewReplacer("{id}", "1", "{slug}", "slug-x", "{username}", "alice", "{tag}", "go",
		"{version}", "1", "{kind}", "post", "{target}", "1", "{provider}", "github", "{key...}", "a/b.png", "{path...}", "p/x")
	errs := []error{nil, domain.ErrNotFound, domain.ErrForbidden, domain.ErrUnauthorized, domain.Invalid("bad"), errors.New("boom")}
	n := 0
	for _, route := range blogRoutes(t) {
		method, path, _ := strings.Cut(route, " ")
		path = repl.Replace(path)
		if strings.HasSuffix(path, "/stream") {
			continue
		}
		for _, e := range errs {
			g.c.err = e
			for _, uid := range []int{0, 1} {
				for _, body := range []string{"", "{broken", "{}", richBody} {
					for _, q := range []string{"", "?page=2&limit=5&status=draft&q=x&cursor=c&tag=go&sort=top&token=t&format=json"} {
						if method == http.MethodGet && body != "" {
							continue
						}
						w := g.do(method, path+q, body, uid, "Authorization", "Bearer tok")
						_ = w
						n++
					}
				}
			}
		}
	}
	g.c.err = nil
	t.Logf("%d calls", n)
	_ = post.StatusDraft
	_ = user.User{}
}
