package diagram

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateTokenShape(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		tok, hash, prefix := GenerateToken()
		if !TokenLooksValid(tok) || len(tok) != 40 || !strings.HasPrefix(tok, TokenPrefix) || prefix != "sdd_pat_" {
			t.Fatalf("token %q prefix %q", tok, prefix)
		}
		if hash != HashToken(tok) || len(hash) != 64 || strings.Contains(hash, tok) || seen[tok] {
			t.Fatalf("hash %q", hash)
		}
		seen[tok] = true
	}
}

func TestParseBearer(t *testing.T) {
	tok, _, _ := GenerateToken()
	for header, want := range map[string]string{
		"Bearer " + tok:         tok,
		"bearer   " + tok + " ": tok,
		"Basic " + tok:          "",
		"Bearer " + tok + "x":   "",
		"Bearer gk_abc":         "",
		"Bearer sdd_pat_short":  "",
		tok:                     "",
		"":                      "",
	} {
		if got := ParseBearer(header); got != want {
			t.Errorf("ParseBearer(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestAccessTokenRules(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	tok := AccessToken{Scopes: []string{ScopeFilesRead}, ProjectIDs: []string{"prj_a"}}
	if !tok.Allows(ScopeFilesRead) || tok.Allows(ScopeFilesWrite) || tok.Allows("") {
		t.Error("scopes")
	}
	if !tok.Reaches("prj_a") || tok.Reaches("prj_b") || !(AccessToken{}).Reaches("anything") {
		t.Error("project limit: nil reaches all, a list only itself")
	}
	if (AccessToken{ProjectIDs: []string{}}).Reaches("prj_a") {
		t.Error("an empty list reaches nothing")
	}
	if !tok.Usable(now) || (AccessToken{ExpiresAt: &past}).Usable(now) || !(AccessToken{ExpiresAt: &future}).Usable(now) || (AccessToken{RevokedAt: &past}).Usable(now) {
		t.Error("usable")
	}
}

// The signature scheme is the Node server's: sha256= + hex HMAC-SHA256 of
// "<timestamp>.<body>".
func TestSignature(t *testing.T) {
	got := Sign("whsec_test", 1700000000, []byte(`{"a":1}`))
	// computed with the Node server's createHmac('sha256', secret).update(`${ts}.${body}`)
	if want := "sha256=38877139021993b830af32feea6e18a8da83eb2f6e49ee50bd9e4cf4ca4d3789"; got != want {
		t.Fatalf("Sign = %q, want the Node server's %q", got, want)
	}
	if !strings.HasPrefix(got, "sha256=") || len(got) != len("sha256=")+64 {
		t.Fatalf("Sign = %q ", got)
	}
	if got != Sign("whsec_test", 1700000000, []byte(`{"a":1}`)) || got == Sign("whsec_test", 1700000001, []byte(`{"a":1}`)) || got == Sign("whsec_other", 1700000000, []byte(`{"a":1}`)) {
		t.Error("the signature must depend on secret, timestamp and body, and be stable")
	}
	now := time.Unix(1700000100, 0)
	if !VerifySignature("whsec_test", 1700000000, []byte(`{"a":1}`), got, now, 0) {
		t.Error("valid signature rejected")
	}
	if VerifySignature("whsec_test", 1700000000, []byte(`{"a":2}`), got, now, 0) || VerifySignature("whsec_test", 1700000000, []byte(`{"a":1}`), "sha256=00", now, 0) {
		t.Error("tampered body or signature accepted")
	}
	if VerifySignature("whsec_test", 1700000000, []byte(`{"a":1}`), got, now.Add(time.Hour), 0) {
		t.Error("a stale timestamp must be refused when a clock is given")
	}
	if !VerifySignature("whsec_test", 1700000000, []byte(`{"a":1}`), got, time.Time{}, 0) {
		t.Error("no clock: only the signature is checked")
	}
}

func TestRetryDelayAndStatus(t *testing.T) {
	want := []time.Duration{10 * time.Second, time.Minute, 6 * time.Minute, 36 * time.Minute}
	for i, w := range want {
		if d, ok := RetryDelay(i+1, 0); !ok || d != w {
			t.Errorf("after attempt %d: %v %v, want %v", i+1, d, ok, w)
		}
	}
	for _, n := range []int{0, 5, 6} {
		if _, ok := RetryDelay(n, 0); ok {
			t.Errorf("attempt %d must not be retried", n)
		}
	}
	if lo, _ := RetryDelay(2, -1); lo != 48*time.Second {
		t.Errorf("-20%% jitter = %v", lo)
	}
	if hi, _ := RetryDelay(2, 5); hi != 72*time.Second {
		t.Errorf("jitter is clamped to +20%%: %v", hi)
	}
	for status, want := range map[int]string{200: StatusOK, 204: StatusOK, 299: StatusOK, 301: StatusFail, 400: StatusFail, 404: StatusFail,
		408: StatusRetry, 429: StatusRetry, 500: StatusRetry, 503: StatusRetry} {
		if got := ClassifyStatus(status); got != want {
			t.Errorf("ClassifyStatus(%d) = %q, want %q", status, got, want)
		}
	}
}
