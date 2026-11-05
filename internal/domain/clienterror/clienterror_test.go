package clienterror

import (
	"strings"
	"testing"
)

func TestScrub(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	for name, c := range map[string]struct{ in, want string }{
		"email":       {"user ali.b+x@example.com failed", "user [redacted] failed"},
		"jwt":         {"token " + jwt + " bad", "token [redacted] bad"},
		"bearer":      {"Authorization: Bearer abc.def-123_x=", "Authorization: [redacted]"},
		"hex":         {"id 0123456789abcdef0123456789abcdef0123 end", "id [redacted] end"},
		"base64":      {"key QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo0MTIz== end", "key [redacted] end"},
		"short kept":  {"abc123 and 0123456789abcdef", "abc123 and 0123456789abcdef"},
		"long word":   {"ThisIsAVeryLongIdentifierWithoutAnyDigitsAtAll stays", "ThisIsAVeryLongIdentifierWithoutAnyDigitsAtAll stays"},
		"query":       {"GET https://x.test/a/b?token=1&u=2 failed", "GET https://x.test/a/b failed"},
		"fragment":    {"at https://x.test/p#access_token=zzz", "at https://x.test/p"},
		"frame query": {"at f (https://x.test/a.js?v=3:10:5)", "at f (https://x.test/a.js:10:5)"},
		"ext url":     {"chrome-extension://abc/x.js?y=1", "chrome-extension://abc/x.js"},
		"plain":       {"Cannot read properties of undefined (reading 'x')", "Cannot read properties of undefined (reading 'x')"},
	} {
		if got := Scrub(c.in); got != c.want {
			t.Errorf("%s: Scrub(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

func TestFingerprintStable(t *testing.T) {
	msg := "Cannot read properties of undefined"
	a := "TypeError: x\n    at render (https://x.test/assets/index-Dk3jf92K.js:10:200)\n    at run (https://x.test/assets/vendor-AbCd1234.js:5:7)\n    at z (https://x.test/assets/index-Dk3jf92K.js:1:1)\n    at deep (https://x.test/other.js:9:9)"
	b := "TypeError: x\n    at render (https://x.test/assets/index-Zq81PmLw.js:99:12)\n    at run (https://x.test/assets/vendor-QwEr5678.js:55:70)\n    at z (https://x.test/assets/index-Zq81PmLw.js:3:4)\n    at other (https://x.test/different.js:1:1)"
	if Fingerprint(msg, a) != Fingerprint(msg, b) {
		t.Error("new build or line numbers changed the fingerprint")
	}
	if Fingerprint(msg, a) == Fingerprint("other message", a) {
		t.Error("different message shares a fingerprint")
	}
	c := strings.Replace(a, "render", "paint", 1)
	if Fingerprint(msg, a) == Fingerprint(msg, c) {
		t.Error("different frame shares a fingerprint")
	}
}

func TestNew(t *testing.T) {
	e, err := New(Input{Message: " boom for me@x.com ", Stack: "at f (https://x.test/a.js?t=1:1:2)",
		PageURL: "https://x.test/p?q=1#h", UserAgent: strings.Repeat("u", 900)})
	if err != nil {
		t.Fatal(err)
	}
	if e.Message != "boom for [redacted]" || e.PageURL != "https://x.test/p" || len(e.UserAgent) != 500 ||
		strings.Contains(e.Stack, "t=1") || e.Fingerprint == "" || e.Status != StatusOpen {
		t.Errorf("entry = %+v", e)
	}
	// The same error from two emails groups together: scrubbing runs before the hash.
	a, _ := New(Input{Message: "bad a@x.com"})
	b, _ := New(Input{Message: "bad b@y.org"})
	if a.Fingerprint != b.Fingerprint {
		t.Error("emails changed the fingerprint")
	}
	for _, in := range []Input{{}, {Message: "  \n"}} {
		if _, err := New(in); err == nil {
			t.Errorf("%+v: want error", in)
		}
	}
	long, _ := New(Input{Message: strings.Repeat("é", 3000), Stack: strings.Repeat("s ", 9000), PageURL: strings.Repeat("p", 3000)})
	if n := len([]rune(long.Message)); n != 1000 || len(long.Stack) > 8000 || len(long.PageURL) != 2000 {
		t.Errorf("not cut: %d %d %d", n, len(long.Stack), len(long.PageURL))
	}
}
