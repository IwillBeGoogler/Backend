package application

import (
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

func TestBlogPureHelpers(t *testing.T) {
	if truncate("  short ", 20) != "short" || truncate("héllo wörld", 2) != "h" || truncate("héllo", 3) != "hé" {
		t.Errorf("truncate: %q %q", truncate("héllo wörld", 2), truncate("héllo", 3))
	}
	if !utf8RuneStart('a') || utf8RuneStart(0x80) {
		t.Error("utf8RuneStart")
	}
	b := &Blog{UnsubscribeKey: []byte("k"), APIURL: "https://api.test"}
	u := b.unsubscribeURL(1, 2)
	if !strings.HasPrefix(u, "https://api.test/api/unsubscribe?s=1&a=2&sig=") {
		t.Errorf("url = %s", u)
	}
	if b.unsubscribeSig(1, 2) == b.unsubscribeSig(2, 1) {
		t.Error("signature ignores order")
	}
	if (&Blog{}).unsubscribeURL(1, 2) != "" {
		t.Error("url without key")
	}
	if err := (&Blog{}).UnsubscribeByLink(ctx, 1, 2, "x"); err == nil {
		t.Error("link without key accepted")
	}
	if err := b.UnsubscribeByLink(ctx, 1, 2, "wrong"); err == nil {
		t.Error("forged link accepted")
	}
}

func TestSSOHelpers(t *testing.T) {
	s, err := randomSecret()
	if err != nil || len(s) != 64 {
		t.Errorf("randomSecret = %q %v", s, err)
	}
	for range 50 {
		if n := randInt(7); n < 0 || n >= 7 {
			t.Fatalf("randInt = %d", n)
		}
	}
	for in, want := range map[[2]string]string{
		{"Ada Lovelace", "a@b.c"}:            "Ada_Lovelace",
		{"!!", "bob@x.com"}:                  "bob",
		{"", "a@x.com"}:                      "auu",
		{strings.Repeat("a", 40), "a@x.com"}: strings.Repeat("a", 28),
	} {
		if got := externalUsername(in[0], in[1]); got != want {
			t.Errorf("externalUsername(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	e := &SSOError{Code: "failed", Msg: "nope"}
	if e.Error() != "nope" {
		t.Error("SSOError")
	}
	if len(hashCodes([]string{"a", "b"})) != 2 {
		t.Error("hashCodes")
	}
}

func TestSecurityEmail(t *testing.T) {
	jobs := &fakeJobs{}
	a := &Accounts{blog: &Blog{Ports: Ports{Jobs: jobs}}}
	a.securityEmail(ctx, user.User{ID: 1, Username: "ali"}, "s", "t") // no address: nothing
	a.securityEmail(ctx, user.User{ID: 1, Username: "ali", Email: "a@x.test"}, "subject", "body")
	if len(jobs.mail) != 1 || !strings.Contains(jobs.mail[0].Text, "Hi ali") {
		t.Errorf("mail = %+v", jobs.mail)
	}
}
