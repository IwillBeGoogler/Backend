package mfa

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 SHA1 test secret "12345678901234567890", 8 digits there; we use
// the low 6 digits: T=59 -> 94287082 -> 287082.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestCodeRFC(t *testing.T) {
	got, err := Code(rfcSecret, Step(time.Unix(59, 0)))
	if err != nil || got != "287082" {
		t.Fatalf("got %q %v", got, err)
	}
	// lowercase secret accepted
	if g, _ := Code(strings.ToLower(rfcSecret), 1); g == "" {
		t.Fatal("lowercase")
	}
	if _, err := Code("!!!notbase32", 1); err == nil {
		t.Fatal("bad secret accepted")
	}
}

func TestSecretAndURI(t *testing.T) {
	s := NewSecret()
	if len(s) != 32 {
		t.Fatalf("len %d", len(s))
	}
	if _, err := Code(s, 1); err != nil {
		t.Fatal(err)
	}
	u := URI(s, "a b@x.com")
	if !strings.HasPrefix(u, "otpauth://totp/IWillBeGoogler:") || !strings.Contains(u, "secret="+s) || !strings.Contains(u, "digits=6") {
		t.Fatalf("uri %s", u)
	}
}

func TestVerify(t *testing.T) {
	now := time.Unix(1_000_000_000, 0)
	cur := Step(now)
	for _, d := range []int64{-1, 0, 1} {
		code, _ := Code(rfcSecret, cur+d)
		step, ok := Verify(rfcSecret, " "+code[:3]+" "+code[3:], now)
		if !ok || step != cur+d {
			t.Errorf("d=%d ok=%v step=%d", d, ok, step)
		}
	}
	far, _ := Code(rfcSecret, cur+5)
	if _, ok := Verify(rfcSecret, far, now); ok {
		t.Error("far step accepted")
	}
	if _, ok := Verify(rfcSecret, "123", now); ok {
		t.Error("short accepted")
	}
	if _, ok := Verify("!!!", "123456", now); ok {
		t.Error("bad secret accepted")
	}
}

func TestBackupCodes(t *testing.T) {
	codes := NewBackupCodes()
	if len(codes) != Backups {
		t.Fatal(len(codes))
	}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' || !IsBackupCode(c) {
			t.Errorf("bad %q", c)
		}
	}
	h := HashBackupCode("ABCDE-FGHJK")
	if h != HashBackupCode(" abcde fghjk ") || h != HashBackupCode("abcdefghjk") || len(h) != 64 {
		t.Fatal("hash not normalized")
	}
	if HashBackupCode("a") == HashBackupCode("b") {
		t.Fatal("collision")
	}
	if IsBackupCode("123456") || !IsBackupCode("abcde fghjk") {
		t.Fatal("IsBackupCode")
	}
}

func TestState(t *testing.T) {
	now := time.Now()
	if (State{}).Enabled() || !(State{EnabledAt: &now}).Enabled() {
		t.Fatal("Enabled")
	}
	if ErrBadCode == nil || ErrNotEnabled == nil || ErrEnabled == nil || ErrNoSetup == nil {
		t.Fatal("errs")
	}
}
